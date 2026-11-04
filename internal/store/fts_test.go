package store

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSanitizeFTSQuery(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "simple query is OR of words",
			input:    "error handling",
			expected: `"error" OR "handling"`,
		},
		{
			name:     "word order does not matter",
			input:    "token validate",
			expected: `"token" OR "validate"`,
		},
		{
			name:     "parentheses are stripped",
			input:    "error (timeout)",
			expected: `"error" OR "timeout"`,
		},
		{
			name:     "double quotes cannot open a phrase",
			input:    `user's "token"`,
			expected: `"user" OR "s" OR "token"`,
		},
		{
			name:     "AND operator becomes a literal term",
			input:    "retry AND backoff",
			expected: `"retry" OR "backoff"`,
		},
		{
			name:     "OR operator is a stopword, not an operator",
			input:    "cache OR throttle",
			expected: `"cache" OR "throttle"`,
		},
		{
			name:     "NOT operator is quoted",
			input:    "error NOT timeout",
			expected: `"error" OR "not" OR "timeout"`,
		},
		{
			name:     "NEAR operator is quoted",
			input:    "user NEAR token",
			expected: `"user" OR "near" OR "token"`,
		},
		{
			name:     "asterisk wildcard is stripped",
			input:    "error*",
			expected: `"error"`,
		},
		{
			name:     "caret prefix is stripped",
			input:    "^error",
			expected: `"error"`,
		},
		{
			name:     "column filter syntax is stripped",
			input:    "name:secret -kind +x",
			expected: `"name" OR "secret" OR "kind" OR "x"`,
		},
		{
			name:     "multiple quotes",
			input:    `"hello" "world"`,
			expected: `"hello" OR "world"`,
		},
		{
			name:     "empty query",
			input:    "",
			expected: ``,
		},
		{
			name:     "punctuation only",
			input:    `"()*^:`,
			expected: ``,
		},
		{
			name:     "mixed special characters",
			input:    `error (timeout) AND "retry" OR backoff*`,
			expected: `"error" OR "timeout" OR "retry" OR "backoff"`,
		},
		{
			name:     "stopwords dropped from natural language",
			input:    "how do we validate a token",
			expected: `"validate" OR "token"`,
		},
		{
			name:     "only stopwords are kept",
			input:    "the and",
			expected: `"the" OR "and"`,
		},
		{
			name:     "duplicates removed, case folded",
			input:    "Token token TOKEN",
			expected: `"token"`,
		},
		{
			name:     "non-ASCII words kept",
			input:    "café 用户",
			expected: `"café" OR "用户"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := sanitizeFTSQuery(tt.input)
			assert.Equal(t, tt.expected, result, "sanitizeFTSQuery(%q) should return %q", tt.input, tt.expected)
		})
	}
}

func TestSanitizeFTSQuery_CapsTerms(t *testing.T) {
	t.Parallel()
	q := "a1 a2 a3 a4 a5 a6 a7 a8 a9 a10 a11 a12 a13 a14 a15 a16 a17 a18 a19 a20"
	got := sanitizeFTSQuery(q)
	assert.Equal(t, maxFTSQueryTerms-1, countOr(got), "at most %d terms", maxFTSQueryTerms)
}

func countOr(s string) int {
	n := 0
	for i := 0; i+4 <= len(s); i++ {
		if s[i:i+4] == " OR " {
			n++
		}
	}
	return n
}

func TestIdentifierWords(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in   []string
		want string
	}{
		{[]string{"validateToken"}, "validate token"},
		{[]string{"ParserPool.Parse"}, "parser pool parse"},
		{[]string{"HTTPServer"}, "http server"},
		{[]string{"getHTTPResponse2"}, "get http response2"},
		{[]string{"verify_token"}, ""}, // unicode61 already splits '_'
		{[]string{"main"}, ""},
		{[]string{"UserAuth.login"}, "user auth login"},
		{[]string{""}, ""},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.want, identifierWords(tt.in...), "identifierWords(%q)", tt.in)
	}
}

// insertFTSFixture inserts a symbol row plus its FTS row and returns the ID.
func insertFTSFixture(t *testing.T, db *DB, repo, name, kind, filePath, signature string) int64 {
	t.Helper()
	ctx := context.Background()
	res, err := db.ExecContext(ctx, `
		INSERT INTO symbols (repo_name, name, qualified_name, kind, file_path, content_hash, code_block, start_line, end_line, language)
		VALUES (?, ?, ?, ?, ?, 'h', ?, 1, 2, 'go')`, repo, name, name, kind, filePath, signature)
	require.NoError(t, err)
	id, err := res.LastInsertId()
	require.NoError(t, err)
	require.NoError(t, BatchInsertFTSSymbols(ctx, db, []FTSSymbol{{
		SymbolID: id, RepoName: repo, Name: name, QualifiedName: name, Kind: kind, Signature: signature,
	}}))
	return id
}

// TestFTSSearch_NaturalLanguage covers M2: the whole query used to be one
// exact phrase, so word order, extra words and camelCase names all missed.
func TestFTSSearch_NaturalLanguage(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	ctx := context.Background()

	validate := insertFTSFixture(t, db, "repo", "validateToken", "function", "auth/token.go", "func validateToken(tok string) error")
	parse := insertFTSFixture(t, db, "repo", "ParseFile", "function", "parse/file.go", "func ParseFile(path string) ([]Symbol, error)")
	insertFTSFixture(t, db, "repo", "connectDB", "function", "db/conn.go", "func connectDB() *DB")
	insertFTSFixture(t, db, "other", "validateToken", "function", "auth/token.go", "func validateToken()")

	tests := []struct {
		query string
		want  int64
	}{
		{"validate token", validate},
		{"token validate", validate},
		{"how do we validate a token", validate},
		{"validateToken", validate},
		{"parse file symbols", parse},
	}
	for _, tt := range tests {
		hits, err := FTSSearch(ctx, db, "repo", tt.query, 10)
		require.NoError(t, err, tt.query)
		require.NotEmpty(t, hits, "query %q should match", tt.query)
		assert.Equal(t, tt.want, hits[0].SymbolID, "query %q: best hit", tt.query)
		for _, h := range hits {
			assert.NotEqual(t, int64(0), h.SymbolID)
		}
	}
}

// TestFTSSearch_InjectionSafe runs hostile inputs through the real FTS5
// parser: none may produce a syntax error or match across repos.
func TestFTSSearch_InjectionSafe(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	ctx := context.Background()
	insertFTSFixture(t, db, "repo", "validateToken", "function", "a.go", "func validateToken()")
	secret := insertFTSFixture(t, db, "other", "secretThing", "function", "a.go", "func secretThing()")

	for _, q := range []string{
		`"`, `""`, `"unterminated`, `(`, `)`, `*`, `^`, `NEAR(a b)`, `a AND`, `OR`, `NOT x`,
		`name:secret`, `repo_name:other`, `{name kind}: x`, `-x`, `+`, `secret*`, "\x00", `'; DROP TABLE symbols; --`,
	} {
		hits, err := FTSSearch(ctx, db, "repo", q, 10)
		require.NoError(t, err, "query %q must not be an FTS5 syntax error", q)
		for _, h := range hits {
			assert.NotEqual(t, secret, h.SymbolID, "query %q leaked another repo", q)
		}
	}
	hits, err := FTSSearch(ctx, db, "repo", "secret", 10)
	require.NoError(t, err)
	assert.Empty(t, hits, "repo scoping must hold")
}

// TestFTSSearchFiltered covers M14: filters run in SQL before the LIMIT, so a
// filtered search still returns matches ranked below many unfiltered ones.
func TestFTSSearchFiltered(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		insertFTSFixture(t, db, "repo", "tokenHelper"+string(rune('A'+i)), "function", "util/x.go", "func tokenHelper() token token")
	}
	class := insertFTSFixture(t, db, "repo", "TokenStore", "class", "auth/store.go", "type TokenStore struct")

	hits, err := FTSSearchFiltered(ctx, db, "repo", "token", SymbolFilter{Kind: "class"}, 2)
	require.NoError(t, err)
	require.Len(t, hits, 1)
	assert.Equal(t, class, hits[0].SymbolID)

	hits, err = FTSSearchFiltered(ctx, db, "repo", "token", SymbolFilter{FilePathPrefix: "auth/"}, 2)
	require.NoError(t, err)
	require.Len(t, hits, 1)
	assert.Equal(t, class, hits[0].SymbolID)

	hits, err = FTSSearchFiltered(ctx, db, "repo", "token", SymbolFilter{FilePathPrefix: "util/"}, 10)
	require.NoError(t, err)
	assert.Len(t, hits, 5)

	ids, err := FilterSymbolIDs(ctx, db, "repo", SymbolFilter{Kind: "class", FilePathPrefix: "auth/"})
	require.NoError(t, err)
	assert.Equal(t, map[int64]struct{}{class: {}}, ids)

	// '%' and '_' are literal in the prefix (substr, not LIKE).
	ids, err = FilterSymbolIDs(ctx, db, "repo", SymbolFilter{FilePathPrefix: "%"})
	require.NoError(t, err)
	assert.Empty(t, ids)
}
