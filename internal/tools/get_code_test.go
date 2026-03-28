package tools

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/context-link-mcp/context-link/internal/store"
)

// openToolTestDB opens a migrated temp DB for tool handler tests.
func openToolTestDB(t *testing.T) *store.DB {
	t.Helper()
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "tools_test.db"))
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() }) //nolint:errcheck
	require.NoError(t, store.Migrate(db))
	return db
}

// insertSymbol inserts a minimal symbol into the DB and returns its ID.
func insertSymbol(t *testing.T, db *store.DB, repo, name, kind, code string) int64 {
	t.Helper()
	res, err := db.ExecContext(context.Background(), `
		INSERT INTO symbols (repo_name, name, qualified_name, kind, file_path, content_hash, code_block, start_line, end_line, language)
		VALUES (?, ?, ?, ?, 'test.ts', 'hash1', ?, 1, 5, 'typescript')
	`, repo, name, name, kind, code)
	require.NoError(t, err)
	id, err := res.LastInsertId()
	require.NoError(t, err)
	return id
}

func TestGetCodeBySymbolHandler_Found(t *testing.T) {
	t.Parallel()
	db := openToolTestDB(t)
	insertSymbol(t, db, "repo", "validateToken", "function", "function validateToken() {}")

	handler := getCodeBySymbolHandlerBatch(db, "repo", NewSessionTokenTracker())
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{"symbol_name": "validateToken"}

	result, err := handler(context.Background(), req)
	require.NoError(t, err)
	assert.False(t, result.IsError, "should find an existing symbol")
}

func TestGetCodeBySymbolHandler_NotFound(t *testing.T) {
	t.Parallel()
	db := openToolTestDB(t)

	handler := getCodeBySymbolHandlerBatch(db, "repo", NewSessionTokenTracker())
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{"symbol_name": "nonExistentSymbol"}

	result, err := handler(context.Background(), req)
	require.NoError(t, err)
	assert.False(t, result.IsError, "batch handler returns success with error in results")

	// With batch support, errors are in the results array
	raw, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, raw.Text, `"error_count":1`)
	assert.Contains(t, raw.Text, "not found")
}

func TestGetCodeBySymbolHandler_MissingParam(t *testing.T) {
	t.Parallel()
	db := openToolTestDB(t)

	handler := getCodeBySymbolHandlerBatch(db, "repo", NewSessionTokenTracker())
	result, err := callHandler(t, handler) // empty request
	require.NoError(t, err)
	assert.True(t, result.IsError, "missing symbol_name should return error")
}

func TestGetCodeBySymbolHandler_DepthClamped(t *testing.T) {
	t.Parallel()
	db := openToolTestDB(t)
	insertSymbol(t, db, "repo", "myFunc", "function", "func myFunc() {}")

	handler := getCodeBySymbolHandlerBatch(db, "repo", NewSessionTokenTracker())
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"symbol_name": "myFunc",
		"depth":       float64(99), // should be clamped to 3
	}

	result, err := handler(context.Background(), req)
	require.NoError(t, err)
	assert.False(t, result.IsError, "oversized depth should be clamped not rejected")
}
