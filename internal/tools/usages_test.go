package tools

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUsages_SingleCaller(t *testing.T) {
	t.Parallel()
	db := openToolTestDB(t)

	// Setup: caller -> target
	targetID := insertSymbol(t, db, "repo", "target", "function", "func target() {}")
	callerID := insertSymbol(t, db, "repo", "caller", "function", "func caller() {}")
	insertDependency(t, db, callerID, targetID, "call")

	handler := symbolUsagesHandler(db, "repo", NewSessionTokenTracker())
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"symbol_name": "target",
	}

	result, err := handler(context.Background(), req)
	require.NoError(t, err)
	assert.False(t, result.IsError)

	var resp struct {
		UsageCount int `json:"usage_count"`
		Usages     []struct {
			CallerName string `json:"caller_name"`
			DepKind    string `json:"dep_kind"`
		} `json:"usages"`
	}
	raw, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	require.NoError(t, json.Unmarshal([]byte(raw.Text), &resp))

	assert.Equal(t, 1, resp.UsageCount)
	assert.Len(t, resp.Usages, 1)
	assert.Equal(t, "caller", resp.Usages[0].CallerName)
	assert.Equal(t, "call", resp.Usages[0].DepKind)
}

func TestUsages_MultipleCallers(t *testing.T) {
	t.Parallel()
	db := openToolTestDB(t)

	// Setup: caller1, caller2, caller3 -> target
	targetID := insertSymbol(t, db, "repo", "target", "function", "func target() {}")
	caller1ID := insertSymbol(t, db, "repo", "caller1", "function", "func caller1() {}")
	caller2ID := insertSymbol(t, db, "repo", "caller2", "function", "func caller2() {}")
	caller3ID := insertSymbol(t, db, "repo", "caller3", "function", "func caller3() {}")
	insertDependency(t, db, caller1ID, targetID, "call")
	insertDependency(t, db, caller2ID, targetID, "call")
	insertDependency(t, db, caller3ID, targetID, "call")

	handler := symbolUsagesHandler(db, "repo", NewSessionTokenTracker())
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"symbol_name": "target",
	}

	result, err := handler(context.Background(), req)
	require.NoError(t, err)
	assert.False(t, result.IsError)

	var resp struct {
		UsageCount int `json:"usage_count"`
		Usages     []struct {
			CallerName string `json:"caller_name"`
		} `json:"usages"`
	}
	raw, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	require.NoError(t, json.Unmarshal([]byte(raw.Text), &resp))

	assert.Equal(t, 3, resp.UsageCount)
	assert.Len(t, resp.Usages, 3)
	names := []string{resp.Usages[0].CallerName, resp.Usages[1].CallerName, resp.Usages[2].CallerName}
	assert.Contains(t, names, "caller1")
	assert.Contains(t, names, "caller2")
	assert.Contains(t, names, "caller3")
}

func TestUsages_NoUsages(t *testing.T) {
	t.Parallel()
	db := openToolTestDB(t)

	// Insert target with no callers
	insertSymbol(t, db, "repo", "orphan", "function", "func orphan() {}")

	handler := symbolUsagesHandler(db, "repo", NewSessionTokenTracker())
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"symbol_name": "orphan",
	}

	result, err := handler(context.Background(), req)
	require.NoError(t, err)
	assert.False(t, result.IsError)

	var resp struct {
		UsageCount int   `json:"usage_count"`
		Usages     []any `json:"usages"`
	}
	raw, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	require.NoError(t, json.Unmarshal([]byte(raw.Text), &resp))

	assert.Equal(t, 0, resp.UsageCount)
	assert.Empty(t, resp.Usages)
}

func TestUsages_SameFileCaller(t *testing.T) {
	t.Parallel()
	db := openToolTestDB(t)

	ctx := context.Background()

	// Insert target
	_, err := db.ExecContext(ctx, `
		INSERT INTO symbols (repo_name, name, qualified_name, kind, file_path, content_hash, code_block, start_line, end_line, language)
		VALUES (?, ?, ?, ?, ?, 'hash1', ?, 1, 5, 'typescript')
	`, "repo", "target", "target", "function", "src/file.ts", "func target() {}")
	require.NoError(t, err)
	var targetID int64
	err = db.QueryRowContext(ctx, "SELECT id FROM symbols WHERE name = ?", "target").Scan(&targetID)
	require.NoError(t, err)

	// Insert two callers in the same file
	_, err = db.ExecContext(ctx, `
		INSERT INTO symbols (repo_name, name, qualified_name, kind, file_path, content_hash, code_block, start_line, end_line, language)
		VALUES (?, ?, ?, ?, ?, 'hash2', ?, 1, 5, 'typescript')
	`, "repo", "caller1", "caller1", "function", "src/file.ts", "func caller1() {}")
	require.NoError(t, err)
	var caller1ID int64
	err = db.QueryRowContext(ctx, "SELECT id FROM symbols WHERE name = ?", "caller1").Scan(&caller1ID)
	require.NoError(t, err)

	_, err = db.ExecContext(ctx, `
		INSERT INTO symbols (repo_name, name, qualified_name, kind, file_path, content_hash, code_block, start_line, end_line, language)
		VALUES (?, ?, ?, ?, ?, 'hash3', ?, 10, 15, 'typescript')
	`, "repo", "caller2", "caller2", "function", "src/file.ts", "func caller2() {}")
	require.NoError(t, err)
	var caller2ID int64
	err = db.QueryRowContext(ctx, "SELECT id FROM symbols WHERE name = ?", "caller2").Scan(&caller2ID)
	require.NoError(t, err)

	insertDependency(t, db, caller1ID, targetID, "call")
	insertDependency(t, db, caller2ID, targetID, "call")

	handler := symbolUsagesHandler(db, "repo", NewSessionTokenTracker())
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"symbol_name": "target",
	}

	result, err := handler(context.Background(), req)
	require.NoError(t, err)
	assert.False(t, result.IsError)

	var resp struct {
		UsageCount int `json:"usage_count"`
		Usages     []struct {
			CallerName string `json:"caller_name"`
			FilePath   string `json:"file_path"`
			StartLine  int    `json:"start_line"`
		} `json:"usages"`
	}
	raw, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	require.NoError(t, json.Unmarshal([]byte(raw.Text), &resp))

	assert.Equal(t, 2, resp.UsageCount)
	// Both callers from same file
	assert.Equal(t, "src/file.ts", resp.Usages[0].FilePath)
	assert.Equal(t, "src/file.ts", resp.Usages[1].FilePath)
	// But different line numbers
	lines := []int{resp.Usages[0].StartLine, resp.Usages[1].StartLine}
	assert.Contains(t, lines, 1)
	assert.Contains(t, lines, 10)
}

func TestUsages_MissingCaller(t *testing.T) {
	t.Parallel()
	db := openToolTestDB(t)

	// Insert target and dependency, but without the caller symbol (orphan dep)
	targetID := insertSymbol(t, db, "repo", "target", "function", "func target() {}")

	// Insert dependency with non-existent caller_id
	_, err := db.ExecContext(context.Background(), `
		INSERT INTO dependencies (caller_id, callee_id, kind)
		VALUES (?, ?, ?)
	`, 99999, targetID, "call")
	require.NoError(t, err)

	handler := symbolUsagesHandler(db, "repo", NewSessionTokenTracker())
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"symbol_name": "target",
	}

	result, err := handler(context.Background(), req)
	require.NoError(t, err)
	assert.False(t, result.IsError)

	var resp struct {
		UsageCount int   `json:"usage_count"`
		Usages     []any `json:"usages"`
	}
	raw, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	require.NoError(t, json.Unmarshal([]byte(raw.Text), &resp))

	// Should gracefully skip missing caller
	assert.Equal(t, 0, resp.UsageCount)
	assert.Empty(t, resp.Usages)
}

func TestUsages_DepKind(t *testing.T) {
	t.Parallel()
	db := openToolTestDB(t)

	// Setup with different dependency kinds
	targetID := insertSymbol(t, db, "repo", "target", "class", "class Target {}")
	caller1ID := insertSymbol(t, db, "repo", "caller1", "function", "func caller1() {}")
	caller2ID := insertSymbol(t, db, "repo", "caller2", "class", "class Caller2 {}")
	insertDependency(t, db, caller1ID, targetID, "call")
	insertDependency(t, db, caller2ID, targetID, "extends")

	handler := symbolUsagesHandler(db, "repo", NewSessionTokenTracker())
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"symbol_name": "target",
	}

	result, err := handler(context.Background(), req)
	require.NoError(t, err)
	assert.False(t, result.IsError)

	var resp struct {
		UsageCount int `json:"usage_count"`
		Usages     []struct {
			CallerName string `json:"caller_name"`
			DepKind    string `json:"dep_kind"`
		} `json:"usages"`
	}
	raw, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	require.NoError(t, json.Unmarshal([]byte(raw.Text), &resp))

	assert.Equal(t, 2, resp.UsageCount)

	// Verify different dep_kind values are preserved
	depKinds := map[string]string{}
	for _, usage := range resp.Usages {
		depKinds[usage.CallerName] = usage.DepKind
	}
	assert.Equal(t, "call", depKinds["caller1"])
	assert.Equal(t, "extends", depKinds["caller2"])
}

func TestUsages_SymbolNotFound(t *testing.T) {
	t.Parallel()
	db := openToolTestDB(t)

	handler := symbolUsagesHandler(db, "repo", NewSessionTokenTracker())
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"symbol_name": "nonexistent",
	}

	result, err := handler(context.Background(), req)
	require.NoError(t, err)
	assert.True(t, result.IsError)

	raw, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, raw.Text, "not found")
	assert.Contains(t, raw.Text, "semantic_search_symbols")
}

func TestUsages_TokenSavings(t *testing.T) {
	t.Parallel()
	db := openToolTestDB(t)

	// Setup with file for token savings
	targetID := insertSymbol(t, db, "repo", "target", "function", "func target() {}")
	callerID := insertSymbol(t, db, "repo", "caller", "function", "func caller() {}")
	insertDependency(t, db, callerID, targetID, "call")
	insertFile(t, db, "repo", "test.ts", 8000)

	handler := symbolUsagesHandler(db, "repo", NewSessionTokenTracker())
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"symbol_name": "target",
	}

	result, err := handler(context.Background(), req)
	require.NoError(t, err)
	assert.False(t, result.IsError)

	var resp struct {
		Metadata struct {
			TimingMs           int64  `json:"timing_ms"`
			TokensSavedEst     int64  `json:"tokens_saved_est"`
			CostAvoidedEst     string `json:"cost_avoided_est"`
			SessionTokensSaved int64  `json:"session_tokens_saved"`
		} `json:"metadata"`
	}
	raw, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	require.NoError(t, json.Unmarshal([]byte(raw.Text), &resp))

	assert.GreaterOrEqual(t, resp.Metadata.TimingMs, int64(0))
	assert.GreaterOrEqual(t, resp.Metadata.TokensSavedEst, int64(0))
	assert.NotEmpty(t, resp.Metadata.CostAvoidedEst)
	assert.GreaterOrEqual(t, resp.Metadata.SessionTokensSaved, int64(0))
}
