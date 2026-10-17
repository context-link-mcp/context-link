package extract

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/context-link-mcp/context-link/internal/indexer"
	"github.com/context-link-mcp/context-link/internal/indexer/adapters"
	"github.com/context-link-mcp/context-link/internal/store"
	"github.com/context-link-mcp/context-link/internal/vectorstore"
)

func TestIsNoisePath(t *testing.T) {
	t.Parallel()
	tests := []struct {
		path string
		want bool
	}{
		{"backend/auth/login.py", false},
		{"src/routes/users.ts", false},
		{"tests/test_auth.py", true},
		{"backend/tests/helpers.py", true},
		{"backend/test_auth.py", true},
		{"conftest.py", true},
		{"internal/store/symbols_test.go", true},
		{"src/app.test.ts", true},
		{"src/app.spec.tsx", true},
		{"web/__tests__/App.js", true},
		{"testdata/langs/go/a.go", true},
		{"vendor/lib/x.go", true},
		{"node_modules/pkg/index.js", true},
		{"db/migrations/001_init.sql", true},
		{"src\\fixtures\\a.ts", true},
		{"src/latest/contest.py", false},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.want, IsNoisePath(tt.path), tt.path)
	}
}

const authSource = `def login(username, password):
    """Authenticate a user and start a session."""
    return check_password(username, password)


def check_password(username, password):
    return username == "admin" and password == "secret"
`

const testAuthSource = `def test_login_authentication():
    assert login("admin", "secret")
`

const routesSource = `from fastapi import APIRouter

router = APIRouter(prefix="/api")


@router.post("/login")
def login_endpoint(body: dict):
    return {"ok": True}
`

// setup indexes a small repo and returns the DB, embedder and index info.
func setup(t *testing.T) (*store.DB, vectorstore.Embedder, IndexInfo) {
	t.Helper()
	ctx := context.Background()

	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })
	require.NoError(t, store.Migrate(db))

	root := t.TempDir()
	files := map[string]string{
		"backend/auth.py":          authSource,
		"backend/routes.py":        routesSource,
		"tests/test_auth.py":       testAuthSource,
		"backend/migrations/01.py": "def upgrade():\n    pass\n",
	}
	for rel, src := range files {
		path := filepath.Join(root, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(src), 0o600))
	}

	registry := indexer.NewLanguageRegistry()
	require.NoError(t, registry.Register(adapters.NewPythonAdapter()))
	embedder := vectorstore.NewModel2VecEmbedder()
	stats, err := indexer.NewIndexer(registry, db, 1, embedder).IndexRepo(ctx, "repo", root)
	require.NoError(t, err)
	require.Equal(t, 0, stats.Errors)

	return db, embedder, IndexInfo{FilesDiscovered: stats.FilesDiscovered, ParseFailures: stats.FailedFiles}
}

func paths(files []FileEntry) []string {
	out := make([]string, len(files))
	for i, f := range files {
		out[i] = f.Path
	}
	return out
}

func TestRunExcludesTestsAndReportsEntryPoints(t *testing.T) {
	t.Parallel()
	db, embedder, info := setup(t)

	res, err := Run(context.Background(), db, embedder, nil, Options{
		RepoName: "repo",
		Queries:  []string{"login authentication"},
	}, info)
	require.NoError(t, err)

	assert.Equal(t, SchemaVersion, res.SchemaVersion)
	assert.Equal(t, "repo", res.Repository)
	assert.Equal(t, 4, res.Index.FilesDiscovered)
	assert.Equal(t, 4, res.Index.FilesIndexed)
	assert.Empty(t, res.Index.ParseFailures)
	assert.Equal(t, map[string]int{"python": 4}, res.Index.Languages)

	assert.Contains(t, paths(res.Files), "backend/auth.py")
	assert.NotContains(t, paths(res.Files), "tests/test_auth.py")
	assert.NotContains(t, paths(res.Files), "backend/migrations/01.py")
	assert.NotEmpty(t, res.Warnings, "excluded matches must be reported")

	require.Len(t, res.EntryPoints, 1)
	ep := res.EntryPoints[0]
	assert.Equal(t, "POST", ep.Method)
	assert.Equal(t, "/api/login", ep.Path)
	assert.Equal(t, "login_endpoint", ep.Handler)
	assert.Equal(t, "fastapi", ep.Framework)

	assert.LessOrEqual(t, res.Budget.UsedBytes, res.Budget.LimitBytes)
	assert.Equal(t, DefaultBudgetBytes, res.Budget.LimitBytes)
}

func TestRunIncludeTests(t *testing.T) {
	t.Parallel()
	db, embedder, info := setup(t)

	res, err := Run(context.Background(), db, embedder, nil, Options{
		RepoName:     "repo",
		Queries:      []string{"login authentication"},
		IncludeTests: true,
	}, info)
	require.NoError(t, err)
	assert.Contains(t, paths(res.Files), "tests/test_auth.py")
}

func TestRunThreatModelProfileAddsEntryPointHandlers(t *testing.T) {
	t.Parallel()
	db, embedder, info := setup(t)

	res, err := Run(context.Background(), db, embedder, nil, Options{
		RepoName: "repo",
		Profile:  ProfileThreatModel,
	}, info)
	require.NoError(t, err)
	assert.Contains(t, paths(res.Files), "backend/routes.py", "entry-point handler file must be ranked")
}

func TestRunRespectsBudget(t *testing.T) {
	t.Parallel()
	db, embedder, info := setup(t)

	res, err := Run(context.Background(), db, embedder, nil, Options{
		RepoName:    "repo",
		Queries:     []string{"login authentication"},
		BudgetBytes: 90,
	}, info)
	require.NoError(t, err)

	assert.LessOrEqual(t, res.Budget.UsedBytes, 90)
	sum := 0
	for _, f := range res.Files {
		sum += len(f.Content)
	}
	assert.Equal(t, res.Budget.UsedBytes, sum, "used_bytes must equal the summed content size")
}

func TestRunValidatesOptions(t *testing.T) {
	t.Parallel()
	db, embedder, info := setup(t)

	_, err := Run(context.Background(), db, embedder, nil, Options{RepoName: "repo"}, info)
	assert.Error(t, err, "no query and no profile")

	_, err = Run(context.Background(), db, embedder, nil, Options{RepoName: "repo", Profile: "nope"}, info)
	assert.Error(t, err, "unknown profile")
}

func TestRunReportsParseFailures(t *testing.T) {
	t.Parallel()
	db, embedder, _ := setup(t)

	info := IndexInfo{ParseFailures: []indexer.IndexFailure{{Path: "broken.py", Error: "boom"}}}
	res, err := Run(context.Background(), db, embedder, nil, Options{
		RepoName: "repo",
		Queries:  []string{"login"},
	}, info)
	require.NoError(t, err)
	require.Len(t, res.Index.ParseFailures, 1)
	assert.Contains(t, res.Warnings[0], "1 files failed to index")
}
