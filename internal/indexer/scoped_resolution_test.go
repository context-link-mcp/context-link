package indexer

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/context-link-mcp/context-link/internal/indexer/adapters"
	"github.com/context-link-mcp/context-link/internal/store"
)

// resolutionRepo is a temp repo indexed with the Go, Python, TS and JS adapters.
type resolutionRepo struct {
	t    *testing.T
	ctx  context.Context
	db   *store.DB
	root string
	idx  *Indexer
}

func newResolutionRepo(t *testing.T, files map[string]string) *resolutionRepo {
	t.Helper()
	registry := NewLanguageRegistry()
	require.NoError(t, registry.Register(adapters.NewGoAdapter()))
	require.NoError(t, registry.Register(adapters.NewPythonAdapter()))
	require.NoError(t, registry.Register(adapters.NewTypeScriptAdapter()))
	require.NoError(t, registry.Register(adapters.NewJavaScriptAdapter()))
	r := &resolutionRepo{t: t, ctx: context.Background(), db: setupTestDB(t), root: t.TempDir()}
	r.idx = NewIndexer(registry, r.db, 1, nil)
	for name, content := range files {
		r.write(name, content)
	}
	return r
}

func (r *resolutionRepo) write(name, content string) {
	r.t.Helper()
	path := filepath.Join(r.root, filepath.FromSlash(name))
	require.NoError(r.t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(r.t, os.WriteFile(path, []byte(content), 0o600))
}

func (r *resolutionRepo) index() *IndexStats {
	r.t.Helper()
	stats, err := r.idx.IndexRepo(r.ctx, "repo", r.root)
	require.NoError(r.t, err)
	require.Zero(r.t, stats.Errors, "failed files: %v", stats.FailedFiles)
	return stats
}

// callees returns "file:qualified_name" of every callee of the caller symbol
// with the given qualified name in the given file.
func (r *resolutionRepo) callees(file, caller string) []string {
	r.t.Helper()
	rows, err := r.db.QueryContext(r.ctx, `
		SELECT callee.file_path || ':' || callee.qualified_name
		FROM dependencies d
		JOIN symbols caller ON caller.id = d.caller_id
		JOIN symbols callee ON callee.id = d.callee_id
		WHERE caller.repo_name = 'repo' AND caller.file_path = ? AND caller.qualified_name = ?`,
		file, caller)
	require.NoError(r.t, err)
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var s string
		require.NoError(r.t, rows.Scan(&s))
		out = append(out, s)
	}
	require.NoError(r.t, rows.Err())
	sort.Strings(out)
	return out
}

// TestScopedCallResolution guards against callees being resolved by bare name
// across the whole repo and every language (M3): os.Open linked to a repo
// Open, dict.get() to a class method get, and so on.
func TestScopedCallResolution(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		files  map[string]string
		file   string // caller's file
		caller string // caller's qualified name
		want   []string
	}{
		{
			name: "go external package call does not link to a repo function of the same name",
			files: map[string]string{
				"internal/store/db.go": "package store\n\nfunc Open(path string) error { return nil }\n",
				"cmd/app/main.go": `package main

import (
	"os"
	"database/sql"
)

func run() {
	os.Open("x")
	sql.Open("sqlite", "x")
}
`,
			},
			file: "cmd/app/main.go", caller: "run",
			want: []string{},
		},
		{
			name: "go qualified call to an imported repo package links",
			files: map[string]string{
				"go.mod":               "module example.com/app\n",
				"internal/store/db.go": "package store\n\nfunc Open(path string) error { return nil }\n",
				"internal/other/db.go": "package other\n\nfunc Open(path string) error { return nil }\n",
				"cmd/app/main.go": `package main

import (
	"os"
	"example.com/app/internal/store"
)

func run() {
	os.Open("x")
	store.Open("x")
}
`,
			},
			file: "cmd/app/main.go", caller: "run",
			want: []string{"internal/store/db.go:Open"},
		},
		{
			name: "go method call on an unknown receiver stays in its package",
			files: map[string]string{
				"internal/vec/heap.go": "package vec\n\ntype candidateHeap []int\n\nfunc (h candidateHeap) Len() int { return len(h) }\n",
				"internal/tools/list.go": `package tools

func count(xs interface{ Len() int }) int {
	return xs.Len()
}
`,
			},
			file: "internal/tools/list.go", caller: "count",
			want: []string{},
		},
		{
			name: "go method call in the same package links",
			files: map[string]string{
				"internal/vec/heap.go": "package vec\n\ntype candidateHeap []int\n\nfunc (h candidateHeap) Len() int { return len(h) }\n",
				"internal/vec/search.go": `package vec

func size(h candidateHeap) int {
	return h.Len()
}
`,
			},
			file: "internal/vec/search.go", caller: "size",
			want: []string{"internal/vec/heap.go:candidateHeap.Len"},
		},
		{
			name: "python method call on an unknown receiver does not link to another file's method",
			files: map[string]string{
				"app/connections.py": "class ConnectionManager:\n    def get(self, key):\n        return key\n",
				"app/handlers.py":    "def handle(d):\n    return d.get('k')\n",
			},
			file: "app/handlers.py", caller: "handle",
			want: []string{},
		},
		{
			name: "python plain call prefers the same file",
			files: map[string]string{
				"app/a.py":  "def helper():\n    return 1\n\ndef run():\n    return helper()\n",
				"lib/b.py":  "def helper():\n    return 2\n",
				"lib2/c.py": "def helper():\n    return 3\n",
			},
			file: "app/a.py", caller: "run",
			want: []string{"app/a.py:helper"},
		},
		{
			name: "python plain call prefers the same directory over the repo",
			files: map[string]string{
				"app/a.py": "def run():\n    return helper()\n",
				"app/b.py": "def helper():\n    return 2\n",
				"lib/c.py": "def helper():\n    return 3\n",
			},
			file: "app/a.py", caller: "run",
			want: []string{"app/b.py:helper"},
		},
		{
			name: "python plain call with an ambiguous name links nothing",
			files: map[string]string{
				"app/a.py":  "def run():\n    return parse()\n",
				"lib/b.py":  "def parse():\n    return 2\n",
				"lib2/c.py": "def parse():\n    return 3\n",
			},
			file: "app/a.py", caller: "run",
			want: []string{},
		},
		{
			name: "python plain call with a name unique in the repo links",
			files: map[string]string{
				"app/a.py": "def run():\n    return parse()\n",
				"lib/b.py": "def parse():\n    return 2\n",
			},
			file: "app/a.py", caller: "run",
			want: []string{"lib/b.py:parse"},
		},
		{
			name: "python import picks the imported module over an equally named one",
			files: map[string]string{
				"app/a.py":  "from lib.b import parse\n\ndef run():\n    return parse()\n",
				"lib/b.py":  "def parse():\n    return 2\n",
				"lib2/c.py": "def parse():\n    return 3\n",
				"lib2/d.py": "def other():\n    return 3\n",
			},
			file: "app/a.py", caller: "run",
			want: []string{"lib/b.py:parse"},
		},
		{
			name: "python module-qualified call links into the module only",
			files: map[string]string{
				"app/a.py":  "import os.path\nimport lib.b\n\ndef run():\n    os.path.join('a')\n    return lib.b.parse()\n",
				"lib/b.py":  "def parse():\n    return 2\n\ndef join(x):\n    return x\n",
				"lib2/c.py": "def parse():\n    return 3\n",
			},
			file: "app/a.py", caller: "run",
			want: []string{"lib/b.py:parse"},
		},
		{
			name: "python receiver bound by a from-import of a module links into it",
			files: map[string]string{
				"tests/test_a.py":    "from backend.db import crud\n\nasync def test_run():\n    return await crud.create_model()\n",
				"backend/db/crud.py": "async def create_model():\n    return 1\n",
				"backend/db/fake.py": "async def create_model():\n    return 2\n",
			},
			file: "tests/test_a.py", caller: "test_run",
			want: []string{"backend/db/crud.py:create_model"},
		},
		{
			name: "python package bound by a from-import covers the modules it re-exports",
			files: map[string]string{
				"app/runner.py":                  "from app.pipeline import nodes\n\ndef run():\n    return nodes.generate()\n",
				"app/pipeline/nodes/__init__.py": "from .threats import generate\n",
				"app/pipeline/nodes/threats.py":  "def generate():\n    return 1\n",
				"lib/gen.py":                     "def generate():\n    return 2\n",
			},
			file: "app/runner.py", caller: "run",
			want: []string{"app/pipeline/nodes/threats.py:generate"},
		},
		{
			name: "python class bound by a from-import links its method",
			files: map[string]string{
				"app/a.py":      "from app.models import Thing\n\ndef run():\n    return Thing.build()\n",
				"app/models.py": "class Thing:\n    @classmethod\n    def build(cls):\n        return cls()\n",
				"lib/other.py":  "class Other:\n    def build(self):\n        return 1\n",
			},
			file: "app/a.py", caller: "run",
			want: []string{"app/models.py:Thing.build"},
		},
		{
			name: "python relative from-import links the imported name",
			files: map[string]string{
				"pkg/sub/a.py":        "from ..util import parse\n\ndef run():\n    return parse()\n",
				"pkg/util.py":         "def parse():\n    return 1\n",
				"other/util.py":       "def parse():\n    return 2\n",
				"pkg/sub/__init__.py": "",
			},
			file: "pkg/sub/a.py", caller: "run",
			want: []string{"pkg/util.py:parse"},
		},
		{
			name: "python self call prefers the caller's own class",
			files: map[string]string{
				"app/a.py": `class A:
    def helper(self):
        return 1

    def run(self):
        return self.helper()


class B:
    def helper(self):
        return 2
`,
			},
			file: "app/a.py", caller: "A.run",
			want: []string{"app/a.py:A.helper"},
		},
		{
			name: "plain calls never resolve across languages",
			files: map[string]string{
				"svc/a.py":    "def run():\n    return Load()\n",
				"svc/load.go": "package svc\n\nfunc Load() int { return 1 }\n",
			},
			file: "svc/a.py", caller: "run",
			want: []string{},
		},
		{
			name: "js namespace import links to the imported file",
			files: map[string]string{
				"src/main.js":  "import * as api from './api';\n\nfunction run() {\n  return api.fetch();\n}\n",
				"src/api.js":   "export function fetch() {\n  return 1;\n}\n",
				"src/other.js": "export function fetch() {\n  return 2;\n}\n",
			},
			file: "src/main.js", caller: "run",
			want: []string{"src/api.js:fetch"},
		},
		{
			name: "ts external default import links nothing",
			files: map[string]string{
				"src/main.ts": "import axios from 'axios';\n\nfunction run() {\n  return axios.get('/x');\n}\n",
				"src/http.ts": "export function get(url: string) {\n  return url;\n}\n",
			},
			file: "src/main.ts", caller: "run",
			want: []string{},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := newResolutionRepo(t, tc.files)
			r.index()
			assert.Equal(t, tc.want, r.callees(tc.file, tc.caller))
		})
	}
}

// TestResolutionIsDeterministic guards against "last row wins" resolution:
// indexing the same repo twice from scratch must give the same edges.
func TestResolutionIsDeterministic(t *testing.T) {
	t.Parallel()
	files := map[string]string{
		"app/a.py":  "def run():\n    parse()\n    helper()\n",
		"app/b.py":  "def helper():\n    return 1\n",
		"lib/b.py":  "def parse():\n    return 2\n",
		"lib2/c.py": "def parse():\n    return 3\n",
	}
	first := newResolutionRepo(t, files)
	first.index()
	for i := 0; i < 3; i++ {
		again := newResolutionRepo(t, files)
		again.index()
		assert.Equal(t, first.callees("app/a.py", "run"), again.callees("app/a.py", "run"))
	}
	assert.Equal(t, []string{"app/b.py:helper"}, first.callees("app/a.py", "run"))
}

// TestInterruptedRunRestoresInboundEdges guards against edges from unchanged
// callers into an edited file being lost for good when the run fails between
// detaching them (Phase 4) and resolving them again (Phase 5): the edited
// file is retried, but the unchanged caller's file is not.
func TestInterruptedRunRestoresInboundEdges(t *testing.T) {
	t.Parallel()
	r := newResolutionRepo(t, map[string]string{
		"cache.go": incrementalCacheSource,
		"main.go":  incrementalMainSource,
	})
	r.index()
	want := []string{"cache.go:Cache.Load", "cache.go:Helper"}
	require.Equal(t, want, r.callees("main.go", "run"))

	// Make Phase 5 fail: every edge insert aborts.
	_, err := r.db.ExecContext(r.ctx, `CREATE TRIGGER fail_edges BEFORE INSERT ON dependencies
		BEGIN SELECT RAISE(ABORT, 'injected failure'); END`)
	require.NoError(t, err)
	r.write("cache.go", "// edited\n"+incrementalCacheSource)
	_, err = r.idx.IndexRepo(r.ctx, "repo", r.root)
	require.Error(t, err, "the run must fail in Phase 5")
	assert.Empty(t, r.callees("main.go", "run"), "the inbound edges are detached")

	_, err = r.db.ExecContext(r.ctx, `DROP TRIGGER fail_edges`)
	require.NoError(t, err)
	stats := r.index()
	assert.Equal(t, 1, stats.FilesIndexed, "only cache.go is retried; main.go is unchanged")
	assert.Equal(t, want, r.callees("main.go", "run"), "main.go's edges into cache.go are restored")

	var pending int
	require.NoError(t, r.db.QueryRowContext(r.ctx, `SELECT COUNT(*) FROM pending_resolution`).Scan(&pending))
	assert.Zero(t, pending, "nothing is left pending after a complete run")
}

// TestAddedCalleeLinksUnchangedCaller guards against a call that was
// unresolved staying unresolved after another file adds the callee: the
// caller's file is unchanged, so only the name marking can re-resolve it.
func TestAddedCalleeLinksUnchangedCaller(t *testing.T) {
	t.Parallel()
	r := newResolutionRepo(t, map[string]string{
		"main.go":  incrementalMainSource,
		"cache.go": "package app\n\ntype Cache struct{}\n\nfunc (c *Cache) Load() int { return 1 }\n",
	})
	r.index()
	require.Equal(t, []string{"cache.go:Cache.Load"}, r.callees("main.go", "run"), "Helper doesn't exist yet")

	r.write("helper.go", "package app\n\nfunc Helper() int { return 2 }\n")
	stats := r.index()
	assert.Equal(t, 1, stats.FilesIndexed, "only helper.go is new")
	assert.Equal(t, []string{"cache.go:Cache.Load", "helper.go:Helper"}, r.callees("main.go", "run"))

	// A second Helper in the package makes the call ambiguous: the unchanged
	// caller is re-resolved again and loses the edge rather than keep a guess.
	r.write("dup.go", "package app\n\nfunc Helper() int { return 3 }\n")
	r.index()
	assert.Equal(t, []string{"cache.go:Cache.Load"}, r.callees("main.go", "run"))

	// Removing it makes Helper unique again.
	require.NoError(t, os.Remove(filepath.Join(r.root, "dup.go")))
	r.index()
	assert.Equal(t, []string{"cache.go:Cache.Load", "helper.go:Helper"}, r.callees("main.go", "run"))
}
