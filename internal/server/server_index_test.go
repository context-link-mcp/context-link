package server

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/context-link-mcp/context-link/internal/config"
	"github.com/context-link-mcp/context-link/internal/indexer"
	"github.com/context-link-mcp/context-link/internal/indexer/adapters"
	"github.com/context-link-mcp/context-link/internal/store"
)

// constEmbedder maps every text to the same unit vector, so vector search
// returns every embedded symbol with similarity 1 regardless of the query.
// That isolates the vector path: a query with no FTS match can only find a
// symbol through the vector cache.
type constEmbedder struct{}

func (constEmbedder) Dim() int     { return 4 }
func (constEmbedder) Close() error { return nil }
func (constEmbedder) EmbedOne(context.Context, string) ([]float32, error) {
	return []float32{1, 0, 0, 0}, nil
}
func (e constEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i := range texts {
		out[i], _ = e.EmbedOne(ctx, "")
	}
	return out, nil
}

// noFTSQuery matches no identifier, so only the vector path can return hits.
const noFTSQuery = "zzqqxv"

type indexFixture struct {
	srv  *Server
	idx  *indexer.Indexer
	root string
	repo string
}

func newIndexFixture(t *testing.T) *indexFixture {
	t.Helper()
	root := filepath.Join(t.TempDir(), "proj")
	require.NoError(t, os.MkdirAll(root, 0o755))
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	require.NoError(t, err)
	require.NoError(t, store.Migrate(db))
	t.Cleanup(func() { db.Close() })

	reg := indexer.NewLanguageRegistry()
	require.NoError(t, reg.Register(adapters.NewGoAdapter()))
	emb := constEmbedder{}
	idx := indexer.NewIndexer(reg, db, 1, emb)

	cfg := &config.Config{ProjectRoot: root, RepoName: "proj"}
	return &indexFixture{srv: New(cfg, db, emb, idx, "test"), idx: idx, root: root, repo: "proj"}
}

func (f *indexFixture) write(t *testing.T, name, src string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(f.root, name), []byte(src), 0o644))
}

func (f *indexFixture) call(t *testing.T, tool string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	st := f.srv.mcp.GetTool(tool)
	require.NotNil(t, st, "tool %s not registered", tool)
	req := mcp.CallToolRequest{}
	req.Params.Name = tool
	req.Params.Arguments = args
	res, err := st.Handler(context.Background(), req)
	require.NoError(t, err)
	require.False(t, res.IsError, "%s returned an error: %+v", tool, res.Content)
	return res
}

// searchNames runs semantic_search_symbols and returns the symbol names found.
func (f *indexFixture) searchNames(t *testing.T) []string {
	t.Helper()
	res := f.call(t, "semantic_search_symbols", map[string]any{"query": noFTSQuery, "min_similarity": 0.5})
	text, ok := res.Content[0].(mcp.TextContent)
	require.True(t, ok)
	var body struct {
		Results []struct {
			SymbolName string `json:"symbol_name"`
		} `json:"results"`
	}
	require.NoError(t, json.Unmarshal([]byte(text.Text), &body), text.Text)
	names := make([]string, 0, len(body.Results))
	for _, r := range body.Results {
		names = append(names, r.SymbolName)
	}
	sort.Strings(names)
	return names
}

// TestReindexProject_RefreshesVectorCache is the H2 regression for the
// reindex_project path: after a reindex, semantic search must see changed
// and new symbols instead of the vectors cached before it.
func TestReindexProject_RefreshesVectorCache(t *testing.T) {
	t.Parallel()
	f := newIndexFixture(t)

	f.write(t, "a.go", "package p\n\nfunc Alpha() int { return 1 }\n")
	f.call(t, "reindex_project", nil)
	require.Equal(t, []string{"Alpha"}, f.searchNames(t), "warm the cache")

	// Change a.go (its symbols get new IDs) and add b.go (new symbols).
	f.write(t, "a.go", "package p\n\nfunc Alpha() int { return 2 }\n")
	f.write(t, "b.go", "package p\n\nfunc Beta() int { return 3 }\n")
	f.call(t, "reindex_project", nil)

	assert.Equal(t, []string{"Alpha", "Beta"}, f.searchNames(t))
}

// TestNotifyIndexed_RefreshesVectorCache is the H2 regression for index
// runs serve does not start through a tool (the --watch watcher): after
// NotifyIndexed, semantic search sees the new vectors.
func TestNotifyIndexed_RefreshesVectorCache(t *testing.T) {
	t.Parallel()
	f := newIndexFixture(t)

	f.write(t, "a.go", "package p\n\nfunc Alpha() int { return 1 }\n")
	_, err := f.idx.IndexRepo(context.Background(), f.repo, f.root)
	require.NoError(t, err)
	require.Equal(t, []string{"Alpha"}, f.searchNames(t), "warm the cache")

	f.write(t, "b.go", "package p\n\nfunc Beta() int { return 3 }\n")
	_, err = f.idx.IndexRepo(context.Background(), f.repo, f.root)
	require.NoError(t, err)
	f.srv.NotifyIndexed()

	assert.Equal(t, []string{"Alpha", "Beta"}, f.searchNames(t))
}

// TestServer_UsesConfiguredRepoName is the H7 regression at the server
// level: tools query cfg.RepoName, not filepath.Base of the root.
func TestServer_UsesConfiguredRepoName(t *testing.T) {
	t.Parallel()
	f := newIndexFixture(t)
	f.write(t, "a.go", "package p\n\nfunc Alpha() int { return 1 }\n")

	// Index under a name that differs from the root's directory name.
	_, err := f.idx.IndexRepo(context.Background(), "custom", f.root)
	require.NoError(t, err)
	cfg := &config.Config{ProjectRoot: f.root, RepoName: "custom"}
	f.srv = New(cfg, f.srv.db, f.srv.embedder, f.idx, "test")

	assert.Equal(t, []string{"Alpha"}, f.searchNames(t))
}

func TestRepoName(t *testing.T) {
	t.Parallel()
	root := filepath.Join(t.TempDir(), "proj")
	tests := []struct {
		name string
		cfg  config.Config
		want string
	}{
		{"explicit", config.Config{ProjectRoot: root, RepoName: "custom"}, "custom"},
		{"derived from root", config.Config{ProjectRoot: root}, "proj"},
		{"derived from unclean root", config.Config{ProjectRoot: root + string(filepath.Separator)}, "proj"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := &Server{cfg: &tc.cfg}
			assert.Equal(t, tc.want, s.repoName())
		})
	}
}
