package tools

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetFileSkeletonBatch_SingleFile(t *testing.T) {
	t.Parallel()
	db := openToolTestDB(t)

	// Insert test symbols for test.go file
	ctx := context.Background()
	_, err := db.ExecContext(ctx, `
		INSERT INTO symbols (repo_name, name, qualified_name, kind, file_path, content_hash, code_block, start_line, end_line, language)
		VALUES (?, ?, ?, ?, ?, 'hash1', ?, 1, 5, 'go')
	`, "repo", "funcA", "funcA", "function", "test.go", "function funcA() { return 42 }")
	require.NoError(t, err)

	_, err = db.ExecContext(ctx, `
		INSERT INTO symbols (repo_name, name, qualified_name, kind, file_path, content_hash, code_block, start_line, end_line, language)
		VALUES (?, ?, ?, ?, ?, 'hash2', ?, 1, 5, 'go')
	`, "repo", "funcB", "funcB", "function", "test.go", "function funcB() { return 'hello' }")
	require.NoError(t, err)

	// Create temporary directory structure for file existence check
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.go")
	require.NoError(t, os.WriteFile(testFile, []byte("package test"), 0600))

	handler := fileSkeletonHandlerBatch(db, "repo", tmpDir, NewSessionTokenTracker())
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"file_path": "test.go",
	}

	result, err := handler(context.Background(), req)
	require.NoError(t, err)
	assert.False(t, result.IsError)

	// Verify response structure (single file processed as 1-item batch)
	raw, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, raw.Text, `"total_files":1`)
	assert.Contains(t, raw.Text, `"success_count":1`)
}

func TestGetFileSkeletonBatch_MultipleFiles(t *testing.T) {
	t.Parallel()
	db := openToolTestDB(t)

	// Insert symbols for multiple files
	ctx := context.Background()
	_, err := db.ExecContext(ctx, `
		INSERT INTO symbols (repo_name, name, qualified_name, kind, file_path, content_hash, code_block, start_line, end_line, language)
		VALUES (?, ?, ?, ?, ?, 'hash1', ?, 1, 5, 'go')
	`, "repo", "funcA", "funcA", "function", "file1.go", "function funcA() { return 1 }")
	require.NoError(t, err)

	_, err = db.ExecContext(ctx, `
		INSERT INTO symbols (repo_name, name, qualified_name, kind, file_path, content_hash, code_block, start_line, end_line, language)
		VALUES (?, ?, ?, ?, ?, 'hash2', ?, 1, 5, 'go')
	`, "repo", "funcB", "funcB", "function", "file2.go", "function funcB() { return 2 }")
	require.NoError(t, err)

	_, err = db.ExecContext(ctx, `
		INSERT INTO symbols (repo_name, name, qualified_name, kind, file_path, content_hash, code_block, start_line, end_line, language)
		VALUES (?, ?, ?, ?, ?, 'hash3', ?, 1, 5, 'go')
	`, "repo", "funcC", "funcC", "function", "file3.go", "function funcC() { return 3 }")
	require.NoError(t, err)

	// Create temporary files
	tmpDir := t.TempDir()
	for _, fname := range []string{"file1.go", "file2.go", "file3.go"} {
		require.NoError(t, os.WriteFile(filepath.Join(tmpDir, fname), []byte("package test"), 0600))
	}

	handler := fileSkeletonHandlerBatch(db, "repo", tmpDir, NewSessionTokenTracker())
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"file_path": []interface{}{"file1.go", "file2.go", "file3.go"},
	}

	result, err := handler(context.Background(), req)
	require.NoError(t, err)
	assert.False(t, result.IsError)

	// Verify batch response
	raw, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, raw.Text, `"total_files":3`)
	assert.Contains(t, raw.Text, `"success_count":3`)
	assert.Contains(t, raw.Text, `"error_count":0`)
	assert.Contains(t, raw.Text, "file1.go")
	assert.Contains(t, raw.Text, "file2.go")
	assert.Contains(t, raw.Text, "file3.go")
}

func TestGetFileSkeletonBatch_PartialErrors(t *testing.T) {
	t.Parallel()
	db := openToolTestDB(t)

	// Insert symbols for only test.go
	ctx := context.Background()
	_, err := db.ExecContext(ctx, `
		INSERT INTO symbols (repo_name, name, qualified_name, kind, file_path, content_hash, code_block, start_line, end_line, language)
		VALUES (?, ?, ?, ?, ?, 'hash1', ?, 1, 5, 'go')
	`, "repo", "funcA", "funcA", "function", "test.go", "function funcA() { return 1 }")
	require.NoError(t, err)

	// Create only test.go, not nonexistent.go
	tmpDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "test.go"), []byte("package test"), 0600))

	handler := fileSkeletonHandlerBatch(db, "repo", tmpDir, NewSessionTokenTracker())
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"file_path": []interface{}{"test.go", "nonexistent.go"},
	}

	result, err := handler(context.Background(), req)
	require.NoError(t, err)
	assert.False(t, result.IsError)

	// Verify partial success
	raw, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, raw.Text, `"total_files":2`)
	assert.Contains(t, raw.Text, `"success_count":1`)
	assert.Contains(t, raw.Text, `"error_count":1`)
	assert.Contains(t, raw.Text, "file does not exist")
}

func TestGetFileSkeletonBatch_BatchSizeLimit(t *testing.T) {
	t.Parallel()
	db := openToolTestDB(t)

	// Create 51 file paths (exceeds max of 50)
	filePaths := make([]interface{}, 51)
	for i := 0; i < 51; i++ {
		filePaths[i] = "file" + string(rune('0'+i)) + ".go"
	}

	handler := fileSkeletonHandlerBatch(db, "repo", "", NewSessionTokenTracker())
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"file_path": filePaths,
	}

	result, err := handler(context.Background(), req)
	require.NoError(t, err)
	assert.True(t, result.IsError, "should reject batch size > 50")

	raw, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, raw.Text, "batch size limit exceeded")
	assert.Contains(t, raw.Text, "max: 50")
}

func TestGetFileSkeletonBatch_EmptyFilePath(t *testing.T) {
	t.Parallel()
	db := openToolTestDB(t)

	// Insert symbols for the valid files
	ctx := context.Background()
	_, err := db.ExecContext(ctx, `
		INSERT INTO symbols (repo_name, name, qualified_name, kind, file_path, content_hash, code_block, start_line, end_line, language)
		VALUES (?, ?, ?, ?, ?, 'hash1', ?, 1, 5, 'go')
	`, "repo", "func1", "func1", "function", "valid.go", "function func1() { return 1 }")
	require.NoError(t, err)

	_, err = db.ExecContext(ctx, `
		INSERT INTO symbols (repo_name, name, qualified_name, kind, file_path, content_hash, code_block, start_line, end_line, language)
		VALUES (?, ?, ?, ?, ?, 'hash2', ?, 1, 5, 'go')
	`, "repo", "func2", "func2", "function", "also-valid.go", "function func2() { return 2 }")
	require.NoError(t, err)

	handler := fileSkeletonHandlerBatch(db, "repo", "", NewSessionTokenTracker())
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"file_path": []interface{}{"valid.go", "", "also-valid.go"},
	}

	result, err := handler(context.Background(), req)
	require.NoError(t, err)
	assert.False(t, result.IsError)

	// Empty string should produce an error for that item
	raw, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, raw.Text, `"success_count":2`)
	assert.Contains(t, raw.Text, `"error_count":1`)
	assert.Contains(t, raw.Text, "file_path is empty")
}

func TestGetFileSkeletonBatch_NoSymbolsFound(t *testing.T) {
	t.Parallel()
	db := openToolTestDB(t)

	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "empty.go")
	require.NoError(t, os.WriteFile(testFile, []byte("package test"), 0600))

	handler := fileSkeletonHandlerBatch(db, "repo", tmpDir, NewSessionTokenTracker())
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"file_path": "empty.go",
	}

	result, err := handler(context.Background(), req)
	require.NoError(t, err)
	assert.False(t, result.IsError)

	raw, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, raw.Text, `"error_count":1`)
	assert.Contains(t, raw.Text, "no symbols found")
}

func TestGetFileSkeletonBatch_MissingParameter(t *testing.T) {
	t.Parallel()
	db := openToolTestDB(t)

	handler := fileSkeletonHandlerBatch(db, "repo", "", NewSessionTokenTracker())
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{} // No file_path parameter

	result, err := handler(context.Background(), req)
	require.NoError(t, err)
	assert.True(t, result.IsError)

	raw, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, raw.Text, "file_path")
	assert.Contains(t, raw.Text, "required")
}

func TestGetFileSkeletonBatch_InvalidParameterType(t *testing.T) {
	t.Parallel()
	db := openToolTestDB(t)

	handler := fileSkeletonHandlerBatch(db, "repo", "", NewSessionTokenTracker())
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"file_path": 123, // Invalid type (int)
	}

	result, err := handler(context.Background(), req)
	require.NoError(t, err)
	assert.True(t, result.IsError)

	raw, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, raw.Text, "invalid file_path parameter")
}
