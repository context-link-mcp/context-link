package indexer

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/context-link-mcp/context-link/internal/indexer/adapters"
	"github.com/context-link-mcp/context-link/internal/store"
	"github.com/context-link-mcp/context-link/pkg/models"
)

func TestEnclosingSymbol(t *testing.T) {
	t.Parallel()
	syms := []models.Symbol{
		{Name: "router", StartLine: 1, EndLine: 1},
		{Name: "Service", StartLine: 3, EndLine: 10},
		{Name: "run", StartLine: 4, EndLine: 6},
		{Name: "outer", StartLine: 12, EndLine: 12},
		{Name: "inner", StartLine: 12, EndLine: 12},
	}
	tests := []struct {
		name string
		line int
		want string
		ok   bool
	}{
		{"single-line symbol", 1, "router", true},
		{"method beats its class", 5, "run", true},
		{"class body outside methods", 8, "Service", true},
		{"same span and start: first in list", 12, "outer", true},
		{"module-level line", 2, "", false},
		{"after every symbol", 20, "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, ok := enclosingSymbol(syms, tt.line)
			assert.Equal(t, tt.ok, ok)
			assert.Equal(t, tt.want, got.Name)
		})
	}
}

const callerAttributionSource = `import os

router = make_router()


def helper():
    return 1


class Service:
    def run(self):
        return helper()


def handler():
    return helper()


def make_router():
    return None


print(helper())
`

// TestResolveDependenciesAttributesCallsToEnclosingSymbol guards against every
// call in a file being attached to the file's first symbol.
func TestResolveDependenciesAttributesCallsToEnclosingSymbol(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := setupTestDB(t)

	registry := NewLanguageRegistry()
	require.NoError(t, registry.Register(adapters.NewPythonAdapter()))
	root := t.TempDir()
	writeGoFile(t, root, "app.py", callerAttributionSource)

	stats, err := NewIndexer(registry, db, 1, nil).IndexRepo(ctx, "repo", root)
	require.NoError(t, err)
	require.Equal(t, 0, stats.Errors)

	sym := func(qualifiedName string) *models.Symbol {
		s, err := store.GetSymbolByQualifiedName(ctx, db, "repo", qualifiedName)
		require.NoError(t, err, qualifiedName)
		return s
	}
	callees := func(qualifiedName string) []int64 {
		deps, err := store.GetDependenciesByCaller(ctx, db, sym(qualifiedName).ID)
		require.NoError(t, err)
		var ids []int64
		for _, d := range deps {
			ids = append(ids, d.CalleeID)
		}
		return ids
	}
	helper := sym("helper").ID

	assert.Equal(t, []int64{sym("make_router").ID}, callees("router"))
	assert.Equal(t, []int64{helper}, callees("Service.run"))
	assert.Equal(t, []int64{helper}, callees("handler"))
	assert.Empty(t, callees("helper"))
	assert.Empty(t, callees("Service"), "the call inside run belongs to run, not its class")

	callers, err := store.GetDependenciesByCallee(ctx, db, helper)
	require.NoError(t, err)
	assert.Len(t, callers, 2, "module-level print(helper()) has no calling symbol")
}

// TestReresolveMigrationForcesFullReindex checks that after migration 007 the
// next normal index re-processes every file and rebuilds the edges, and that
// memories end up linked to the re-indexed symbols and not stale.
func TestReresolveMigrationForcesFullReindex(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := setupTestDB(t)

	registry := NewLanguageRegistry()
	require.NoError(t, registry.Register(adapters.NewPythonAdapter()))
	root := t.TempDir()
	writeGoFile(t, root, "app.py", callerAttributionSource)
	writeGoFile(t, root, "other.py", "def other():\n    return helper()\n")
	idx := NewIndexer(registry, db, 1, nil)

	_, err := idx.IndexRepo(ctx, "repo", root)
	require.NoError(t, err)
	helper, err := store.GetSymbolByName(ctx, db, "repo", "helper")
	require.NoError(t, err)
	_, err = store.SaveMemory(ctx, db, &models.Memory{
		SymbolID: &helper.ID, Note: "returns a constant", Author: "agent",
		LastKnownSymbol: "helper", LastKnownFile: "app.py",
	})
	require.NoError(t, err)

	// Simulate an index built before the migration existed, then upgrade.
	_, err = db.ExecContext(ctx, `DELETE FROM schema_version WHERE filename = '007_reresolve_dependencies.sql'`)
	require.NoError(t, err)
	require.NoError(t, store.Migrate(db))

	var edges int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM dependencies`).Scan(&edges))
	assert.Zero(t, edges, "migration drops the old edges")
	hashes, err := store.GetFileHashIndex(ctx, db, "repo")
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"app.py": "", "other.py": ""}, hashes)

	stats, err := idx.IndexRepo(ctx, "repo", root)
	require.NoError(t, err)
	assert.Equal(t, 2, stats.FilesIndexed, "every file is re-processed")
	assert.Zero(t, stats.FilesUnchanged)
	assert.Equal(t, 1, stats.MemoriesRelinked)

	helper, err = store.GetSymbolByName(ctx, db, "repo", "helper")
	require.NoError(t, err)
	callers, err := store.GetDependenciesByCallee(ctx, db, helper.ID)
	require.NoError(t, err)
	assert.Len(t, callers, 3, "Service.run, handler and other")

	mems, err := store.GetMemoriesBySymbolName(ctx, db, "repo", "helper", 0, 10)
	require.NoError(t, err)
	require.Len(t, mems, 1, "memory stays linked to the re-indexed symbol")
	assert.False(t, mems[0].IsStale)

	stats, err = idx.IndexRepo(ctx, "repo", root)
	require.NoError(t, err)
	assert.Zero(t, stats.FilesIndexed, "hashes are restored, so the next index is incremental again")
}
