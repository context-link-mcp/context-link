package tools

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBlastRadius_SingleDepth(t *testing.T) {
	t.Parallel()
	db := openToolTestDB(t)

	// Setup: root <- caller (single depth)
	rootID := insertSymbol(t, db, "repo", "root", "function", "func root() {}")
	callerID := insertSymbol(t, db, "repo", "caller", "function", "func caller() {}")
	insertDependency(t, db, callerID, rootID, "call")

	handler := blastRadiusHandler(db, "repo", NewSessionTokenTracker())
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"symbol_name": "root",
		"depth":       1,
	}

	result, err := handler(context.Background(), req)
	require.NoError(t, err)
	assert.False(t, result.IsError)

	var resp struct {
		Root struct {
			Name string `json:"name"`
		} `json:"root"`
		TotalAffected int `json:"total_affected"`
	}
	raw, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	require.NoError(t, json.Unmarshal([]byte(raw.Text), &resp))

	assert.Equal(t, "root", resp.Root.Name)
	assert.Equal(t, 1, resp.TotalAffected, "should find 1 caller at depth 1")
}

func TestBlastRadius_DoubleDepth(t *testing.T) {
	t.Parallel()
	db := openToolTestDB(t)

	// Setup: root <- level1 <- level2 (depth 2 chain)
	rootID := insertSymbol(t, db, "repo", "root", "function", "func root() {}")
	level1ID := insertSymbol(t, db, "repo", "level1", "function", "func level1() {}")
	level2ID := insertSymbol(t, db, "repo", "level2", "function", "func level2() {}")
	insertDependency(t, db, level1ID, rootID, "call")
	insertDependency(t, db, level2ID, level1ID, "call")

	handler := blastRadiusHandler(db, "repo", NewSessionTokenTracker())
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"symbol_name": "root",
		"depth":       2,
	}

	result, err := handler(context.Background(), req)
	require.NoError(t, err)
	assert.False(t, result.IsError)

	var resp struct {
		TotalAffected int            `json:"total_affected"`
		ByDepth       map[string]int `json:"by_depth"`
	}
	raw, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	require.NoError(t, json.Unmarshal([]byte(raw.Text), &resp))

	assert.Equal(t, 2, resp.TotalAffected, "should find 2 callers across 2 depths")
	assert.Equal(t, 1, resp.ByDepth["1"], "should have 1 symbol at depth 1")
	assert.Equal(t, 1, resp.ByDepth["2"], "should have 1 symbol at depth 2")
}

func TestBlastRadius_MaxDepth(t *testing.T) {
	t.Parallel()
	db := openToolTestDB(t)

	// Setup: root <- l1 <- l2 <- l3 <- l4 (depth 4 chain, but max is 3)
	rootID := insertSymbol(t, db, "repo", "root", "function", "func root() {}")
	l1ID := insertSymbol(t, db, "repo", "l1", "function", "func l1() {}")
	l2ID := insertSymbol(t, db, "repo", "l2", "function", "func l2() {}")
	l3ID := insertSymbol(t, db, "repo", "l3", "function", "func l3() {}")
	l4ID := insertSymbol(t, db, "repo", "l4", "function", "func l4() {}")
	insertDependency(t, db, l1ID, rootID, "call")
	insertDependency(t, db, l2ID, l1ID, "call")
	insertDependency(t, db, l3ID, l2ID, "call")
	insertDependency(t, db, l4ID, l3ID, "call")

	handler := blastRadiusHandler(db, "repo", NewSessionTokenTracker())
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"symbol_name": "root",
		"depth":       3,
	}

	result, err := handler(context.Background(), req)
	require.NoError(t, err)
	assert.False(t, result.IsError)

	var resp struct {
		TotalAffected int            `json:"total_affected"`
		ByDepth       map[string]int `json:"by_depth"`
	}
	raw, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	require.NoError(t, json.Unmarshal([]byte(raw.Text), &resp))

	assert.Equal(t, 3, resp.TotalAffected, "should stop at depth 3")
	assert.Equal(t, 1, resp.ByDepth["1"])
	assert.Equal(t, 1, resp.ByDepth["2"])
	assert.Equal(t, 1, resp.ByDepth["3"])
	assert.Equal(t, 0, resp.ByDepth["4"], "should not reach depth 4")
}

func TestBlastRadius_NoCaller(t *testing.T) {
	t.Parallel()
	db := openToolTestDB(t)

	// Insert symbol with no callers
	insertSymbol(t, db, "repo", "orphan", "function", "func orphan() {}")

	handler := blastRadiusHandler(db, "repo", NewSessionTokenTracker())
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"symbol_name": "orphan",
		"depth":       2,
	}

	result, err := handler(context.Background(), req)
	require.NoError(t, err)
	assert.False(t, result.IsError)

	var resp struct {
		TotalAffected int `json:"total_affected"`
		FilesAffected int `json:"files_affected"`
	}
	raw, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	require.NoError(t, json.Unmarshal([]byte(raw.Text), &resp))

	assert.Equal(t, 0, resp.TotalAffected, "orphan symbol has no blast radius")
	assert.Equal(t, 0, resp.FilesAffected)
}

func TestBlastRadius_CircularDeps(t *testing.T) {
	t.Parallel()
	db := openToolTestDB(t)

	// Setup: root <- a <- b <- a (circular dependency)
	rootID := insertSymbol(t, db, "repo", "root", "function", "func root() {}")
	aID := insertSymbol(t, db, "repo", "a", "function", "func a() {}")
	bID := insertSymbol(t, db, "repo", "b", "function", "func b() {}")
	insertDependency(t, db, aID, rootID, "call")
	insertDependency(t, db, bID, aID, "call")
	insertDependency(t, db, aID, bID, "call") // circular: a <-> b

	handler := blastRadiusHandler(db, "repo", NewSessionTokenTracker())
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"symbol_name": "root",
		"depth":       3,
	}

	result, err := handler(context.Background(), req)
	require.NoError(t, err)
	assert.False(t, result.IsError)

	var resp struct {
		TotalAffected int `json:"total_affected"`
	}
	raw, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	require.NoError(t, json.Unmarshal([]byte(raw.Text), &resp))

	// BFS should handle cycles gracefully (visit each symbol once)
	assert.LessOrEqual(t, resp.TotalAffected, 2, "should not duplicate symbols in cycle")
}

func TestBlastRadius_FileGrouping(t *testing.T) {
	t.Parallel()
	db := openToolTestDB(t)

	// Setup: root <- caller1, caller2 (same file), caller3 (different file)
	ctx := context.Background()
	_, err := db.ExecContext(ctx, `
		INSERT INTO symbols (repo_name, name, qualified_name, kind, file_path, content_hash, code_block, start_line, end_line, language)
		VALUES (?, ?, ?, ?, ?, 'hash1', ?, 1, 5, 'typescript')
	`, "repo", "root", "root", "function", "src/root.ts", "func root() {}")
	require.NoError(t, err)
	var rootID int64
	err = db.QueryRowContext(ctx, "SELECT id FROM symbols WHERE name = ?", "root").Scan(&rootID)
	require.NoError(t, err)

	_, err = db.ExecContext(ctx, `
		INSERT INTO symbols (repo_name, name, qualified_name, kind, file_path, content_hash, code_block, start_line, end_line, language)
		VALUES (?, ?, ?, ?, ?, 'hash2', ?, 1, 5, 'typescript')
	`, "repo", "caller1", "caller1", "function", "src/file1.ts", "func caller1() {}")
	require.NoError(t, err)
	var caller1ID int64
	err = db.QueryRowContext(ctx, "SELECT id FROM symbols WHERE name = ?", "caller1").Scan(&caller1ID)
	require.NoError(t, err)

	_, err = db.ExecContext(ctx, `
		INSERT INTO symbols (repo_name, name, qualified_name, kind, file_path, content_hash, code_block, start_line, end_line, language)
		VALUES (?, ?, ?, ?, ?, 'hash3', ?, 1, 5, 'typescript')
	`, "repo", "caller2", "caller2", "function", "src/file1.ts", "func caller2() {}")
	require.NoError(t, err)
	var caller2ID int64
	err = db.QueryRowContext(ctx, "SELECT id FROM symbols WHERE name = ?", "caller2").Scan(&caller2ID)
	require.NoError(t, err)

	_, err = db.ExecContext(ctx, `
		INSERT INTO symbols (repo_name, name, qualified_name, kind, file_path, content_hash, code_block, start_line, end_line, language)
		VALUES (?, ?, ?, ?, ?, 'hash4', ?, 1, 5, 'typescript')
	`, "repo", "caller3", "caller3", "function", "src/file2.ts", "func caller3() {}")
	require.NoError(t, err)
	var caller3ID int64
	err = db.QueryRowContext(ctx, "SELECT id FROM symbols WHERE name = ?", "caller3").Scan(&caller3ID)
	require.NoError(t, err)

	insertDependency(t, db, caller1ID, rootID, "call")
	insertDependency(t, db, caller2ID, rootID, "call")
	insertDependency(t, db, caller3ID, rootID, "call")

	handler := blastRadiusHandler(db, "repo", NewSessionTokenTracker())
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"symbol_name": "root",
		"depth":       1,
	}

	result, err := handler(context.Background(), req)
	require.NoError(t, err)
	assert.False(t, result.IsError)

	var resp struct {
		AffectedFiles map[string][]any `json:"affected_files"`
		FilesAffected int              `json:"files_affected"`
		TotalAffected int              `json:"total_affected"`
	}
	raw, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	require.NoError(t, json.Unmarshal([]byte(raw.Text), &resp))

	assert.Equal(t, 3, resp.TotalAffected, "should find 3 callers")
	assert.Equal(t, 2, resp.FilesAffected, "should group into 2 files")
	assert.Len(t, resp.AffectedFiles["src/file1.ts"], 2, "file1 should have 2 symbols")
	assert.Len(t, resp.AffectedFiles["src/file2.ts"], 1, "file2 should have 1 symbol")
}

func TestBlastRadius_DepthCount(t *testing.T) {
	t.Parallel()
	db := openToolTestDB(t)

	// Setup: root <- (a, b) <- (c, d) (2 at each depth)
	rootID := insertSymbol(t, db, "repo", "root", "function", "func root() {}")
	aID := insertSymbol(t, db, "repo", "a", "function", "func a() {}")
	bID := insertSymbol(t, db, "repo", "b", "function", "func b() {}")
	cID := insertSymbol(t, db, "repo", "c", "function", "func c() {}")
	dID := insertSymbol(t, db, "repo", "d", "function", "func d() {}")

	insertDependency(t, db, aID, rootID, "call")
	insertDependency(t, db, bID, rootID, "call")
	insertDependency(t, db, cID, aID, "call")
	insertDependency(t, db, dID, bID, "call")

	handler := blastRadiusHandler(db, "repo", NewSessionTokenTracker())
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"symbol_name": "root",
		"depth":       2,
	}

	result, err := handler(context.Background(), req)
	require.NoError(t, err)
	assert.False(t, result.IsError)

	var resp struct {
		ByDepth map[string]int `json:"by_depth"`
	}
	raw, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	require.NoError(t, json.Unmarshal([]byte(raw.Text), &resp))

	assert.Equal(t, 2, resp.ByDepth["1"], "2 symbols at depth 1")
	assert.Equal(t, 2, resp.ByDepth["2"], "2 symbols at depth 2")
}

func TestBlastRadius_SymbolNotFound(t *testing.T) {
	t.Parallel()
	db := openToolTestDB(t)

	handler := blastRadiusHandler(db, "repo", NewSessionTokenTracker())
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"symbol_name": "nonexistent",
		"depth":       2,
	}

	result, err := handler(context.Background(), req)
	require.NoError(t, err)
	assert.True(t, result.IsError, "should return error for missing symbol")

	raw, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, raw.Text, "not found", "error should mention symbol not found")
	assert.Contains(t, raw.Text, "semantic_search_symbols", "should suggest search")
}

func TestBlastRadius_DepthClamp(t *testing.T) {
	t.Parallel()
	db := openToolTestDB(t)

	rootID := insertSymbol(t, db, "repo", "root", "function", "func root() {}")
	callerID := insertSymbol(t, db, "repo", "caller", "function", "func caller() {}")
	insertDependency(t, db, callerID, rootID, "call")

	handler := blastRadiusHandler(db, "repo", NewSessionTokenTracker())

	tests := []struct {
		name          string
		inputDepth    int
		expectedDepth int
	}{
		{"negative depth", -1, 1},
		{"zero depth", 0, 1},
		{"depth 5", 5, 3},
		{"depth 10", 10, 3},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := mcp.CallToolRequest{}
			req.Params.Arguments = map[string]any{
				"symbol_name": "root",
				"depth":       tt.inputDepth,
			}

			result, err := handler(context.Background(), req)
			require.NoError(t, err)
			assert.False(t, result.IsError, "should clamp depth instead of erroring")
		})
	}
}

func TestBlastRadius_TokenSavings(t *testing.T) {
	t.Parallel()
	db := openToolTestDB(t)

	// Insert symbol and file for token savings
	rootID := insertSymbol(t, db, "repo", "root", "function", "func root() {}")
	callerID := insertSymbol(t, db, "repo", "caller", "function", "func caller() {}")
	insertDependency(t, db, callerID, rootID, "call")
	insertFile(t, db, "repo", "test.ts", 10000)

	handler := blastRadiusHandler(db, "repo", NewSessionTokenTracker())
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"symbol_name": "root",
		"depth":       1,
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
