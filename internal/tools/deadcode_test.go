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

	"github.com/context-link-mcp/context-link/internal/store"
)

// insertDependency inserts a dependency edge into the DB.
func insertDependency(t *testing.T, db *store.DB, callerID, calleeID int64, kind string) {
	t.Helper()
	_, err := db.ExecContext(context.Background(), `
		INSERT INTO dependencies (caller_id, callee_id, kind)
		VALUES (?, ?, ?)
	`, callerID, calleeID, kind)
	require.NoError(t, err)
}

// insertFile inserts a file record for token savings calculation.
func insertFile(t *testing.T, db *store.DB, repo, path string, sizeBytes int) {
	t.Helper()
	_, err := db.ExecContext(context.Background(), `
		INSERT INTO files (repo_name, path, size_bytes, content_hash, last_indexed)
		VALUES (?, ?, ?, 'hash1', datetime('now'))
	`, repo, path, sizeBytes)
	require.NoError(t, err)
}

func TestFindDeadCode_NoResults(t *testing.T) {
	t.Parallel()
	db := openToolTestDB(t)

	// Setup: create a full call chain where all symbols have callers
	// root -> caller -> callee
	rootID := insertSymbol(t, db, "repo", "root", "function", "func root() {}")
	callerID := insertSymbol(t, db, "repo", "caller", "function", "func caller() {}")
	calleeID := insertSymbol(t, db, "repo", "callee", "function", "func callee() {}")
	insertDependency(t, db, rootID, callerID, "call")
	insertDependency(t, db, callerID, calleeID, "call")

	// Mark root as an entry point by naming it main
	_, err := db.ExecContext(context.Background(), `
		UPDATE symbols SET name = 'main', qualified_name = 'main' WHERE id = ?
	`, rootID)
	require.NoError(t, err)

	handler := deadCodeHandler(db, "repo", "", NewSessionTokenTracker())
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{}

	result, err := handler(context.Background(), req)
	require.NoError(t, err)
	assert.False(t, result.IsError)

	var resp struct {
		DeadSymbols []any `json:"dead_symbols"`
		Count       int   `json:"count"`
	}
	raw, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	require.NoError(t, json.Unmarshal([]byte(raw.Text), &resp))

	// All non-entry-point symbols have callers, so no dead code
	assert.Empty(t, resp.DeadSymbols, "symbols with callers should not be dead")
	assert.Equal(t, 0, resp.Count)
}

func TestFindDeadCode_ExcludeExported(t *testing.T) {
	t.Parallel()
	db := openToolTestDB(t)

	// Insert exported (uppercase) and unexported (lowercase) dead symbols
	insertSymbol(t, db, "repo", "ExportedFunc", "function", "func ExportedFunc() {}")
	insertSymbol(t, db, "repo", "unexportedFunc", "function", "func unexportedFunc() {}")

	handler := deadCodeHandler(db, "repo", "", NewSessionTokenTracker())
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"exclude_exported": true,
	}

	result, err := handler(context.Background(), req)
	require.NoError(t, err)
	assert.False(t, result.IsError)

	var resp struct {
		DeadSymbols []struct {
			Name string `json:"name"`
		} `json:"dead_symbols"`
	}
	raw, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	require.NoError(t, json.Unmarshal([]byte(raw.Text), &resp))

	assert.Len(t, resp.DeadSymbols, 1, "should only return unexported symbol")
	assert.Equal(t, "unexportedFunc", resp.DeadSymbols[0].Name)
}

func TestFindDeadCode_IncludeExported(t *testing.T) {
	t.Parallel()
	db := openToolTestDB(t)

	// Insert exported and unexported dead symbols
	insertSymbol(t, db, "repo", "ExportedFunc", "function", "func ExportedFunc() {}")
	insertSymbol(t, db, "repo", "unexportedFunc", "function", "func unexportedFunc() {}")

	handler := deadCodeHandler(db, "repo", "", NewSessionTokenTracker())
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"exclude_exported": false,
	}

	result, err := handler(context.Background(), req)
	require.NoError(t, err)
	assert.False(t, result.IsError)

	var resp struct {
		DeadSymbols []struct {
			Name string `json:"name"`
		} `json:"dead_symbols"`
	}
	raw, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	require.NoError(t, json.Unmarshal([]byte(raw.Text), &resp))

	assert.Len(t, resp.DeadSymbols, 2, "should return both exported and unexported")
	names := []string{resp.DeadSymbols[0].Name, resp.DeadSymbols[1].Name}
	assert.Contains(t, names, "ExportedFunc")
	assert.Contains(t, names, "unexportedFunc")
}

func TestFindDeadCode_KindFilter(t *testing.T) {
	t.Parallel()
	db := openToolTestDB(t)

	// Insert symbols of different kinds
	insertSymbol(t, db, "repo", "deadFunc", "function", "func deadFunc() {}")
	insertSymbol(t, db, "repo", "DeadClass", "class", "class DeadClass {}")
	insertSymbol(t, db, "repo", "deadMethod", "method", "method deadMethod() {}")

	handler := deadCodeHandler(db, "repo", "", NewSessionTokenTracker())
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"kind":             "function",
		"exclude_exported": false,
	}

	result, err := handler(context.Background(), req)
	require.NoError(t, err)
	assert.False(t, result.IsError)

	var resp struct {
		DeadSymbols []struct {
			Name string `json:"name"`
			Kind string `json:"kind"`
		} `json:"dead_symbols"`
	}
	raw, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	require.NoError(t, json.Unmarshal([]byte(raw.Text), &resp))

	assert.Len(t, resp.DeadSymbols, 1, "should only return functions")
	assert.Equal(t, "deadFunc", resp.DeadSymbols[0].Name)
	assert.Equal(t, "function", resp.DeadSymbols[0].Kind)
}

func TestFindDeadCode_FileFilter(t *testing.T) {
	t.Parallel()
	db := openToolTestDB(t)

	// Insert symbols in different files
	ctx := context.Background()
	_, err := db.ExecContext(ctx, `
		INSERT INTO symbols (repo_name, name, qualified_name, kind, file_path, content_hash, code_block, start_line, end_line, language)
		VALUES (?, ?, ?, ?, ?, 'hash1', ?, 1, 5, 'typescript')
	`, "repo", "func1", "func1", "function", "src/a.ts", "func func1() {}")
	require.NoError(t, err)

	_, err = db.ExecContext(ctx, `
		INSERT INTO symbols (repo_name, name, qualified_name, kind, file_path, content_hash, code_block, start_line, end_line, language)
		VALUES (?, ?, ?, ?, ?, 'hash2', ?, 1, 5, 'typescript')
	`, "repo", "func2", "func2", "function", "src/b.ts", "func func2() {}")
	require.NoError(t, err)

	// Create temporary project root with the file
	projectRoot := t.TempDir()
	srcDir := filepath.Join(projectRoot, "src")
	require.NoError(t, os.MkdirAll(srcDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(srcDir, "a.ts"), []byte(""), 0644))

	handler := deadCodeHandler(db, "repo", projectRoot, NewSessionTokenTracker())
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"file_path":        "src/a.ts",
		"exclude_exported": false,
	}

	result, err := handler(context.Background(), req)
	require.NoError(t, err)
	assert.False(t, result.IsError)

	var resp struct {
		DeadSymbols []struct {
			Name     string `json:"name"`
			FilePath string `json:"file_path"`
		} `json:"dead_symbols"`
	}
	raw, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	require.NoError(t, json.Unmarshal([]byte(raw.Text), &resp))

	assert.Len(t, resp.DeadSymbols, 1, "should only return symbols from src/a.ts")
	assert.Equal(t, "func1", resp.DeadSymbols[0].Name)
	assert.Equal(t, "src/a.ts", resp.DeadSymbols[0].FilePath)
}

func TestFindDeadCode_WithCallers(t *testing.T) {
	t.Parallel()
	db := openToolTestDB(t)

	// Insert symbols with dependency edges
	callerID := insertSymbol(t, db, "repo", "caller", "function", "func caller() {}")
	calleeID := insertSymbol(t, db, "repo", "callee", "function", "func callee() {}")
	deadID := insertSymbol(t, db, "repo", "deadFunc", "function", "func deadFunc() {}")

	// Create dependency: caller -> callee (callee has caller, not dead)
	insertDependency(t, db, callerID, calleeID, "call")
	// deadFunc has no incoming edges

	handler := deadCodeHandler(db, "repo", "", NewSessionTokenTracker())
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"exclude_exported": false,
	}

	result, err := handler(context.Background(), req)
	require.NoError(t, err)
	assert.False(t, result.IsError)

	var resp struct {
		DeadSymbols []struct {
			Name string `json:"name"`
		} `json:"dead_symbols"`
		Count int `json:"count"`
	}
	raw, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	require.NoError(t, json.Unmarshal([]byte(raw.Text), &resp))

	// Should return deadFunc and caller (no incoming), but not callee
	assert.GreaterOrEqual(t, resp.Count, 1, "should find at least deadFunc")
	names := make([]string, len(resp.DeadSymbols))
	for i, s := range resp.DeadSymbols {
		names[i] = s.Name
	}
	assert.Contains(t, names, "deadFunc", "deadFunc has no callers")
	assert.NotContains(t, names, "callee", "callee has a caller")

	// Note: We use GreaterOrEqual because "caller" might also be returned
	// since it has no incoming edges (it's the root of the call chain)
	_ = deadID // suppress unused warning
}

func TestFindDeadCode_Limit(t *testing.T) {
	t.Parallel()
	db := openToolTestDB(t)

	// Insert 10 dead symbols
	for i := 0; i < 10; i++ {
		insertSymbol(t, db, "repo", string(rune('a'+i))+"Func", "function", "func x() {}")
	}

	handler := deadCodeHandler(db, "repo", "", NewSessionTokenTracker())
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"limit":            5,
		"exclude_exported": false,
	}

	result, err := handler(context.Background(), req)
	require.NoError(t, err)
	assert.False(t, result.IsError)

	var resp struct {
		DeadSymbols []any `json:"dead_symbols"`
		Count       int   `json:"count"`
	}
	raw, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	require.NoError(t, json.Unmarshal([]byte(raw.Text), &resp))

	assert.LessOrEqual(t, resp.Count, 5, "should respect limit")
	assert.LessOrEqual(t, len(resp.DeadSymbols), 5, "should return at most 5 symbols")
}

func TestFindDeadCode_EntryPoints(t *testing.T) {
	t.Parallel()
	db := openToolTestDB(t)

	// Insert entry point functions (should be excluded)
	insertSymbol(t, db, "repo", "main", "function", "func main() {}")
	insertSymbol(t, db, "repo", "init", "function", "func init() {}")
	insertSymbol(t, db, "repo", "Init", "function", "func Init() {}")
	insertSymbol(t, db, "repo", "Main", "function", "func Main() {}")
	insertSymbol(t, db, "repo", "regularFunc", "function", "func regularFunc() {}")

	handler := deadCodeHandler(db, "repo", "", NewSessionTokenTracker())
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"exclude_exported": false,
	}

	result, err := handler(context.Background(), req)
	require.NoError(t, err)
	assert.False(t, result.IsError)

	var resp struct {
		DeadSymbols []struct {
			Name string `json:"name"`
		} `json:"dead_symbols"`
	}
	raw, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	require.NoError(t, json.Unmarshal([]byte(raw.Text), &resp))

	// Entry points should be excluded, only regularFunc should be returned
	names := make([]string, len(resp.DeadSymbols))
	for i, s := range resp.DeadSymbols {
		names[i] = s.Name
	}

	assert.NotContains(t, names, "main", "main should be excluded")
	assert.NotContains(t, names, "init", "init should be excluded")
	assert.NotContains(t, names, "Init", "Init should be excluded")
	assert.NotContains(t, names, "Main", "Main should be excluded")
	assert.Contains(t, names, "regularFunc", "regular function should be included")
}

func TestFindDeadCode_InvalidFile(t *testing.T) {
	t.Parallel()
	db := openToolTestDB(t)

	projectRoot := t.TempDir()

	handler := deadCodeHandler(db, "repo", projectRoot, NewSessionTokenTracker())
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"file_path": "nonexistent/file.ts",
	}

	result, err := handler(context.Background(), req)
	require.NoError(t, err)
	assert.True(t, result.IsError, "should return error for nonexistent file")

	raw, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, raw.Text, "does not exist", "error should mention file doesn't exist")
}

func TestFindDeadCode_TokenSavings(t *testing.T) {
	t.Parallel()
	db := openToolTestDB(t)

	// Insert dead symbol
	insertSymbol(t, db, "repo", "deadFunc", "function", "func deadFunc() {}")

	// Insert file record for token savings calculation
	insertFile(t, db, "repo", "test.ts", 5000)

	handler := deadCodeHandler(db, "repo", "", NewSessionTokenTracker())
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"exclude_exported": false,
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
			SessionCostAvoided string `json:"session_cost_avoided"`
		} `json:"metadata"`
	}
	raw, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	require.NoError(t, json.Unmarshal([]byte(raw.Text), &resp))

	assert.GreaterOrEqual(t, resp.Metadata.TimingMs, int64(0), "timing should be recorded")
	assert.GreaterOrEqual(t, resp.Metadata.TokensSavedEst, int64(0), "token savings should be calculated")
	assert.NotEmpty(t, resp.Metadata.CostAvoidedEst, "cost avoided should be formatted")
	assert.Greater(t, resp.Metadata.SessionTokensSaved, int64(0), "session tokens should be tracked")
}
