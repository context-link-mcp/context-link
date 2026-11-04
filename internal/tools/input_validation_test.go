package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/context-link-mcp/context-link/internal/store"
	"github.com/context-link-mcp/context-link/internal/vectorstore"
	"github.com/context-link-mcp/context-link/pkg/models"
)

// callTool runs a handler with args and returns the result and its text.
func callTool(t *testing.T, h server.ToolHandlerFunc, args map[string]any) (*mcp.CallToolResult, string) {
	t.Helper()
	req := mcp.CallToolRequest{}
	req.Params.Arguments = args
	result, err := h(context.Background(), req)
	require.NoError(t, err)
	require.NotEmpty(t, result.Content)
	raw, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	return result, raw.Text
}

// countMemories returns the number of memories in the DB.
func countMemories(t *testing.T, db *store.DB) int {
	t.Helper()
	var n int
	require.NoError(t, db.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM memories`).Scan(&n))
	return n
}

// TestSaveMemory_NeverAttachesToAGuess is the M13 regression: on master an
// empty name, "%" or a partial name attached the note to whatever symbol the
// unescaped LIKE fallback returned first.
func TestSaveMemory_NeverAttachesToAGuess(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		symbolName string
		note       string
		wantSaved  bool
		wantText   string
	}{
		{"empty name", "", "n", false, "must not be empty"},
		{"blank name", "   ", "n", false, "must not be empty"},
		{"percent", "%", "n", false, "not found"},
		{"underscore wildcard", "validate_oken", "n", false, "not found"},
		{"partial name", "validate", "n", false, "did you mean"},
		{"blank note", "validateToken", "  ", false, "'note'"},
		{"exact", "validateToken", "n", true, `"symbol_name":"validateToken"`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			db := openToolTestDB(t)
			insertSymbol(t, db, "repo", "validateToken", "function", "function validateToken() {}")
			insertSymbol(t, db, "repo", "unrelated", "function", "function unrelated() {}")

			result, text := callTool(t, saveMemoryHandler(db, "repo"), map[string]any{"symbol_name": tc.symbolName, "note": tc.note})
			assert.Equal(t, !tc.wantSaved, result.IsError, text)
			assert.Contains(t, text, tc.wantText)
			want := 0
			if tc.wantSaved {
				want = 1
			}
			assert.Equal(t, want, countMemories(t, db))
		})
	}
}

// TestGetCode_LookupOutcomes: unknown, ambiguous and fuzzy names are told
// apart, with candidates; master reported every failure as "not found" and
// silently used the first substring match.
func TestGetCode_LookupOutcomes(t *testing.T) {
	t.Parallel()
	db := openToolTestDB(t)
	insertSymbol(t, db, "repo", "parseConfig", "function", "function parseConfig() {}")
	insertSymbol(t, db, "repo", "parseFlags", "function", "function parseFlags() {}")
	insertSymbol(t, db, "repo", "validateToken", "function", "function validateToken() {}")

	_, text := callTool(t, getCodeBySymbolHandlerBatch(db, "repo", NewSessionTokenTracker()),
		map[string]any{"symbol_name": []any{"parse", "validateTok", "validateToken", "nope", "%"}})

	var resp struct {
		Results []struct {
			Input      string         `json:"input"`
			Error      string         `json:"error"`
			Candidates []string       `json:"candidates"`
			Data       map[string]any `json:"data"`
		} `json:"results"`
	}
	require.NoError(t, json.Unmarshal([]byte(text), &resp))
	require.Len(t, resp.Results, 5)

	ambiguous := resp.Results[0]
	assert.Contains(t, ambiguous.Error, "ambiguous")
	assert.Len(t, ambiguous.Candidates, 2)

	fuzzy := resp.Results[1]
	assert.Empty(t, fuzzy.Error)
	assert.Equal(t, "fuzzy", fuzzy.Data["match_type"])
	assert.Equal(t, []any{}, fuzzy.Data["dependencies"], "dependencies is [] not null")

	exact := resp.Results[2]
	assert.Equal(t, "exact", exact.Data["match_type"])

	assert.Contains(t, resp.Results[3].Error, "not found")
	assert.Contains(t, resp.Results[4].Error, "not found")
}

// TestGetMemories_ExactName: get_symbol_memories no longer returns the notes
// of every symbol containing the name.
func TestGetMemories_ExactName(t *testing.T) {
	t.Parallel()
	db := openToolTestDB(t)
	loginID := insertSymbol(t, db, "repo", "Login", "function", "func Login() {}")
	handlerID := insertSymbol(t, db, "repo", "LoginHandler", "function", "func LoginHandler() {}")
	for _, id := range []int64{loginID, handlerID} {
		id := id
		_, err := store.SaveMemory(context.Background(), db, &models.Memory{SymbolID: &id, RepoName: "repo", Note: fmt.Sprint("note ", id), Author: "agent"})
		require.NoError(t, err)
	}

	_, text := callTool(t, getMemoriesHandler(db, "repo"), map[string]any{"symbol_name": "Login"})
	assert.Contains(t, text, `"total_returned":1`)

	_, text = callTool(t, getMemoriesHandler(db, "repo"), map[string]any{"symbol_name": "%"})
	assert.Contains(t, text, `"memories":[]`)
}

// TestTestsTool_EmptyIsArray: no tests yields "tests": [] (master: null).
func TestTestsTool_EmptyIsArray(t *testing.T) {
	t.Parallel()
	db := openToolTestDB(t)
	insertSymbol(t, db, "repo", "Lonely", "function", "func Lonely() {}")

	result, text := callTool(t, testDiscoveryHandler(db, "repo", NewSessionTokenTracker()), map[string]any{"symbol_name": "Lonely"})
	require.False(t, result.IsError, text)
	assert.Contains(t, text, `"tests":[]`)
	assert.Contains(t, text, `"truncated":false`)
}

// TestUsages_EmptyIsArray: no callers yields "usages": [] (master: null).
func TestUsages_EmptyIsArray(t *testing.T) {
	t.Parallel()
	db := openToolTestDB(t)
	insertSymbol(t, db, "repo", "Lonely", "function", "func Lonely() {}")

	result, text := callTool(t, symbolUsagesHandler(db, "repo", NewSessionTokenTracker()), map[string]any{"symbol_name": "Lonely"})
	require.False(t, result.IsError, text)
	assert.Contains(t, text, `"usages":[]`)
}

func TestCleanRepoPath(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	tests := []struct {
		name    string
		in      string
		want    string
		wantErr bool
	}{
		{"plain", "src/a.go", "src/a.go", false},
		{"dot segments", "./src/../src/a.go", "src/a.go", false},
		{"backslashes", `src\pkg\a.go`, "src/pkg/a.go", false},
		{"absolute under root", filepath.Join(root, "src", "a.go"), "src/a.go", false},
		{"parent escape", "../../etc/passwd", "", true},
		{"hidden escape", "src/../../x", "", true},
		{"backslash escape", `..\..\x`, "", true},
		{"rooted", "/etc/passwd", "", true},
		{"root itself", ".", "", true},
		{"empty", " ", "", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := cleanRepoPath(root, tc.in)
			if tc.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

// TestSkeletonAndDeadCode_PathConfinement: paths outside the project root
// are rejected before the filesystem is consulted (master answered "does not
// exist" vs "no symbols" for ../ paths, an existence oracle), and backslash
// paths find the indexed file (master returned no dead code for them).
func TestSkeletonAndDeadCode_PathConfinement(t *testing.T) {
	t.Parallel()
	outer := t.TempDir()
	root := filepath.Join(outer, "proj")
	require.NoError(t, os.MkdirAll(filepath.Join(root, "src"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "src", "a.go"), []byte("package src\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(outer, "secret.txt"), []byte("x"), 0o644))

	db := openToolTestDB(t)
	insertSymbolWithLines(t, db, "repo", "unused", "function", "func unused() {}", "src/a.go", 1, 1)

	skeleton := fileSkeletonHandlerBatch(db, "repo", root, NewSessionTokenTracker())
	deadCode := deadCodeHandler(db, "repo", root, NewSessionTokenTracker())

	// An existing and a missing file outside the root give the same answer.
	_, existing := callTool(t, skeleton, map[string]any{"file_path": "../secret.txt"})
	_, missing := callTool(t, skeleton, map[string]any{"file_path": "../missing.txt"})
	assert.Contains(t, existing, "stay inside")
	assert.Contains(t, missing, "stay inside")

	for _, p := range []string{"../secret.txt", "../missing.txt"} {
		result, text := callTool(t, deadCode, map[string]any{"file_path": p})
		assert.True(t, result.IsError)
		assert.Contains(t, text, "stay inside")
	}

	// Backslash and dotted forms reach the indexed file.
	_, text := callTool(t, skeleton, map[string]any{"file_path": `src\a.go`})
	assert.Contains(t, text, `"success_count":1`)
	for _, p := range []string{`src\a.go`, "./src/a.go"} {
		result, text := callTool(t, deadCode, map[string]any{"file_path": p, "exclude_exported": false})
		require.False(t, result.IsError, text)
		assert.Contains(t, text, `"count":1`, p)
	}

	if runtime.GOOS == "windows" {
		_, text = callTool(t, skeleton, map[string]any{"file_path": `C:\Windows\win.ini`})
		assert.Contains(t, text, "stay inside")
	}
}

// TestSemanticSearch_TopKValidation: top_k above 50 is capped to 50 (master
// reset it to 10); zero or negative is an error.
func TestSemanticSearch_TopKValidation(t *testing.T) {
	t.Parallel()
	db, emb, repo := setupSearchDB(t)
	ctx := context.Background()
	for i := 0; i < 80; i++ {
		res, err := db.ExecContext(ctx, `
			INSERT INTO symbols (repo_name, name, qualified_name, kind, file_path, content_hash, code_block, start_line, end_line, language)
			VALUES (?, ?, ?, 'function', 'src/many.ts', 'h', 'code', ?, ?, 'typescript')`,
			repo, fmt.Sprintf("extra%02d", i), fmt.Sprintf("extra%02d", i), i+1, i+1)
		require.NoError(t, err)
		id, err := res.LastInsertId()
		require.NoError(t, err)
		vec, err := emb.EmbedOne(ctx, "query") // identical to the query: similarity 1
		require.NoError(t, err)
		require.NoError(t, vectorstore.UpsertEmbedding(ctx, db, id, repo, vec))
	}
	handler := semanticSearchHandler(db, emb, repo, NewSessionTokenTracker(), nil)

	_, text := callTool(t, handler, map[string]any{"query": "query", "top_k": float64(999)})
	var resp struct {
		Results []any `json:"results"`
	}
	require.NoError(t, json.Unmarshal([]byte(text), &resp))
	assert.Len(t, resp.Results, 50)

	for _, k := range []float64{0, -3} {
		result, _ := callTool(t, handler, map[string]any{"query": "query", "top_k": k})
		assert.True(t, result.IsError, "top_k %v", k)
	}
}

// TestRoutes_Truncated: more than 100 matching routes set "truncated" (the
// cap was silent before).
func TestRoutes_Truncated(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		routes int
		want   bool
	}{
		{"at cap", 100, false},
		{"over cap", 101, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			db := openToolTestDB(t)
			for i := 0; i < tc.routes; i++ {
				insertRoute(t, db, "repo", "GET", fmt.Sprintf("/r%03d", i), "app.py", "flask", "definition")
			}
			result, text := callTool(t, routesHandler(db, "repo", "", NewSessionTokenTracker()), map[string]any{})
			require.False(t, result.IsError, text)
			var resp struct {
				RouteCount      int   `json:"route_count"`
				Truncated       bool  `json:"truncated"`
				RoutesTruncated bool  `json:"routes_truncated"`
				Matches         []any `json:"matches"`
			}
			require.NoError(t, json.Unmarshal([]byte(text), &resp))
			assert.Equal(t, 100, resp.RouteCount)
			assert.Equal(t, tc.want, resp.Truncated)
			assert.Equal(t, tc.want, resp.RoutesTruncated)
			assert.NotNil(t, resp.Matches, "matches is [] not null")
		})
	}
}
