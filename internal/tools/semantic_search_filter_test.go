package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"path/filepath"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/context-link-mcp/context-link/internal/store"
	"github.com/context-link-mcp/context-link/internal/vectorstore"
)

// fixedEmbedder embeds every query as the same vector, so tests control
// similarities exactly through the stored vectors.
type fixedEmbedder struct{ vec []float32 }

func (f fixedEmbedder) EmbedOne(context.Context, string) ([]float32, error) { return f.vec, nil }
func (f fixedEmbedder) EmbedBatch(_ context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i := range texts {
		out[i] = f.vec
	}
	return out, nil
}
func (f fixedEmbedder) Dim() int     { return len(f.vec) }
func (f fixedEmbedder) Close() error { return nil }

// vecWithSim returns a unit vector whose dot product with
// (1, 0, 0, 0) is sim.
func vecWithSim(sim float32) []float32 {
	v := []float32{sim, 0, 0, 0}
	v[1] = float32(math.Sqrt(1 - float64(sim)*float64(sim)))
	return v
}

type searchFixture struct {
	name, kind, path string
	sim              float32
	fts              bool // also index the symbol in FTS
}

func seedSearch(t *testing.T, repo string, fx []searchFixture) (*store.DB, map[string]int64) {
	t.Helper()
	ctx := context.Background()
	db, err := store.Open(filepath.Join(t.TempDir(), "s.db"))
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() }) //nolint:errcheck
	require.NoError(t, store.Migrate(db))

	ids := make(map[string]int64, len(fx))
	for _, f := range fx {
		res, err := db.ExecContext(ctx, `
			INSERT INTO symbols (repo_name, name, qualified_name, kind, file_path, content_hash, code_block, start_line, end_line, language)
			VALUES (?, ?, ?, ?, ?, 'h', 'code', 1, 2, 'typescript')`, repo, f.name, f.name, f.kind, f.path)
		require.NoError(t, err)
		id, err := res.LastInsertId()
		require.NoError(t, err)
		ids[f.name] = id
		require.NoError(t, vectorstore.UpsertEmbedding(ctx, db, id, repo, vecWithSim(f.sim)))
		if f.fts {
			require.NoError(t, store.BatchInsertFTSSymbols(ctx, db, []store.FTSSymbol{{
				SymbolID: id, RepoName: repo, Name: f.name, QualifiedName: f.name, Kind: f.kind,
			}}))
		}
	}
	return db, ids
}

var queryVec = []float32{1, 0, 0, 0}

// TestSemanticSearch_FilterBeforeTruncate covers M14: kind/file_path_prefix
// were applied after cutting the fused list to top_k*2, so a filtered query
// whose matches rank below that cut returned nothing.
func TestSemanticSearch_FilterBeforeTruncate(t *testing.T) {
	t.Parallel()
	const repo = "r"
	var fx []searchFixture
	for i := 0; i < 40; i++ {
		fx = append(fx, searchFixture{name: fmt.Sprintf("fn%02d", i), kind: "function", path: "src/util/x.ts", sim: 0.95})
	}
	fx = append(fx, searchFixture{name: "TokenStore", kind: "class", path: "src/auth/store.ts", sim: 0.5})
	db, _ := seedSearch(t, repo, fx)
	handler := semanticSearchHandler(db, fixedEmbedder{queryVec}, repo, NewSessionTokenTracker(), nil)

	tests := []struct {
		name string
		args map[string]any
	}{
		{"kind", map[string]any{"kind": "class"}},
		{"file_path_prefix", map[string]any{"file_path_prefix": "src/auth/"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			args := map[string]any{"query": "token store", "top_k": float64(5)}
			for k, v := range tt.args {
				args[k] = v
			}
			req := mcp.CallToolRequest{}
			req.Params.Arguments = args
			result, err := handler(context.Background(), req)
			require.NoError(t, err)
			require.False(t, result.IsError)
			raw, _ := result.Content[0].(mcp.TextContent)
			var resp semanticSearchResponse
			require.NoError(t, json.Unmarshal([]byte(raw.Text), &resp))
			require.Len(t, resp.Results, 1, "the only matching symbol must be found")
			assert.Equal(t, "TokenStore", resp.Results[0].SymbolName)
		})
	}
}

// TestHybridSearch_MinSimilarityOnFTSOnlyHits covers M14: FTS-only hits
// skipped min_similarity and were reported with similarity 0.
func TestHybridSearch_MinSimilarityOnFTSOnlyHits(t *testing.T) {
	t.Parallel()
	const repo = "r"
	db, ids := seedSearch(t, repo, []searchFixture{
		{name: "alpha", kind: "function", path: "a.ts", sim: 0.99},
		{name: "beta", kind: "function", path: "a.ts", sim: 0.98},
		{name: "gamma", kind: "function", path: "a.ts", sim: 0.97},
		// Keyword match, semantically close, but outside the KNN fetch (topK*3=3).
		{name: "validateToken", kind: "function", path: "auth.ts", sim: 0.9, fts: true},
		// Keyword match, semantically unrelated: below the threshold.
		{name: "tokenValidateUnrelated", kind: "function", path: "x.ts", sim: 0.1, fts: true},
	})

	for _, cache := range []*vectorstore.VectorCache{nil, vectorstore.NewVectorCache(repo)} {
		hits, err := HybridSearch(context.Background(), db, fixedEmbedder{queryVec}, cache, repo,
			"validate token", 1, 0.3, store.SymbolFilter{})
		require.NoError(t, err)

		byID := map[int64]HybridHit{}
		for _, h := range hits {
			byID[h.SymbolID] = h
		}
		assert.NotContains(t, byID, ids["tokenValidateUnrelated"], "FTS-only hit below min_similarity must be dropped")
		got, ok := byID[ids["validateToken"]]
		require.True(t, ok, "FTS-only hit above min_similarity is kept, got %+v", hits)
		assert.Equal(t, "fts", got.MatchSource)
		assert.InDelta(t, 0.9, got.Similarity, 1e-4, "FTS-only hits report their real cosine similarity")
		for _, h := range hits {
			assert.GreaterOrEqual(t, h.Similarity, float32(0.3), "every hit honours min_similarity")
		}
	}
}
