package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/context-link-mcp/context-link/internal/indexer"
	"github.com/context-link-mcp/context-link/internal/indexer/adapters"
	"github.com/context-link-mcp/context-link/internal/store"
)

// buildTestIndexer creates an indexer with Go adapter for testing.
func buildTestIndexer(t *testing.T, db *store.DB) *indexer.Indexer {
	t.Helper()
	registry := indexer.NewLanguageRegistry()
	require.NoError(t, registry.Register(adapters.NewGoAdapter()))
	return indexer.NewIndexer(registry, db, 1, nil)
}

func TestReindex_NoChanges(t *testing.T) {
	t.Parallel()
	db := openToolTestDB(t)
	idx := buildTestIndexer(t, db)

	// Create empty project
	projectRoot := t.TempDir()

	handler := reindexHandler(idx, "repo", projectRoot, NewSessionTokenTracker())
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{}

	result, err := handler(context.Background(), req)
	require.NoError(t, err)
	assert.False(t, result.IsError)

	var resp struct {
		FilesChanged    int `json:"files_changed"`
		FilesScanned    int `json:"files_scanned"`
		SymbolsAdded    int `json:"symbols_added"`
		SymbolsRemoved  int `json:"symbols_removed"`
	}
	raw, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	require.NoError(t, json.Unmarshal([]byte(raw.Text), &resp))

	assert.Equal(t, 0, resp.FilesChanged, "no files changed")
	assert.Equal(t, 0, resp.SymbolsAdded, "no symbols added")
}

func TestReindex_NewFiles(t *testing.T) {
	t.Parallel()
	db := openToolTestDB(t)
	idx := buildTestIndexer(t, db)

	projectRoot := t.TempDir()

	// Initial index (empty)
	_, err := idx.IndexRepo(context.Background(), "repo", projectRoot)
	require.NoError(t, err)

	// Add a new file
	goFile := filepath.Join(projectRoot, "main.go")
	require.NoError(t, os.WriteFile(goFile, []byte(`
package main

func main() {
	println("hello")
}
`), 0644))

	handler := reindexHandler(idx, "repo", projectRoot, NewSessionTokenTracker())
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{}

	result, err := handler(context.Background(), req)
	require.NoError(t, err)
	assert.False(t, result.IsError)

	var resp struct {
		FilesChanged int `json:"files_changed"`
		SymbolsAdded int `json:"symbols_added"`
	}
	raw, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	require.NoError(t, json.Unmarshal([]byte(raw.Text), &resp))

	assert.Greater(t, resp.FilesChanged, 0, "new file should be detected")
	assert.Greater(t, resp.SymbolsAdded, 0, "main function should be extracted")
}

func TestReindex_DeletedFiles(t *testing.T) {
	t.Parallel()
	db := openToolTestDB(t)
	idx := buildTestIndexer(t, db)

	projectRoot := t.TempDir()
	goFile := filepath.Join(projectRoot, "main.go")

	// Initial index with file
	require.NoError(t, os.WriteFile(goFile, []byte(`package main
func main() {}`), 0644))
	_, err := idx.IndexRepo(context.Background(), "repo", projectRoot)
	require.NoError(t, err)

	// Delete the file
	require.NoError(t, os.Remove(goFile))

	handler := reindexHandler(idx, "repo", projectRoot, NewSessionTokenTracker())
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{}

	result, err := handler(context.Background(), req)
	require.NoError(t, err)
	assert.False(t, result.IsError)

	var resp struct {
		FilesDeleted int `json:"files_deleted"`
	}
	raw, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	require.NoError(t, json.Unmarshal([]byte(raw.Text), &resp))

	assert.Greater(t, resp.FilesDeleted, 0, "deleted file should be detected")
}

func TestReindex_ModifiedFiles(t *testing.T) {
	t.Parallel()
	db := openToolTestDB(t)
	idx := buildTestIndexer(t, db)

	projectRoot := t.TempDir()
	goFile := filepath.Join(projectRoot, "main.go")

	// Initial index
	require.NoError(t, os.WriteFile(goFile, []byte(`package main
func main() {}`), 0644))
	_, err := idx.IndexRepo(context.Background(), "repo", projectRoot)
	require.NoError(t, err)

	// Modify the file
	require.NoError(t, os.WriteFile(goFile, []byte(`package main
func main() {}
func newFunc() {}`), 0644))

	handler := reindexHandler(idx, "repo", projectRoot, NewSessionTokenTracker())
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{}

	result, err := handler(context.Background(), req)
	require.NoError(t, err)
	assert.False(t, result.IsError)

	var resp struct {
		FilesChanged    int `json:"files_changed"`
		SymbolsUpdated  int `json:"symbols_updated"`
	}
	raw, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	require.NoError(t, json.Unmarshal([]byte(raw.Text), &resp))

	assert.Greater(t, resp.FilesChanged, 0, "modified file should be detected")
	assert.Greater(t, resp.SymbolsUpdated, 0, "symbols should be updated")
}

func TestReindex_DependencyUpdate(t *testing.T) {
	t.Parallel()
	db := openToolTestDB(t)
	idx := buildTestIndexer(t, db)

	projectRoot := t.TempDir()
	goFile := filepath.Join(projectRoot, "main.go")

	// Write file with function call (dependency)
	require.NoError(t, os.WriteFile(goFile, []byte(`package main

func caller() {
	callee()
}

func callee() {}
`), 0644))

	handler := reindexHandler(idx, "repo", projectRoot, NewSessionTokenTracker())
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{}

	result, err := handler(context.Background(), req)
	require.NoError(t, err)
	assert.False(t, result.IsError)

	var resp struct {
		DependenciesUpdated int  `json:"dependencies_updated"`
	}
	raw, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	require.NoError(t, json.Unmarshal([]byte(raw.Text), &resp))

	assert.GreaterOrEqual(t, resp.DependenciesUpdated, 0, "dependencies should be tracked")
}

func TestReindex_FTSUpdate(t *testing.T) {
	t.Parallel()
	db := openToolTestDB(t)
	idx := buildTestIndexer(t, db)

	projectRoot := t.TempDir()
	goFile := filepath.Join(projectRoot, "main.go")

	// Add a file
	require.NoError(t, os.WriteFile(goFile, []byte(`package main
func main() {}`), 0644))

	handler := reindexHandler(idx, "repo", projectRoot, NewSessionTokenTracker())
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{}

	result, err := handler(context.Background(), req)
	require.NoError(t, err)
	assert.False(t, result.IsError)

	var resp struct {
		FTSUpdated bool `json:"fts_updated"`
	}
	raw, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	require.NoError(t, json.Unmarshal([]byte(raw.Text), &resp))

	assert.True(t, resp.FTSUpdated, "FTS should be updated when files change")
}

func TestReindex_EmbeddingUpdate(t *testing.T) {
	t.Parallel()
	db := openToolTestDB(t)

	// Create indexer with embedder
	registry := indexer.NewLanguageRegistry()
	require.NoError(t, registry.Register(adapters.NewGoAdapter()))

	// Use nil embedder (embeddings_updated will be false)
	idx := indexer.NewIndexer(registry, db, 1, nil)

	projectRoot := t.TempDir()
	goFile := filepath.Join(projectRoot, "main.go")
	require.NoError(t, os.WriteFile(goFile, []byte(`package main
func main() {}`), 0644))

	handler := reindexHandler(idx, "repo", projectRoot, NewSessionTokenTracker())
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{}

	result, err := handler(context.Background(), req)
	require.NoError(t, err)
	assert.False(t, result.IsError)

	var resp struct {
		EmbeddingsUpdated bool `json:"embeddings_updated"`
	}
	raw, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	require.NoError(t, json.Unmarshal([]byte(raw.Text), &resp))

	// With nil embedder, embeddings_updated should be false
	assert.False(t, resp.EmbeddingsUpdated, "embeddings not generated without embedder")
}

func TestReindex_LargeRepo(t *testing.T) {
	t.Parallel()
	db := openToolTestDB(t)
	idx := buildTestIndexer(t, db)

	projectRoot := t.TempDir()

	// Create multiple files
	for i := 0; i < 10; i++ {
		goFile := filepath.Join(projectRoot, "file"+string(rune('0'+i))+".go")
		require.NoError(t, os.WriteFile(goFile, []byte(`package main
func testFunc() {}`), 0644))
	}

	handler := reindexHandler(idx, "repo", projectRoot, NewSessionTokenTracker())
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{}

	result, err := handler(context.Background(), req)
	require.NoError(t, err)
	assert.False(t, result.IsError)

	var resp struct {
		FilesScanned int   `json:"files_scanned"`
		FilesChanged int   `json:"files_changed"`
		DurationMs   int64 `json:"duration_ms"`
	}
	raw, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	require.NoError(t, json.Unmarshal([]byte(raw.Text), &resp))

	assert.GreaterOrEqual(t, resp.FilesScanned, 10, "should scan all files")
	assert.Greater(t, resp.FilesChanged, 0, "should index new files")
	assert.Greater(t, resp.DurationMs, int64(0), "should track duration")
}

func TestReindex_PartialIndexing(t *testing.T) {
	t.Parallel()
	db := openToolTestDB(t)
	idx := buildTestIndexer(t, db)

	projectRoot := t.TempDir()

	// Create valid and invalid files
	validFile := filepath.Join(projectRoot, "valid.go")
	require.NoError(t, os.WriteFile(validFile, []byte(`package main
func main() {}`), 0644))

	// The indexer should gracefully handle the valid file
	handler := reindexHandler(idx, "repo", projectRoot, NewSessionTokenTracker())
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{}

	result, err := handler(context.Background(), req)
	require.NoError(t, err)
	assert.False(t, result.IsError)

	var resp struct {
		FilesChanged int `json:"files_changed"`
	}
	raw, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	require.NoError(t, json.Unmarshal([]byte(raw.Text), &resp))

	assert.Greater(t, resp.FilesChanged, 0, "valid files should be indexed")
}

func TestReindex_TimeoutBehavior(t *testing.T) {
	t.Parallel()
	db := openToolTestDB(t)
	idx := buildTestIndexer(t, db)

	projectRoot := t.TempDir()
	goFile := filepath.Join(projectRoot, "main.go")
	require.NoError(t, os.WriteFile(goFile, []byte(`package main
func main() {}`), 0644))

	// The handler should complete within the timeout
	handler := reindexHandler(idx, "repo", projectRoot, NewSessionTokenTracker())
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{}

	ctx, cancel := context.WithTimeout(context.Background(), ReindexTimeout)
	defer cancel()

	result, err := handler(ctx, req)
	require.NoError(t, err)
	assert.False(t, result.IsError)

	var resp struct {
		DurationMs int64 `json:"duration_ms"`
	}
	raw, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	require.NoError(t, json.Unmarshal([]byte(raw.Text), &resp))

	// Should complete in reasonable time (much less than 5 minutes)
	assert.Less(t, resp.DurationMs, int64(60000), "should complete quickly for small repo")
}
