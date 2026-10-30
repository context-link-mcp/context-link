package extract

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/context-link-mcp/context-link/internal/indexer"
	"github.com/context-link-mcp/context-link/internal/indexer/adapters"
	"github.com/context-link-mcp/context-link/internal/store"
	"github.com/context-link-mcp/context-link/internal/tools"
	"github.com/context-link-mcp/context-link/internal/vectorstore"
	"github.com/context-link-mcp/context-link/pkg/models"
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
	return setupFiles(t, map[string]string{
		"backend/auth.py":          authSource,
		"backend/routes.py":        routesSource,
		"tests/test_auth.py":       testAuthSource,
		"backend/migrations/01.py": "def upgrade():\n    pass\n",
	})
}

// setupFiles indexes the given repo-relative files as repo "repo".
func setupFiles(t *testing.T, files map[string]string) (*store.DB, vectorstore.Embedder, IndexInfo) {
	t.Helper()
	ctx := context.Background()

	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })
	require.NoError(t, store.Migrate(db))

	root := t.TempDir()
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

// symbol loads one indexed symbol by name.
func symbol(t *testing.T, db *store.DB, name string) models.Symbol {
	t.Helper()
	s, err := store.GetSymbolByName(context.Background(), db, "repo", name)
	require.NoError(t, err)
	return *s
}

// candidate builds a one-file pack candidate from indexed symbols.
func candidate(t *testing.T, db *store.DB, names ...string) ([]fileCandidate, map[int64]*scored) {
	t.Helper()
	scores := make(map[int64]*scored)
	c := fileCandidate{score: 1, reason: "query: test"}
	for _, n := range names {
		s := symbol(t, db, n)
		c.path = s.FilePath
		c.symbols = append(c.symbols, s)
		scores[s.ID] = &scored{score: 1}
	}
	return []fileCandidate{c}, scores
}

func findFile(t *testing.T, files []FileEntry, path string) FileEntry {
	t.Helper()
	for _, f := range files {
		if f.Path == path {
			return f
		}
	}
	require.Failf(t, "file not packed", "%s not in %v", path, paths(files))
	return FileEntry{}
}

func findRef(t *testing.T, f FileEntry, name string) SymbolRef {
	t.Helper()
	for _, r := range f.Symbols {
		if r.Name == name {
			return r
		}
	}
	require.Failf(t, "symbol not in entry", "%s not in %s", name, f.Path)
	return SymbolRef{}
}

func usedBytes(files []FileEntry) int {
	sum := 0
	for _, f := range files {
		sum += len(f.Content)
	}
	return sum
}

func TestRunSymbolRefsCarryLinesOffsetsAndDependencies(t *testing.T) {
	t.Parallel()
	db, embedder, info := setup(t)

	res, err := Run(context.Background(), db, embedder, nil, Options{
		RepoName: "repo",
		Queries:  []string{"login authentication"},
	}, info)
	require.NoError(t, err)
	assert.Equal(t, 2, res.SchemaVersion)

	for _, f := range res.Files {
		for _, r := range f.Symbols {
			stored := symbol(t, db, r.Name)
			assert.Equal(t, stored.StartLine, r.StartLine, r.Name)
			assert.Equal(t, stored.EndLine, r.EndLine, r.Name)
			want := stored.CodeBlock
			if f.Tier == "skeleton" {
				want = tools.ExtractSignature(stored.CodeBlock)
			}
			assert.True(t, strings.HasPrefix(f.Content[r.Offset:], want), "content[offset:] of %s", r.Name)
			assert.NotNil(t, r.Dependencies, "dependencies must be [] not null")
		}
	}

	login := findRef(t, findFile(t, res.Files, "backend/auth.py"), "login")
	assert.Equal(t, 1, login.StartLine)
	assert.Equal(t, 3, login.EndLine)
	assert.Equal(t, RoleMatch, login.Role)
	assert.Equal(t, []DependencyRef{{
		Name: "check_password", QualifiedName: "check_password", Kind: "function",
		File: "backend/auth.py", StartLine: 6, EndLine: 7,
	}}, login.Dependencies)
	assert.Equal(t, res.Budget.UsedBytes, usedBytes(res.Files))
}

func TestPackDependencyCode(t *testing.T) {
	t.Parallel()
	db, _, _ := setup(t)
	ctx := context.Background()
	opts := Options{RepoName: "repo", BudgetBytes: DefaultBudgetBytes}

	t.Run("callee packed as dependency", func(t *testing.T) {
		candidates, scores := candidate(t, db, "login")
		files, budget, stats, err := pack(ctx, db, opts, candidates, scores)
		require.NoError(t, err)
		require.Len(t, files, 1)
		f := files[0]
		assert.Equal(t, []string{"login", "check_password"}, []string{f.Symbols[0].Name, f.Symbols[1].Name})
		assert.Equal(t, RoleMatch, f.Symbols[0].Role)
		assert.Equal(t, RoleDependency, f.Symbols[1].Role)
		assert.Equal(t, symbol(t, db, "login").CodeBlock+codeSep+symbol(t, db, "check_password").CodeBlock, f.Content)
		assert.Equal(t, budget.UsedBytes, usedBytes(files))
		assert.Zero(t, stats.droppedDeps)
	})

	t.Run("depth 0 lists references only", func(t *testing.T) {
		candidates, scores := candidate(t, db, "login")
		o := opts
		o.SkipDependencyCode = true
		files, _, _, err := pack(ctx, db, o, candidates, scores)
		require.NoError(t, err)
		require.Len(t, files, 1)
		require.Len(t, files[0].Symbols, 1)
		assert.Equal(t, "check_password", files[0].Symbols[0].Dependencies[0].Name)
		assert.Equal(t, symbol(t, db, "login").CodeBlock, files[0].Content)
	})

	t.Run("callee that is also a match appears once", func(t *testing.T) {
		candidates, scores := candidate(t, db, "login", "check_password")
		files, _, _, err := pack(ctx, db, opts, candidates, scores)
		require.NoError(t, err)
		require.Len(t, files, 1)
		require.Len(t, files[0].Symbols, 2)
		for _, r := range files[0].Symbols {
			assert.Equal(t, RoleMatch, r.Role, r.Name)
		}
	})

	t.Run("budget too small for the callee", func(t *testing.T) {
		candidates, scores := candidate(t, db, "login")
		o := opts
		o.BudgetBytes = len(symbol(t, db, "login").CodeBlock)
		files, budget, stats, err := pack(ctx, db, o, candidates, scores)
		require.NoError(t, err)
		require.Len(t, files, 1)
		assert.Len(t, files[0].Symbols, 1)
		assert.Len(t, files[0].Symbols[0].Dependencies, 1, "dropped code stays listed as a reference")
		assert.Equal(t, 1, stats.droppedDeps)
		assert.Equal(t, budget.UsedBytes, usedBytes(files))
	})
}

func TestPackDependencyInOtherFileAndNoise(t *testing.T) {
	t.Parallel()
	db, _, _ := setupFiles(t, map[string]string{
		"backend/service.py": "def handle_order(order):\n    validate_order(order)\n    return build_fixture(order)\n",
		"backend/checks.py":  "import os\n\n\ndef validate_order(order):\n    return order is not None\n",
		"tests/helpers.py":   "def build_fixture(order):\n    return order\n",
	})
	ctx := context.Background()

	candidates, scores := candidate(t, db, "handle_order")
	files, budget, _, err := pack(ctx, db, Options{RepoName: "repo", BudgetBytes: DefaultBudgetBytes}, candidates, scores)
	require.NoError(t, err)
	assert.Equal(t, []string{"backend/service.py", "backend/checks.py"}, paths(files), "tests/ callee is not packed")

	dep := findFile(t, files, "backend/checks.py")
	assert.Equal(t, "code", dep.Tier)
	assert.Equal(t, "dependency of handle_order", dep.Reason)
	ref := findRef(t, dep, "validate_order")
	assert.Equal(t, RoleDependency, ref.Role)
	assert.Equal(t, 4, ref.StartLine)
	assert.Equal(t, budget.UsedBytes, usedBytes(files))

	handle := findRef(t, files[0], "handle_order")
	var names []string
	for _, d := range handle.Dependencies {
		names = append(names, d.Name)
	}
	assert.ElementsMatch(t, []string{"validate_order", "build_fixture"}, names, "noise callees stay listed as references")

	files, _, _, err = pack(ctx, db, Options{RepoName: "repo", BudgetBytes: DefaultBudgetBytes, IncludeTests: true}, candidates, scores)
	require.NoError(t, err)
	assert.Contains(t, paths(files), "tests/helpers.py")
}

func TestBuildContent(t *testing.T) {
	t.Parallel()
	tests := []struct {
		parts   []string
		sep     string
		want    string
		offsets []int
	}{
		{nil, codeSep, "", []int{}},
		{[]string{"a"}, codeSep, "a", []int{0}},
		{[]string{"ab", "c", "déf"}, codeSep, "ab\n\nc\n\ndéf", []int{0, 4, 7}},
		{[]string{"x", "y"}, skeletonSep, "x\ny", []int{0, 2}},
	}
	for _, tt := range tests {
		got, offsets := buildContent(tt.parts, tt.sep)
		assert.Equal(t, tt.want, got)
		assert.Equal(t, tt.offsets, offsets)
	}
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
