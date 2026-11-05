package extract

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/context-link-mcp/context-link/internal/store"
	"github.com/context-link-mcp/context-link/pkg/models"
)

// insertRoutes stores n route definitions in file, with paths /<prefix>/<i>.
func insertRoutes(t *testing.T, db *store.DB, file, method, prefix string, n int) {
	t.Helper()
	routes := make([]models.Route, n)
	for i := range routes {
		path := fmt.Sprintf("/%s/%03d", prefix, i)
		routes[i] = models.Route{
			RepoName: "repo", Method: method, PathPattern: path, NormalizedPath: path,
			FilePath: file, StartLine: i + 1, Framework: "fastapi", Kind: "definition",
		}
	}
	require.NoError(t, store.BatchInsertRoutes(context.Background(), db, routes))
}

func hasWarning(res *Result, substr string) bool {
	for _, w := range res.Warnings {
		if strings.Contains(w, substr) {
			return true
		}
	}
	return false
}

// TestRunEntryPointsFilterNoiseBeforeCap is the regression for routes being
// capped at 200 in SQL before test routes were dropped: 250 test routes that
// sort first (DELETE < POST) used to push the real route out of the output.
func TestRunEntryPointsFilterNoiseBeforeCap(t *testing.T) {
	t.Parallel()
	db, embedder, info := setup(t)
	insertRoutes(t, db, "tests/test_routes.py", "DELETE", "a", 250)

	res, err := Run(context.Background(), db, embedder, nil, Options{
		RepoName: "repo", Queries: []string{"login"},
	}, info)
	require.NoError(t, err)
	require.Len(t, res.EntryPoints, 1)
	assert.Equal(t, "/api/login", res.EntryPoints[0].Path)
	assert.False(t, hasWarning(res, "entry points were omitted"))
}

// TestRunEntryPointsCapIsReported checks that more than MaxEntryPoints
// routes are cut deterministically (by file and line) and reported.
func TestRunEntryPointsCapIsReported(t *testing.T) {
	t.Parallel()
	db, embedder, info := setup(t)
	insertRoutes(t, db, "api/a.py", "GET", "a", MaxEntryPoints+10)

	res, err := Run(context.Background(), db, embedder, nil, Options{
		RepoName: "repo", Queries: []string{"login"},
	}, info)
	require.NoError(t, err)
	require.Len(t, res.EntryPoints, MaxEntryPoints)
	assert.Equal(t, "api/a.py", res.EntryPoints[0].File)
	assert.Equal(t, 1, res.EntryPoints[0].Line)
	assert.NotContains(t, entryPaths(res), "/api/login", "backend/routes.py sorts after api/a.py and is cut")
	assert.True(t, hasWarning(res, "11 more entry points were omitted"), res.Warnings)
}

// TestBuildEntryPointsHandlerLookupError: a failing handler lookup is returned
// as an error, not entry points without handler names. Routes without a
// handler need no lookup, so they still build on the same (closed) DB.
func TestBuildEntryPointsHandlerLookupError(t *testing.T) {
	t.Parallel()
	db, err := store.Open(filepath.Join(t.TempDir(), "closed.db"))
	require.NoError(t, err)
	require.NoError(t, db.Close())

	handler := int64(1)
	tests := []struct {
		name    string
		routes  []models.Route
		wantErr bool
	}{
		{"handler to look up", []models.Route{{Method: "GET", PathPattern: "/a", FilePath: "api/a.py", StartLine: 1, HandlerSymbolID: &handler}}, true},
		{"no handlers", []models.Route{{Method: "GET", PathPattern: "/a", FilePath: "api/a.py", StartLine: 1}}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			eps, omitted, err := buildEntryPoints(context.Background(), db, Options{RepoName: "repo"}, tc.routes)
			if tc.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "extract: failed to load route handlers")
				assert.Nil(t, eps)
				return
			}
			require.NoError(t, err)
			assert.Zero(t, omitted)
			require.Len(t, eps, 1)
			assert.Empty(t, eps[0].Handler)
		})
	}
}

func entryPaths(res *Result) []string {
	out := make([]string, len(res.EntryPoints))
	for i, ep := range res.EntryPoints {
		out[i] = ep.Path
	}
	return out
}

// TestGroupByFileTieBreakIsDeterministic is the regression for symbols with
// equal score and start line being ordered by map iteration: with one symbol
// per file, the same one must win on every run.
func TestGroupByFileTieBreakIsDeterministic(t *testing.T) {
	t.Parallel()
	symbols := map[int64]models.Symbol{}
	scores := map[int64]*scored{}
	for i, qn := range []string{"pkg.Zeta", "pkg.Alpha", "pkg.Mid", "pkg.Beta"} {
		id := int64(i + 1)
		symbols[id] = models.Symbol{ID: id, Name: qn, QualifiedName: qn, FilePath: "a.go", StartLine: 7}
		scores[id] = &scored{score: 0.5, bestScore: 0.5, bestReason: "query: " + qn}
	}
	opts := Options{MaxSymbolsPerFile: 2}

	for range 50 {
		candidates, _ := groupByFile(symbols, scores, opts)
		require.Len(t, candidates, 1)
		got := []string{candidates[0].symbols[0].QualifiedName, candidates[0].symbols[1].QualifiedName}
		require.Equal(t, []string{"pkg.Alpha", "pkg.Beta"}, got)
		require.Equal(t, "query: pkg.Alpha", candidates[0].reason)
	}
}
