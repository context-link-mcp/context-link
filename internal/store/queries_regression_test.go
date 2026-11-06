package store

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/context-link-mcp/context-link/pkg/models"
)

// seedSymbols inserts symbols (one per file, line 1 unless given) and returns
// them by name, with IDs.
func seedSymbols(t *testing.T, db *DB, syms ...models.Symbol) map[string]models.Symbol {
	t.Helper()
	ctx := context.Background()
	for i := range syms {
		s := &syms[i]
		if s.RepoName == "" {
			s.RepoName = "repo"
		}
		if s.QualifiedName == "" {
			s.QualifiedName = s.Name
		}
		if s.Kind == "" {
			s.Kind = "function"
		}
		if s.StartLine == 0 {
			s.StartLine, s.EndLine = 1, 1
		}
		s.ContentHash, s.Language = "h", "go"
		if s.CodeBlock == "" {
			s.CodeBlock = "func " + s.Name + "() {}"
		}
	}
	require.NoError(t, BatchInsertSymbols(ctx, db, syms))
	out := make(map[string]models.Symbol, len(syms))
	for _, s := range syms {
		var id int64
		require.NoError(t, db.QueryRowContext(ctx,
			`SELECT id FROM symbols WHERE repo_name = ? AND qualified_name = ? AND file_path = ? AND start_line = ?`,
			s.RepoName, s.QualifiedName, s.FilePath, s.StartLine).Scan(&id))
		s.ID = id
		out[s.QualifiedName] = s
	}
	return out
}

// manyIDs returns n IDs: the given real ones followed by IDs that exist
// nowhere, so a query must bind more parameters than SQLite allows in one
// statement (32,766).
func manyIDs(n int, real ...int64) []int64 {
	ids := append([]int64{}, real...)
	for next := int64(10_000_000); len(ids) < n; next++ {
		ids = append(ids, next)
	}
	return ids
}

// TestIDQueries_MoreThanSQLiteParamLimit is the M16 regression: every
// IN (...) query over caller-supplied IDs is chunked. On master each of these
// failed with "too many SQL variables" (GetCalleeNamesForSymbols then dropped
// every callee keyword of a large first index).
func TestIDQueries_MoreThanSQLiteParamLimit(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	ctx := context.Background()
	syms := seedSymbols(t, db,
		models.Symbol{Name: "caller", FilePath: "a.go"},
		models.Symbol{Name: "callee", FilePath: "b.go"},
	)
	caller, callee := syms["caller"], syms["callee"]
	require.NoError(t, BatchInsertDependencies(ctx, db, []models.Dependency{
		{CallerID: caller.ID, CalleeID: callee.ID, Kind: "call"},
	}))
	_, err := SaveMemory(ctx, db, &models.Memory{SymbolID: &callee.ID, RepoName: "repo", Note: "n", Author: "agent"})
	require.NoError(t, err)

	const n = 40_000
	tests := []struct {
		name  string
		check func(t *testing.T)
	}{
		{"GetCalleeNamesForSymbols", func(t *testing.T) {
			got, err := GetCalleeNamesForSymbols(ctx, db, manyIDs(n, caller.ID))
			require.NoError(t, err)
			assert.Equal(t, map[int64][]string{caller.ID: {"callee"}}, got)
		}},
		{"GetSymbolsByIDs", func(t *testing.T) {
			got, err := GetSymbolsByIDs(ctx, db, "repo", manyIDs(n, caller.ID, callee.ID))
			require.NoError(t, err)
			assert.Len(t, got, 2)
		}},
		{"GetDirectDependencies", func(t *testing.T) {
			got, err := GetDirectDependencies(ctx, db, "repo", manyIDs(n, caller.ID))
			require.NoError(t, err)
			require.Len(t, got[caller.ID], 1)
			assert.Equal(t, "callee", got[caller.ID][0].Name)
		}},
		{"CountMemoriesBySymbolIDs", func(t *testing.T) {
			got, err := CountMemoriesBySymbolIDs(ctx, db, manyIDs(n, callee.ID))
			require.NoError(t, err)
			assert.Equal(t, map[int64]int{callee.ID: 1}, got)
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.check(t)
		})
	}
}

// TestGetCalleeNamesForSymbols_Sorted: each caller's callee names come back
// sorted, whatever order the edges were inserted in (master returned them in
// row order).
func TestGetCalleeNamesForSymbols_Sorted(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	ctx := context.Background()
	syms := seedSymbols(t, db,
		models.Symbol{Name: "caller", FilePath: "a.go"},
		models.Symbol{Name: "other", FilePath: "a.go", StartLine: 3, EndLine: 3},
		models.Symbol{Name: "zeta", FilePath: "z.go"},
		models.Symbol{Name: "alpha", FilePath: "y.go"},
		models.Symbol{Name: "mid", FilePath: "x.go"},
	)
	var deps []models.Dependency
	for _, callee := range []string{"zeta", "mid", "alpha"} {
		deps = append(deps,
			models.Dependency{CallerID: syms["caller"].ID, CalleeID: syms[callee].ID, Kind: "call"},
			models.Dependency{CallerID: syms["other"].ID, CalleeID: syms[callee].ID, Kind: "call"},
		)
	}
	require.NoError(t, BatchInsertDependencies(ctx, db, deps))

	got, err := GetCalleeNamesForSymbols(ctx, db, []int64{syms["other"].ID, syms["caller"].ID})
	require.NoError(t, err)
	want := []string{"alpha", "mid", "zeta"}
	assert.Equal(t, map[int64][]string{syms["caller"].ID: want, syms["other"].ID: want}, got)
}

func TestForEachIDChunk(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		n    int
		size int
		want []int
	}{
		{"empty", 0, 3, nil},
		{"exact", 6, 3, []int{3, 3}},
		{"remainder", 7, 3, []int{3, 3, 1}},
		{"default size", 1001, 0, []int{500, 500, 1}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ids := make([]int64, tc.n)
			var sizes []int
			require.NoError(t, forEachIDChunk(ids, tc.size, func(c []int64) error {
				sizes = append(sizes, len(c))
				return nil
			}))
			assert.Equal(t, tc.want, sizes)
		})
	}

	stop := errors.New("stop")
	calls := 0
	err := forEachIDChunk(make([]int64, 10), 2, func([]int64) error { calls++; return stop })
	assert.ErrorIs(t, err, stop)
	assert.Equal(t, 1, calls)
}

func TestEscapeLike(t *testing.T) {
	t.Parallel()
	tests := []struct{ in, want string }{
		{"plain", "plain"},
		{"%", `\%`},
		{"a_b", `a\_b`},
		{`c:\x`, `c:\\x`},
		{`50%_\`, `50\%\_\\`},
	}
	for _, tc := range tests {
		assert.Equal(t, tc.want, EscapeLike(tc.in), tc.in)
	}
}

// TestResolveSymbol is the M13 regression: on master a blank name, "%" or
// "_" resolved to an arbitrary symbol through an unescaped LIKE fallback, and
// any substring silently picked the first match.
func TestResolveSymbol(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	seedSymbols(t, db,
		models.Symbol{Name: "validateToken", QualifiedName: "Auth.validateToken", FilePath: "auth.go"},
		models.Symbol{Name: "parseConfig", FilePath: "cfg.go"},
		models.Symbol{Name: "parseFlags", FilePath: "flags.go"},
		models.Symbol{Name: "load_all", FilePath: "load.go"},
	)
	ctx := context.Background()

	tests := []struct {
		name           string
		input          string
		allowFuzzy     bool
		wantErr        error
		wantName       string
		wantFuzzy      bool
		wantCandidates int
	}{
		{"exact name", "validateToken", false, nil, "validateToken", false, 0},
		{"exact qualified name", "Auth.validateToken", false, nil, "validateToken", false, 0},
		{"empty", "", true, ErrEmptySymbolName, "", false, 0},
		{"blank", "  ", true, ErrEmptySymbolName, "", false, 0},
		{"percent is literal", "%", true, ErrSymbolNotFound, "", false, 0},
		{"underscore is literal", "e_onfig", true, ErrSymbolNotFound, "", false, 0}, // as a wildcard it matched parseConfig
		{"underscore matches underscore", "d_al", true, nil, "load_all", true, 0},
		{"unique substring, fuzzy allowed", "validateTok", true, nil, "validateToken", true, 0},
		{"unique substring, fuzzy refused", "validateTok", false, ErrSymbolNotFound, "", false, 1},
		{"ambiguous substring", "parse", true, ErrAmbiguousSymbol, "", false, 2},
		{"typo", "parsConfig", true, ErrSymbolNotFound, "", false, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			res, err := ResolveSymbol(ctx, db, "repo", tc.input, tc.allowFuzzy)
			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
				assert.Nil(t, res.Symbol)
			} else {
				require.NoError(t, err)
				require.NotNil(t, res.Symbol)
				assert.Equal(t, tc.wantName, res.Symbol.Name)
			}
			assert.Equal(t, tc.wantFuzzy, res.Fuzzy)
			assert.Len(t, res.Candidates, tc.wantCandidates)
		})
	}
}

// TestResolveSymbol_OverloadsAreNotAmbiguous: several symbols sharing one
// qualified name (overloads) resolve as one fuzzy match.
func TestResolveSymbol_OverloadsAreNotAmbiguous(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	seedSymbols(t, db,
		models.Symbol{Name: "render", QualifiedName: "View.render", FilePath: "v.ts", StartLine: 1, EndLine: 1},
		models.Symbol{Name: "render", QualifiedName: "View.render", FilePath: "v.ts", StartLine: 5, EndLine: 5},
	)
	res, err := ResolveSymbol(context.Background(), db, "repo", "rend", true)
	require.NoError(t, err)
	assert.True(t, res.Fuzzy)
	assert.Equal(t, 1, res.Symbol.StartLine)
}

// TestIsTestPath is the M15 regression for path classification: master's
// case-insensitive "%test_%" LIKE (with "_" a wildcard) made latest.go and
// contest.go tests.
func TestIsTestPath(t *testing.T) {
	t.Parallel()
	tests := []struct {
		path string
		want bool
	}{
		{"pkg/latest.go", false},
		{"pkg/contest.go", false},
		{"attestation/verify.go", false},
		{"src/latest_version.py", false},
		{"pkg/handler_test.go", true},
		{"tests/helpers.py", true},
		{"app/test_views.py", true},
		{"app/views_test.py", true},
		{"conftest.py", true},
		{"src/__tests__/a.js", true},
		{"src/a.test.ts", true},
		{"src/a.spec.tsx", true},
		{"spec/models/user_spec.rb", true},
		{"src/test/java/UserServiceTest.java", true},
		{"src/main/java/UserServiceTest.java", true},
		{"src/main/java/Contest.java", false},
		{"Tests/AppTests/AuthTests.swift", true},
		{"MyApp.Tests/Foo.cs", true},
		{`win\tests\x.go`, true},
		{"cmd/main.go", false},
	}
	for _, tc := range tests {
		assert.Equal(t, tc.want, IsTestPath(tc.path), tc.path)
	}
}

func TestIsTestSymbol(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		sym  models.Symbol
		want bool
	}{
		{"test file function", models.Symbol{Name: "TestFoo", Kind: "function", FilePath: "a_test.go"}, true},
		{"production testConnection", models.Symbol{Name: "testConnection", Kind: "method", FilePath: "db/client.ts"}, false},
		{"production TestConnection", models.Symbol{Name: "TestConnection", Kind: "method", FilePath: "db/client.go"}, false},
		{"rust mod tests", models.Symbol{Name: "parses", QualifiedName: "tests.parses", Kind: "function", FilePath: "src/lib.rs"}, true},
		{"class in test file", models.Symbol{Name: "Fixture", Kind: "class", FilePath: "a_test.go"}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, IsTestSymbol(tc.sym))
		})
	}
}

// TestGetTestsForSymbol: only real tests are returned, sorted, never nil.
func TestGetTestsForSymbol(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	ctx := context.Background()
	syms := seedSymbols(t, db,
		models.Symbol{Name: "Connect", FilePath: "db/conn.go"},
		models.Symbol{Name: "TestConnect", FilePath: "db/z_test.go"},
		models.Symbol{Name: "TestConnectRetry", FilePath: "db/a_test.go"},
		models.Symbol{Name: "useLatest", FilePath: "db/latest.go"},
		models.Symbol{Name: "testConnection", FilePath: "db/contest.go"},
		models.Symbol{Name: "Lonely", FilePath: "db/lonely.go"},
	)
	var deps []models.Dependency
	for _, caller := range []string{"TestConnect", "TestConnectRetry", "useLatest", "testConnection"} {
		deps = append(deps, models.Dependency{CallerID: syms[caller].ID, CalleeID: syms["Connect"].ID, Kind: "call"})
	}
	require.NoError(t, BatchInsertDependencies(ctx, db, deps))

	tests, truncated, err := GetTestsForSymbol(ctx, db, "repo", syms["Connect"].ID)
	require.NoError(t, err)
	assert.False(t, truncated)
	var names []string
	for _, s := range tests {
		names = append(names, s.Name)
	}
	assert.Equal(t, []string{"TestConnectRetry", "TestConnect"}, names, "sorted by file path; latest.go and contest.go are not tests")

	none, _, err := GetTestsForSymbol(ctx, db, "repo", syms["Lonely"].ID)
	require.NoError(t, err)
	assert.NotNil(t, none)
	assert.Empty(t, none)

	byName, _, err := GetTestsByNameHeuristic(ctx, db, "repo", "conn.Connect")
	require.NoError(t, err)
	names = names[:0]
	for _, s := range byName {
		names = append(names, s.Name)
	}
	assert.Equal(t, []string{"TestConnectRetry", "TestConnect"}, names)

	wild, _, err := GetTestsByNameHeuristic(ctx, db, "repo", "%")
	require.NoError(t, err)
	assert.Empty(t, wild, "%% is a literal, not a wildcard")
}

// TestGetTestsForSymbol_Bounded: results stop at MaxTestResults with
// truncated set (master returned every caller).
func TestGetTestsForSymbol_Bounded(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	ctx := context.Background()
	target := []models.Symbol{{Name: "Target", FilePath: "t.go"}}
	for i := 0; i < MaxTestResults+5; i++ {
		target = append(target, models.Symbol{Name: "TestTarget", QualifiedName: "TestTarget", FilePath: "t_test.go", StartLine: i + 1, EndLine: i + 1})
	}
	seedSymbols(t, db, target...)
	targetSym, err := GetSymbolByName(ctx, db, "repo", "Target")
	require.NoError(t, err)

	got, truncated, err := GetTestsByNameHeuristic(ctx, db, "repo", "Target")
	require.NoError(t, err)
	assert.Len(t, got, MaxTestResults)
	assert.True(t, truncated)

	var deps []models.Dependency
	rows, err := db.QueryContext(ctx, `SELECT id FROM symbols WHERE name = 'TestTarget'`)
	require.NoError(t, err)
	for rows.Next() {
		var id int64
		require.NoError(t, rows.Scan(&id))
		deps = append(deps, models.Dependency{CallerID: id, CalleeID: targetSym.ID, Kind: "call"})
	}
	require.NoError(t, rows.Err())
	rows.Close()
	require.NoError(t, BatchInsertDependencies(ctx, db, deps))

	got, truncated, err = GetTestsForSymbol(ctx, db, "repo", targetSym.ID)
	require.NoError(t, err)
	assert.Len(t, got, MaxTestResults)
	assert.True(t, truncated)
}

// TestGetMemoriesBySymbolName_ExactOnly: memories are matched by exact name
// or qualified name; master's unescaped '%'+name+'%' returned the notes of
// every symbol containing the name, and of every symbol for "%".
func TestGetMemoriesBySymbolName_ExactOnly(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	ctx := context.Background()
	syms := seedSymbols(t, db,
		models.Symbol{Name: "Login", QualifiedName: "Auth.Login", FilePath: "a.go"},
		models.Symbol{Name: "LoginHandler", FilePath: "h.go"},
	)
	for _, qn := range []string{"Auth.Login", "LoginHandler"} {
		id := syms[qn].ID
		_, err := SaveMemory(ctx, db, &models.Memory{SymbolID: &id, RepoName: "repo", Note: "note " + qn, Author: "agent", LastKnownSymbol: qn})
		require.NoError(t, err)
	}
	// An orphan last seen as Auth.Logout.
	_, err := SaveMemory(ctx, db, &models.Memory{RepoName: "repo", Note: "orphan", Author: "agent", LastKnownSymbol: "Auth.Logout"})
	require.NoError(t, err)

	tests := []struct {
		name  string
		input string
		want  []string
	}{
		{"name", "Login", []string{"note Auth.Login"}},
		{"qualified", "Auth.Login", []string{"note Auth.Login"}},
		{"other symbol", "LoginHandler", []string{"note LoginHandler"}},
		{"percent", "%", nil},
		{"substring", "Log", nil},
		{"blank", " ", nil},
		{"orphan by name", "Logout", []string{"orphan"}},
		{"orphan by qualified name", "Auth.Logout", []string{"orphan"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			mems, err := GetMemoriesBySymbolName(ctx, db, "repo", tc.input, 0, 10)
			require.NoError(t, err)
			var notes []string
			for _, m := range mems {
				notes = append(notes, m.Note)
			}
			assert.Equal(t, tc.want, notes)
		})
	}
}

// TestFindRoutes_PathWildcardsAreLiteral: "_" and "%" in the path filter
// match literally (master passed them to LIKE unescaped).
func TestFindRoutes_PathWildcardsAreLiteral(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	ctx := context.Background()
	require.NoError(t, BatchInsertRoutes(ctx, db, []models.Route{
		{RepoName: "repo", Method: "GET", PathPattern: "/user_info", NormalizedPath: "/user_info", FilePath: "a.py", StartLine: 1, Framework: "flask", Kind: "definition"},
		{RepoName: "repo", Method: "GET", PathPattern: "/userXinfo", NormalizedPath: "/userXinfo", FilePath: "a.py", StartLine: 2, Framework: "flask", Kind: "definition"},
	}))

	got, err := FindRoutes(ctx, db, "repo", RouteFilter{Path: "user_info"})
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "/user_info", got[0].PathPattern)

	got, err = FindRoutes(ctx, db, "repo", RouteFilter{Path: "%"})
	require.NoError(t, err)
	assert.Empty(t, got)
}

// TestSearchCodePatterns_CursorPaging: pages follow (file, line, id) order
// without gaps or repeats.
func TestSearchCodePatterns_CursorPaging(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	ctx := context.Background()
	var syms []models.Symbol
	for i := 0; i < 7; i++ {
		syms = append(syms, models.Symbol{Name: "f", QualifiedName: "f", FilePath: string(rune('a'+i%3)) + ".go", StartLine: i + 1, EndLine: i + 1})
	}
	seedSymbols(t, db, syms...)

	var seen []int64
	var cursor *SymbolCursor
	for {
		page, err := SearchCodePatterns(ctx, db, "repo", CodePatternFilter{Literals: []string{"func"}}, cursor, 3)
		require.NoError(t, err)
		for _, s := range page {
			seen = append(seen, s.ID)
		}
		if len(page) < 3 {
			break
		}
		cursor = CursorAfter(page[len(page)-1])
	}
	assert.Len(t, seen, 7)
	uniq := map[int64]bool{}
	for _, id := range seen {
		uniq[id] = true
	}
	assert.Len(t, uniq, 7)
}
