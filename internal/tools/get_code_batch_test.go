package tools

import (
	"context"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetCodeBatch_SingleSymbol(t *testing.T) {
	t.Parallel()
	db := openToolTestDB(t)

	// Insert a test symbol
	insertSymbol(t, db, "repo", "testFunc", "function", "function testFunc() { return 42 }")

	handler := getCodeBySymbolHandlerBatch(db, "repo", NewSessionTokenTracker())
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"symbol_name": "testFunc",
		"depth":       0,
	}

	result, err := handler(context.Background(), req)
	require.NoError(t, err)
	assert.False(t, result.IsError)

	// Verify response structure (single symbol processed as 1-item batch)
	raw, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, raw.Text, `"total_symbols":1`)
	assert.Contains(t, raw.Text, `"success_count":1`)
	assert.Contains(t, raw.Text, `"error_count":0`)
	assert.Contains(t, raw.Text, "testFunc")
}

func TestGetCodeBatch_MultipleSymbols(t *testing.T) {
	t.Parallel()
	db := openToolTestDB(t)

	// Insert multiple test symbols
	insertSymbol(t, db, "repo", "funcA", "function", "function funcA() { return 1 }")
	insertSymbol(t, db, "repo", "funcB", "function", "function funcB() { return 2 }")
	insertSymbol(t, db, "repo", "funcC", "function", "function funcC() { return 3 }")

	handler := getCodeBySymbolHandlerBatch(db, "repo", NewSessionTokenTracker())
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"symbol_name": []interface{}{"funcA", "funcB", "funcC"},
		"depth":       0,
	}

	result, err := handler(context.Background(), req)
	require.NoError(t, err)
	assert.False(t, result.IsError)

	// Verify batch response
	raw, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, raw.Text, `"total_symbols":3`)
	assert.Contains(t, raw.Text, `"success_count":3`)
	assert.Contains(t, raw.Text, `"error_count":0`)
	assert.Contains(t, raw.Text, "funcA")
	assert.Contains(t, raw.Text, "funcB")
	assert.Contains(t, raw.Text, "funcC")
}

func TestGetCodeBatch_PartialErrors(t *testing.T) {
	t.Parallel()
	db := openToolTestDB(t)

	// Insert only funcA, not funcB or funcC
	insertSymbol(t, db, "repo", "funcA", "function", "function funcA() { return 1 }")

	handler := getCodeBySymbolHandlerBatch(db, "repo", NewSessionTokenTracker())
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"symbol_name": []interface{}{"funcA", "nonexistent1", "nonexistent2"},
		"depth":       0,
	}

	result, err := handler(context.Background(), req)
	require.NoError(t, err)
	assert.False(t, result.IsError)

	// Verify partial success
	raw, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, raw.Text, `"total_symbols":3`)
	assert.Contains(t, raw.Text, `"success_count":1`)
	assert.Contains(t, raw.Text, `"error_count":2`)
	assert.Contains(t, raw.Text, "funcA")
	assert.Contains(t, raw.Text, "not found")
}

func TestGetCodeBatch_WithDependencies(t *testing.T) {
	t.Parallel()
	db := openToolTestDB(t)

	// Insert symbols with dependencies
	ctx := context.Background()
	_, err := db.ExecContext(ctx, `
		INSERT INTO symbols (repo_name, name, qualified_name, kind, file_path, content_hash, code_block, start_line, end_line, language)
		VALUES (?, ?, ?, ?, ?, 'hash1', ?, 1, 5, 'typescript')
	`, "repo", "caller", "caller", "function", "caller.ts", "function caller() { return callee() }")
	require.NoError(t, err)

	var callerID int64
	err = db.QueryRowContext(ctx, "SELECT id FROM symbols WHERE name = ?", "caller").Scan(&callerID)
	require.NoError(t, err)

	_, err = db.ExecContext(ctx, `
		INSERT INTO symbols (repo_name, name, qualified_name, kind, file_path, content_hash, code_block, start_line, end_line, language)
		VALUES (?, ?, ?, ?, ?, 'hash2', ?, 1, 5, 'typescript')
	`, "repo", "callee", "callee", "function", "callee.ts", "function callee() { return 42 }")
	require.NoError(t, err)

	var calleeID int64
	err = db.QueryRowContext(ctx, "SELECT id FROM symbols WHERE name = ?", "callee").Scan(&calleeID)
	require.NoError(t, err)

	// Insert dependency edge
	_, err = db.ExecContext(ctx, `
		INSERT INTO dependencies (caller_id, callee_id, kind)
		VALUES (?, ?, ?)
	`, callerID, calleeID, "call")
	require.NoError(t, err)

	handler := getCodeBySymbolHandlerBatch(db, "repo", NewSessionTokenTracker())
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"symbol_name": "caller",
		"depth":       1,
	}

	result, err := handler(context.Background(), req)
	require.NoError(t, err)
	assert.False(t, result.IsError)

	raw, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, raw.Text, "caller")
	assert.Contains(t, raw.Text, "callee")
	assert.Contains(t, raw.Text, `"dependency_count":1`)
}

func TestGetCodeBatch_BatchSizeLimit(t *testing.T) {
	t.Parallel()
	db := openToolTestDB(t)

	// Create 51 symbol names (exceeds max of 50)
	symbolNames := make([]interface{}, 51)
	for i := 0; i < 51; i++ {
		symbolNames[i] = "symbol" + string(rune('0'+i))
	}

	handler := getCodeBySymbolHandlerBatch(db, "repo", NewSessionTokenTracker())
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"symbol_name": symbolNames,
	}

	result, err := handler(context.Background(), req)
	require.NoError(t, err)
	assert.True(t, result.IsError, "should reject batch size > 50")

	raw, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, raw.Text, "batch size limit exceeded")
	assert.Contains(t, raw.Text, "max: 50")
}

func TestGetCodeBatch_DepthParameter(t *testing.T) {
	t.Parallel()
	db := openToolTestDB(t)

	insertSymbol(t, db, "repo", "testFunc", "function", "function testFunc() { return 42 }")

	tests := []struct {
		name          string
		depthInput    int
		expectedDepth int
	}{
		{"default depth", -1, 1},     // -1 means not provided, should default to 1
		{"depth 0", 0, 0},             // Explicit 0
		{"depth 1", 1, 1},             // Explicit 1
		{"depth 3", 3, 3},             // Max depth
		{"depth clamped", 5, 3},       // Should clamp to max 3
		{"negative clamped", -2, 0},   // Should clamp to min 0
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			handler := getCodeBySymbolHandlerBatch(db, "repo", NewSessionTokenTracker())
			req := mcp.CallToolRequest{}
			args := map[string]any{
				"symbol_name": "testFunc",
			}
			if tt.depthInput >= 0 {
				args["depth"] = tt.depthInput
			}
			req.Params.Arguments = args

			result, err := handler(context.Background(), req)
			require.NoError(t, err)
			assert.False(t, result.IsError)
		})
	}
}

func TestGetCodeBatch_MissingParameter(t *testing.T) {
	t.Parallel()
	db := openToolTestDB(t)

	handler := getCodeBySymbolHandlerBatch(db, "repo", NewSessionTokenTracker())
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{} // No symbol_name parameter

	result, err := handler(context.Background(), req)
	require.NoError(t, err)
	assert.True(t, result.IsError)

	raw, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, raw.Text, "symbol_name")
	assert.Contains(t, raw.Text, "required")
}

func TestGetCodeBatch_InvalidParameterType(t *testing.T) {
	t.Parallel()
	db := openToolTestDB(t)

	handler := getCodeBySymbolHandlerBatch(db, "repo", NewSessionTokenTracker())
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"symbol_name": 123, // Invalid type (int)
	}

	result, err := handler(context.Background(), req)
	require.NoError(t, err)
	assert.True(t, result.IsError)

	raw, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, raw.Text, "invalid symbol_name parameter")
}

func TestGetCodeBatch_TokenSavings(t *testing.T) {
	t.Parallel()
	db := openToolTestDB(t)

	// Insert symbols and files for token savings calculation
	ctx := context.Background()
	_, err := db.ExecContext(ctx, `
		INSERT INTO files (repo_name, path, size_bytes, content_hash, last_indexed)
		VALUES (?, ?, ?, ?, datetime('now'))
	`, "repo", "test.ts", 5000, "hash1")
	require.NoError(t, err)

	insertSymbol(t, db, "repo", "testFunc", "function", "function testFunc() { return 42 }")

	handler := getCodeBySymbolHandlerBatch(db, "repo", NewSessionTokenTracker())
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"symbol_name": "testFunc",
		"depth":       0,
	}

	result, err := handler(context.Background(), req)
	require.NoError(t, err)
	assert.False(t, result.IsError)

	// Verify metadata includes token savings
	raw, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, raw.Text, "tokens_saved_est")
	assert.Contains(t, raw.Text, "cost_avoided_est")
	assert.Contains(t, raw.Text, "session_tokens_saved")
}
