package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/context-link-mcp/context-link/internal/store"
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

func TestRequiredLiterals(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		pattern  string
		expected []string
	}{
		{"simple literal", "errorHandler", []string{"errorHandler"}},
		{"pattern with anchors", "^function.*$", []string{"function"}},
		{"escapes are literal characters", `errors\.New\(`, []string{"errors.New("}},
		{"alternation contributes nothing", `retry.*(?:backoff|timeout)`, []string{"retry"}},
		{"alternation of words", `foo|bar`, nil},
		{"common alternation prefix is required", `handleGet|handlePost`, []string{"handle"}},
		{"no literal sequence", `.*`, nil},
		{"short literal (< 2 bytes)", `a.*`, nil},
		{"two-byte literal kept", `ab.*`, []string{"ab"}},
		{"word boundary is zero-width", `\berror\b`, []string{"error"}},
		{"whitespace class splits literals", `func\s+main`, []string{"func", "main"}},
		{"character class is not literal", `[abc]def`, []string{"def"}},
		{"optional part dropped", `colou?r`, []string{"colo"}},
		{"star part dropped", `ab(cd)*ef`, []string{"ab", "ef"}},
		{"plus keeps its body", `x(abc)+y`, []string{"abc"}},
		{"case folded ascii literal kept", `(?i)error`, []string{"ERROR"}}, // the parser stores folded runes upper-cased; LIKE ignores ASCII case
		{"case folded k dropped", `(?i)kelvin`, nil},
		{"case folded s dropped", `(?i)status`, nil},
		{"longest first, at most three", `alpha.beta.gamma.de.epsilon`, []string{"epsilon", "alpha", "gamma"}},
		{"invalid regex", `[`, nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.expected, requiredLiterals(tt.pattern))
		})
	}
}

// TestRequiredLiterals_Sound checks the prefilter property on real inputs:
// every string a pattern matches contains each required literal (ASCII case
// aside, as LIKE is case-insensitive).
func TestRequiredLiterals_Sound(t *testing.T) {
	t.Parallel()
	cases := []struct{ pattern, input string }{
		{`foo|bar`, "x bar y"},
		{`func\s+main`, "func   main()"},
		{`[Ee]rror`, "Error"},
		{`colou?r`, "color"},
		{`(?i)ERROR`, "error"},
		{`handleGet|handlePost`, "handlePost"},
		{`a\.b`, "a.b"},
	}
	for _, c := range cases {
		re := regexp.MustCompile(c.pattern)
		require.True(t, re.MatchString(c.input), c.pattern)
		for _, lit := range requiredLiterals(c.pattern) {
			assert.Contains(t, strings.ToLower(c.input), strings.ToLower(lit), "pattern %q", c.pattern)
		}
	}
}

// searchPatterns runs search_code_patterns and decodes the response.
func searchPatterns(t *testing.T, db *store.DB, args map[string]any) (patternResponse, bool) {
	t.Helper()
	handler := searchCodePatternsHandler(db, "repo", NewSessionTokenTracker())
	req := mcp.CallToolRequest{}
	req.Params.Arguments = args
	result, err := handler(context.Background(), req)
	require.NoError(t, err)
	var resp patternResponse
	if result.IsError {
		return resp, true
	}
	raw, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	require.NoError(t, json.Unmarshal([]byte(raw.Text), &resp))
	return resp, false
}

type patternResponse struct {
	Results []struct {
		SymbolName string `json:"symbol_name"`
	} `json:"results"`
	ResultCount     int    `json:"result_count"`
	Truncated       bool   `json:"truncated"`
	TruncatedReason string `json:"truncated_reason"`
}

func (r patternResponse) names() []string {
	names := make([]string, 0, len(r.Results))
	for _, res := range r.Results {
		names = append(names, res.SymbolName)
	}
	return names
}

// TestSearchCodePatterns_PrefilterKeepsRealMatches: patterns whose old
// heuristic prefilter excluded real matches. Each case fails on master.
func TestSearchCodePatterns_PrefilterKeepsRealMatches(t *testing.T) {
	t.Parallel()
	db := openToolTestDB(t)
	insertSymbol(t, db, "repo", "onlyBar", "function", "function onlyBar() { return bar(); }")
	insertSymbol(t, db, "repo", "hexy", "function", "function ABCD() {}")
	insertSymbol(t, db, "repo", "classy", "function", "function classy() { return zzError; }")

	tests := []struct {
		name    string
		pattern string
		want    string
	}{
		// Master prefiltered on "foo", the first branch only.
		{"second alternation branch", `foo\(|bar\(`, "onlyBar"},
		// Master skipped the "x" of \x41 and prefiltered on "41BCD".
		{"escape sequence", `\x41BCD`, "hexy"},
		// Master took the class contents "xyz" as a literal.
		{"character class", `[xyz]+Error`, "classy"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			resp, isErr := searchPatterns(t, db, map[string]any{"pattern": tc.pattern})
			require.False(t, isErr)
			assert.Equal(t, []string{tc.want}, resp.names())
		})
	}
}

// TestSearchCodePatterns_RegexBeforeLimit: the limit applies to regex
// matches, not to LIKE candidates. On master the candidates were cut at
// limit*2 before the regex ran, so matches after many non-matching
// candidates were lost.
func TestSearchCodePatterns_RegexBeforeLimit(t *testing.T) {
	t.Parallel()
	db := openToolTestDB(t)
	// 1,200 candidates containing "retry" (more than two pages) that do not
	// match, then two that do; file paths order them last.
	for i := 0; i < 1200; i++ {
		insertSymbolWithLines(t, db, "repo", fmt.Sprintf("noise%04d", i), "function",
			"function f() { retry(1) }", "a/noise.ts", i+1, i+1)
	}
	insertSymbolWithLines(t, db, "repo", "hitOne", "function", "function g() { retry(backoff) }", "z/hit.ts", 1, 1)
	insertSymbolWithLines(t, db, "repo", "hitTwo", "function", "function h() { retry(backoff) }", "z/hit.ts", 2, 2)

	resp, isErr := searchPatterns(t, db, map[string]any{"pattern": `retry\(backoff\)`, "limit": 1})
	require.False(t, isErr)
	assert.Equal(t, []string{"hitOne"}, resp.names())
	assert.True(t, resp.Truncated, "a second match exists beyond limit 1")
	assert.Equal(t, "limit", resp.TruncatedReason)

	resp, isErr = searchPatterns(t, db, map[string]any{"pattern": `retry\(backoff\)`, "limit": 5})
	require.False(t, isErr)
	assert.Equal(t, []string{"hitOne", "hitTwo"}, resp.names())
	assert.False(t, resp.Truncated)
}

func TestSearchCodePatterns_LimitValidation(t *testing.T) {
	t.Parallel()
	db := openToolTestDB(t)
	insertSymbol(t, db, "repo", "f", "function", "function f() {}")

	tests := []struct {
		name    string
		limit   any
		wantErr bool
	}{
		{"negative is invalid", -1, true}, // master returned no results
		{"zero is invalid", 0, true},
		{"above max is capped", 1000, false},
		{"default", nil, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			args := map[string]any{"pattern": "function"}
			if tc.limit != nil {
				args["limit"] = tc.limit
			}
			resp, isErr := searchPatterns(t, db, args)
			assert.Equal(t, tc.wantErr, isErr)
			if !tc.wantErr {
				assert.Equal(t, 1, resp.ResultCount)
			}
		})
	}
}

// TestSearchCodePatterns_EmptyResultsIsArray: no match returns "results": [].
func TestSearchCodePatterns_EmptyResultsIsArray(t *testing.T) {
	t.Parallel()
	db := openToolTestDB(t)
	handler := searchCodePatternsHandler(db, "repo", NewSessionTokenTracker())
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{"pattern": "nothing_matches_this"}
	result, err := handler(context.Background(), req)
	require.NoError(t, err)
	raw, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, raw.Text, `"results":[]`)
}

// TestSearchCodePatterns_PrefixWildcardsAreLiteral: "_" in file_path_prefix
// is a literal underscore, not a LIKE wildcard, and backslashes are
// normalised.
func TestSearchCodePatterns_PrefixWildcardsAreLiteral(t *testing.T) {
	t.Parallel()
	db := openToolTestDB(t)
	insertSymbolWithLines(t, db, "repo", "inUnderscore", "function", "function a() {}", "my_pkg/a.ts", 1, 1)
	insertSymbolWithLines(t, db, "repo", "inOther", "function", "function b() {}", "myXpkg/b.ts", 1, 1)

	resp, isErr := searchPatterns(t, db, map[string]any{"pattern": "function", "file_path_prefix": "my_pkg/"})
	require.False(t, isErr)
	assert.Equal(t, []string{"inUnderscore"}, resp.names())

	resp, isErr = searchPatterns(t, db, map[string]any{"pattern": "function", "file_path_prefix": `my_pkg\`})
	require.False(t, isErr)
	assert.Equal(t, []string{"inUnderscore"}, resp.names())
}
