package tools

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/context-link-mcp/context-link/internal/store"
	"github.com/context-link-mcp/context-link/internal/vectorstore"
)

// setupSearchDB opens a migrated test DB and seeds symbols + embeddings for search tests.
// Returns the DB, the MockEmbedder used, and the repo name.
func setupSearchDB(t *testing.T) (*store.DB, *vectorstore.MockEmbedder, string) {
	t.Helper()
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "test.db"))
	require.NoError(t, err, "open db")
	t.Cleanup(func() { db.Close() }) //nolint:errcheck
	require.NoError(t, store.Migrate(db), "migrate")

	const repo = "searchrepo"
	embedder := vectorstore.NewMockEmbedder(0)
	ctx := context.Background()

	// Insert symbols with distinct kinds and file paths.
	symbols := []struct {
		name, kind, filePath string
	}{
		{"validateToken", "function", "src/auth/token.ts"},
		{"UserAuth", "class", "src/auth/auth.ts"},
		{"Repository", "interface", "src/db/repo.ts"},
		{"connectDB", "function", "src/db/connect.ts"},
	}

	for _, s := range symbols {
		res, err := db.ExecContext(ctx, `
			INSERT INTO symbols (repo_name, name, qualified_name, kind, file_path, content_hash, code_block, start_line, end_line, language)
			VALUES (?, ?, ?, ?, ?, 'hash1', 'code', 1, 10, 'typescript')
		`, repo, s.name, s.name, s.kind, s.filePath)
		require.NoError(t, err)
		id, err := res.LastInsertId()
		require.NoError(t, err)

		// Use SymbolEmbedText as the real pipeline would.
		text := vectorstore.SymbolEmbedText(s.kind, s.name, "code")
		vec, err := embedder.EmbedOne(ctx, text)
		require.NoError(t, err)
		require.NoError(t, vectorstore.UpsertEmbedding(ctx, db, id, repo, vec))
	}

	return db, embedder, repo
}

func TestSemanticSearch_NoQuery(t *testing.T) {
	t.Parallel()
	db, emb, repo := setupSearchDB(t)

	handler := semanticSearchHandler(db, emb, repo, nil)
	result, err := handler(context.Background(), mcp.CallToolRequest{})
	require.NoError(t, err)
	assert.True(t, result.IsError, "missing query should return error")
}

func TestSemanticSearch_NilEmbedder(t *testing.T) {
	t.Parallel()
	db, _, repo := setupSearchDB(t)

	handler := semanticSearchHandler(db, nil, repo, nil)
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{"query": "token validation"}
	result, err := handler(context.Background(), req)
	require.NoError(t, err)
	assert.True(t, result.IsError, "nil embedder should return error")
}

func TestSemanticSearch_ReturnsResults(t *testing.T) {
	t.Parallel()
	db, emb, repo := setupSearchDB(t)

	handler := semanticSearchHandler(db, emb, repo, nil)
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"query":          "function validateToken",
		"min_similarity": 0.0, // allow all results through
	}

	result, err := handler(context.Background(), req)
	require.NoError(t, err)
	require.False(t, result.IsError, "should not be an error")

	// Parse JSON response.
	raw, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)

	var resp semanticSearchResponse
	require.NoError(t, json.Unmarshal([]byte(raw.Text), &resp))

	assert.Greater(t, len(resp.Results), 0, "should return at least one result")
	assert.Equal(t, "function validateToken", resp.Metadata.Query)
	assert.GreaterOrEqual(t, resp.Metadata.TimingMs, int64(0))
}

func TestSemanticSearch_TopKLimit(t *testing.T) {
	t.Parallel()
	db, emb, repo := setupSearchDB(t)

	handler := semanticSearchHandler(db, emb, repo, nil)
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"query":          "auth",
		"top_k":          float64(2),
		"min_similarity": 0.0,
	}

	result, err := handler(context.Background(), req)
	require.NoError(t, err)
	require.False(t, result.IsError)

	raw, _ := result.Content[0].(mcp.TextContent)
	var resp semanticSearchResponse
	require.NoError(t, json.Unmarshal([]byte(raw.Text), &resp))

	assert.LessOrEqual(t, len(resp.Results), 2, "top_k=2 should cap results at 2")
}

func TestSemanticSearch_KindFilter(t *testing.T) {
	t.Parallel()
	db, emb, repo := setupSearchDB(t)

	handler := semanticSearchHandler(db, emb, repo, nil)
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"query":          "auth",
		"kind":           "class",
		"min_similarity": 0.0,
	}

	result, err := handler(context.Background(), req)
	require.NoError(t, err)
	require.False(t, result.IsError)

	raw, _ := result.Content[0].(mcp.TextContent)
	var resp semanticSearchResponse
	require.NoError(t, json.Unmarshal([]byte(raw.Text), &resp))

	for _, r := range resp.Results {
		assert.Equal(t, "class", r.Kind, "kind filter should only return classes")
	}
}

func TestSemanticSearch_FilePathPrefixFilter(t *testing.T) {
	t.Parallel()
	db, emb, repo := setupSearchDB(t)

	handler := semanticSearchHandler(db, emb, repo, nil)
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"query":            "database",
		"file_path_prefix": "src/db/",
		"min_similarity":   0.0,
	}

	result, err := handler(context.Background(), req)
	require.NoError(t, err)
	require.False(t, result.IsError)

	raw, _ := result.Content[0].(mcp.TextContent)
	var resp semanticSearchResponse
	require.NoError(t, json.Unmarshal([]byte(raw.Text), &resp))

	for _, r := range resp.Results {
		assert.True(t, len(r.FilePath) >= 7 && r.FilePath[:7] == "src/db/",
			"file_path_prefix filter should only return src/db/ files, got: %s", r.FilePath)
	}
}

func TestSemanticSearch_TopKOutOfRange_Clamped(t *testing.T) {
	t.Parallel()
	db, emb, repo := setupSearchDB(t)

	handler := semanticSearchHandler(db, emb, repo, nil)
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"query":          "function",
		"top_k":          float64(999), // should be clamped to 10
		"min_similarity": 0.0,
	}

	result, err := handler(context.Background(), req)
	require.NoError(t, err)
	assert.False(t, result.IsError, "out-of-range top_k should be clamped, not error")
}

func TestSemanticSearch_MetadataFields(t *testing.T) {
	t.Parallel()
	db, emb, repo := setupSearchDB(t)

	handler := semanticSearchHandler(db, emb, repo, nil)
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"query":          "token",
		"min_similarity": 0.0,
	}

	result, err := handler(context.Background(), req)
	require.NoError(t, err)
	require.False(t, result.IsError)

	raw, _ := result.Content[0].(mcp.TextContent)
	var resp semanticSearchResponse
	require.NoError(t, json.Unmarshal([]byte(raw.Text), &resp))

	assert.Equal(t, "token", resp.Metadata.Query)
	assert.Equal(t, len(resp.Results), resp.Metadata.TotalResults)
	assert.GreaterOrEqual(t, resp.Metadata.TimingMs, int64(0))
}
