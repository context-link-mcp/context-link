package indexer

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/context-link-mcp/context-link/internal/indexer/adapters"
	"github.com/context-link-mcp/context-link/internal/store"
)

const incrementalCacheSource = `package app

type Cache struct{}

func (c *Cache) Load() int { return 1 }

func Helper() int { return 2 }
`

const incrementalMainSource = `package app

func run(c *Cache) int {
	return c.Load() + Helper()
}
`

// incrementalEdgesFixture indexes a two-file Go repo where main.go calls into
// cache.go, and returns helpers to inspect the graph.
type incrementalEdgesFixture struct {
	t    *testing.T
	ctx  context.Context
	db   *store.DB
	root string
	idx  *Indexer
}

// With cascade set, foreign keys are switched on, so deleting a file's symbols
// also deletes every edge into and out of them (what the schema's ON DELETE
// CASCADE does, and what store.ReplaceFileIndex emulates with FKs off). The
// inbound edges must then be rebuilt, not merely repointed.
func newIncrementalEdgesFixture(t *testing.T, cascade bool) *incrementalEdgesFixture {
	t.Helper()
	registry := NewLanguageRegistry()
	require.NoError(t, registry.Register(adapters.NewGoAdapter()))
	db := setupTestDB(t)
	if cascade {
		// The pool holds a single connection, so the pragma sticks.
		_, err := db.Exec(`PRAGMA foreign_keys = ON`)
		require.NoError(t, err)
		var on int
		require.NoError(t, db.QueryRow(`PRAGMA foreign_keys`).Scan(&on))
		require.Equal(t, 1, on)
	}
	f := &incrementalEdgesFixture{t: t, ctx: context.Background(), db: db, root: t.TempDir(), idx: NewIndexer(registry, db, 1, nil)}
	writeGoFile(t, f.root, "cache.go", incrementalCacheSource)
	writeGoFile(t, f.root, "main.go", incrementalMainSource)
	f.index()
	return f
}

func (f *incrementalEdgesFixture) index() *IndexStats {
	f.t.Helper()
	stats, err := f.idx.IndexRepo(f.ctx, "repo", f.root)
	require.NoError(f.t, err)
	require.Zero(f.t, stats.Errors)
	return stats
}

// callees returns "file:qualified_name" of every callee of the named caller.
func (f *incrementalEdgesFixture) callees(caller string) []string {
	f.t.Helper()
	sym, err := store.GetSymbolByQualifiedName(f.ctx, f.db, "repo", caller)
	require.NoError(f.t, err)
	deps, err := store.GetDependenciesByCaller(f.ctx, f.db, sym.ID)
	require.NoError(f.t, err)
	var out []string
	for _, d := range deps {
		var file, qn string
		err := f.db.QueryRowContext(f.ctx,
			`SELECT file_path, qualified_name FROM symbols WHERE id = ?`, d.CalleeID).Scan(&file, &qn)
		require.NoError(f.t, err, "edge %d->%d points at a missing symbol", d.CallerID, d.CalleeID)
		out = append(out, file+":"+qn)
	}
	sort.Strings(out)
	return out
}

// requireNoDanglingEdges fails if any edge references a symbol that no longer exists.
func (f *incrementalEdgesFixture) requireNoDanglingEdges() {
	f.t.Helper()
	var dangling int
	require.NoError(f.t, f.db.QueryRowContext(f.ctx, `
		SELECT COUNT(*) FROM dependencies d
		WHERE NOT EXISTS (SELECT 1 FROM symbols s WHERE s.id = d.caller_id)
		   OR NOT EXISTS (SELECT 1 FROM symbols s WHERE s.id = d.callee_id)`).Scan(&dangling))
	assert.Zero(f.t, dangling, "dangling dependency edges")
}

// TestIncrementalIndexKeepsInboundEdges guards against edges from unchanged
// files dangling (or vanishing) after the callee's file is edited, moved or
// deleted: both when stale edges are left in place and when they are cascaded
// away with the symbols.
func TestIncrementalIndexKeepsInboundEdges(t *testing.T) {
	t.Parallel()
	for _, cascade := range []bool{false, true} {
		t.Run(fmt.Sprintf("cascade=%v", cascade), func(t *testing.T) {
			t.Parallel()
			testIncrementalInboundEdges(t, cascade)
		})
	}
}

func testIncrementalInboundEdges(t *testing.T, cascade bool) {
	want := []string{"cache.go:Cache.Load", "cache.go:Helper"}

	t.Run("edit callee file", func(t *testing.T) {
		t.Parallel()
		f := newIncrementalEdgesFixture(t, cascade)
		require.Equal(t, want, f.callees("run"))

		writeGoFile(t, f.root, "cache.go", "// edited\n"+incrementalCacheSource)
		stats := f.index()
		assert.Equal(t, 1, stats.FilesIndexed, "only cache.go changed")

		assert.Equal(t, want, f.callees("run"), "run's edges follow the re-inserted symbols")
		f.requireNoDanglingEdges()
	})

	t.Run("edit caller and callee files", func(t *testing.T) {
		t.Parallel()
		f := newIncrementalEdgesFixture(t, cascade)
		writeGoFile(t, f.root, "cache.go", "// edited\n"+incrementalCacheSource)
		writeGoFile(t, f.root, "main.go", "// edited\n"+incrementalMainSource)
		f.index()

		assert.Equal(t, want, f.callees("run"))
		f.requireNoDanglingEdges()
	})

	t.Run("callee removed from edited file", func(t *testing.T) {
		t.Parallel()
		f := newIncrementalEdgesFixture(t, cascade)
		writeGoFile(t, f.root, "cache.go", "package app\n\ntype Cache struct{}\n\nfunc (c *Cache) Load() int { return 1 }\n")
		f.index()

		assert.Equal(t, []string{"cache.go:Cache.Load"}, f.callees("run"), "Helper is gone, so its call is unresolved")
		f.requireNoDanglingEdges()
	})

	t.Run("callee file deleted", func(t *testing.T) {
		t.Parallel()
		f := newIncrementalEdgesFixture(t, cascade)
		require.NoError(t, os.Remove(filepath.Join(f.root, "cache.go")))
		stats := f.index()
		assert.Equal(t, 1, stats.FilesDeleted)

		assert.Empty(t, f.callees("run"), "calls into a deleted file are unresolved")
		f.requireNoDanglingEdges()
	})

	t.Run("callee moved to a new file", func(t *testing.T) {
		t.Parallel()
		f := newIncrementalEdgesFixture(t, cascade)
		require.NoError(t, os.Remove(filepath.Join(f.root, "cache.go")))
		writeGoFile(t, f.root, "store.go", incrementalCacheSource)
		f.index()

		assert.Equal(t, []string{"store.go:Cache.Load", "store.go:Helper"}, f.callees("run"))
		f.requireNoDanglingEdges()
	})
}
