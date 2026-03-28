package tools

import (
	"context"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSearchCodePatternsHandler_ValidPattern(t *testing.T) {
	t.Parallel()
	db := openToolTestDB(t)

	// Insert symbols with error sentinels
	insertSymbol(t, db, "repo", "ErrNotFound", "variable", `var ErrNotFound = errors.New("not found")`)
	insertSymbol(t, db, "repo", "ErrInvalid", "variable", `var ErrInvalid = errors.New("invalid input")`)
	insertSymbol(t, db, "repo", "myFunc", "function", "func myFunc() { return nil }")

	handler := searchCodePatternsHandler(db, "repo", NewSessionTokenTracker())
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{"pattern": `errors\.New\(`}

	result, err := handler(context.Background(), req)
	require.NoError(t, err)
	assert.False(t, result.IsError)

	// Verify the result contains 2 matches (not the function)
	raw, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, raw.Text, `"result_count":2`, "should find 2 error sentinels")
}

func TestSearchCodePatternsHandler_InvalidRegex(t *testing.T) {
	t.Parallel()
	db := openToolTestDB(t)

	handler := searchCodePatternsHandler(db, "repo", NewSessionTokenTracker())
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{"pattern": "[invalid(regex"}

	result, err := handler(context.Background(), req)
	require.NoError(t, err)
	assert.True(t, result.IsError, "should return error for invalid regex")
	raw, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, raw.Text, "invalid regex pattern")
}

func TestSearchCodePatternsHandler_KindFilter(t *testing.T) {
	t.Parallel()
	db := openToolTestDB(t)

	// Insert different kinds of symbols with retry pattern
	insertSymbol(t, db, "repo", "retryFunc", "function", "function retry() { return 3 }")
	insertSymbol(t, db, "repo", "retryVar", "variable", "var retryCount = 3")

	handler := searchCodePatternsHandler(db, "repo", NewSessionTokenTracker())
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"pattern": "retry",
		"kind":    "function",
	}

	result, err := handler(context.Background(), req)
	require.NoError(t, err)
	assert.False(t, result.IsError)

	// Verify only the function is returned, not the variable
	raw, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, raw.Text, `"result_count":1`)
	assert.Contains(t, raw.Text, "retryFunc")
	assert.NotContains(t, raw.Text, "retryVar")
}

func TestSearchCodePatternsHandler_FilePathPrefix(t *testing.T) {
	t.Parallel()
	db := openToolTestDB(t)

	// Insert symbols with different file paths
	ctx := context.Background()
	_, err := db.ExecContext(ctx, `
		INSERT INTO symbols (repo_name, name, qualified_name, kind, file_path, content_hash, code_block, start_line, end_line, language)
		VALUES (?, ?, ?, ?, ?, 'hash1', ?, 1, 5, 'typescript')
	`, "repo", "func1", "func1", "function", "internal/store/symbols.go", "function func1() { retry }")
	require.NoError(t, err)

	_, err = db.ExecContext(ctx, `
		INSERT INTO symbols (repo_name, name, qualified_name, kind, file_path, content_hash, code_block, start_line, end_line, language)
		VALUES (?, ?, ?, ?, ?, 'hash2', ?, 1, 5, 'typescript')
	`, "repo", "func2", "func2", "function", "internal/tools/handler.go", "function func2() { retry }")
	require.NoError(t, err)

	handler := searchCodePatternsHandler(db, "repo", NewSessionTokenTracker())
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"pattern":          "retry",
		"file_path_prefix": "internal/store/",
	}

	result, err := handler(context.Background(), req)
	require.NoError(t, err)
	assert.False(t, result.IsError)

	// Verify only the symbol in internal/store/ is returned
	raw, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, raw.Text, `"result_count":1`)
	assert.Contains(t, raw.Text, "internal/store/symbols.go")
	assert.NotContains(t, raw.Text, "internal/tools/handler.go")
}

func TestSearchCodePatternsHandler_ResultLimit(t *testing.T) {
	t.Parallel()
	db := openToolTestDB(t)

	// Insert many symbols matching the pattern
	for i := 0; i < 10; i++ {
		insertSymbol(t, db, "repo", "func"+string(rune('0'+i)), "function", "function test() { error }")
	}

	handler := searchCodePatternsHandler(db, "repo", NewSessionTokenTracker())
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"pattern": "error",
		"limit":   3,
	}

	result, err := handler(context.Background(), req)
	require.NoError(t, err)
	assert.False(t, result.IsError)

	// Verify only 3 results are returned
	raw, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, raw.Text, `"result_count":3`)
}

func TestSearchCodePatternsHandler_NoMatches(t *testing.T) {
	t.Parallel()
	db := openToolTestDB(t)

	insertSymbol(t, db, "repo", "myFunc", "function", "function myFunc() { return null }")

	handler := searchCodePatternsHandler(db, "repo", NewSessionTokenTracker())
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{"pattern": "nonexistent_pattern_xyz"}

	result, err := handler(context.Background(), req)
	require.NoError(t, err)
	assert.False(t, result.IsError)

	// Verify no results found
	raw, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, raw.Text, `"result_count":0`)
}

func TestSearchCodePatternsHandler_MissingPattern(t *testing.T) {
	t.Parallel()
	db := openToolTestDB(t)

	handler := searchCodePatternsHandler(db, "repo", NewSessionTokenTracker())
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{} // No pattern parameter

	result, err := handler(context.Background(), req)
	require.NoError(t, err)
	assert.True(t, result.IsError, "should return error for missing pattern")
	raw, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, raw.Text, "pattern")
}

func TestSearchCodePatternsHandler_TokenSavings(t *testing.T) {
	t.Parallel()
	db := openToolTestDB(t)

	// Insert a file entry for token savings calculation
	ctx := context.Background()
	_, err := db.ExecContext(ctx, `
		INSERT INTO files (repo_name, path, size_bytes, content_hash, last_indexed)
		VALUES (?, ?, ?, ?, datetime('now'))
	`, "repo", "test.ts", 5000, "hash1")
	require.NoError(t, err)

	insertSymbol(t, db, "repo", "myFunc", "function", "function test() { error handling }")

	handler := searchCodePatternsHandler(db, "repo", NewSessionTokenTracker())
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{"pattern": "error"}

	result, err := handler(context.Background(), req)
	require.NoError(t, err)
	assert.False(t, result.IsError)

	// Verify metadata includes token savings
	raw, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, raw.Text, "tokens_saved_est")
	assert.Contains(t, raw.Text, "cost_avoided_est")
}

func TestExtractLiteralSubstring(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		pattern  string
		expected string
	}{
		{
			name:     "simple literal",
			pattern:  "errorHandler",
			expected: "errorHandler",
		},
		{
			name:     "pattern with anchors",
			pattern:  "^function.*$",
			expected: "function",
		},
		{
			name:     "pattern with escapes",
			pattern:  `errors\.New\(`,
			expected: "errors",
		},
		{
			name:     "complex regex",
			pattern:  `retry.*(?:backoff|timeout)`,
			expected: "retry",
		},
		{
			name:     "no literal sequence",
			pattern:  `.*`,
			expected: "",
		},
		{
			name:     "short literal (< 3 chars)",
			pattern:  `ab.*`,
			expected: "",
		},
		{
			name:     "word boundary in pattern",
			pattern:  `\berror\b`,
			expected: "error",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := extractLiteralSubstring(tt.pattern)
			assert.Equal(t, tt.expected, result)
		})
	}
}
