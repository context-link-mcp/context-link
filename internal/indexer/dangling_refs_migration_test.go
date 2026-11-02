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

// TestDanglingRefsMigration checks migration 011 on an index built while
// foreign keys were not enforced: rows pointing at missing symbols are
// dropped or detached, the callers that lose an edge are re-resolved, and the
// next normal index relinks the detached memory to the live symbol.
func TestDanglingRefsMigration(t *testing.T) {
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

	// Rebuild the state an old version left behind: helper was re-inserted
	// under a new ID while edges, memory, embedding and route still point at
	// the old one, plus rows for a symbol deleted long ago.
	const gone = 999999
	conn, err := db.Conn(ctx)
	require.NoError(t, err)
	for _, q := range []string{
		`PRAGMA foreign_keys = OFF`,
		`UPDATE symbols SET id = id + 100000 WHERE repo_name = 'repo' AND name = 'helper'`,
		`INSERT INTO vec_symbols (symbol_id, repo_name, embedding) VALUES (999999, 'repo', x'00000000')`,
		`INSERT INTO dependencies (caller_id, callee_id, kind) VALUES (999999, 999998, 'call')`,
		`INSERT INTO routes (repo_name, method, path_pattern, normalized_path, handler_symbol_id, file_path, start_line, framework)
		 VALUES ('repo', 'GET', '/x', '/x', 999999, 'app.py', 1, 'flask')`,
		`PRAGMA foreign_keys = ON`,
	} {
		_, err = conn.ExecContext(ctx, q)
		require.NoError(t, err, q)
	}
	require.NoError(t, conn.Close())
	assert.NotZero(t, countRows(t, db, `SELECT COUNT(*) FROM pragma_foreign_key_check`), "fixture must hold dangling rows")

	// Simulate an index built before the migration existed, then upgrade.
	_, err = db.ExecContext(ctx, `DELETE FROM schema_version WHERE filename = '011_drop_dangling_symbol_refs.sql'`)
	require.NoError(t, err)
	require.NoError(t, store.Migrate(db))

	assert.Zero(t, countRows(t, db, `SELECT COUNT(*) FROM pragma_foreign_key_check`), "no row points at a missing symbol")
	assert.Zero(t, countRows(t, db, `SELECT COUNT(*) FROM vec_symbols WHERE symbol_id = ?`, gone))
	assert.Equal(t, 1, countRows(t, db, `SELECT COUNT(*) FROM routes WHERE handler_symbol_id IS NULL`))

	orphans, err := store.GetOrphanedMemoriesBySymbol(ctx, db, "repo", "helper", "app.py")
	require.NoError(t, err)
	require.Len(t, orphans, 1, "the memory is detached, not deleted")
	assert.True(t, orphans[0].IsStale)
	assert.Equal(t, "symbol_deleted", orphans[0].StaleReason)

	hashes, err := store.GetFileHashIndex(ctx, db, "repo")
	require.NoError(t, err)
	assert.Empty(t, hashes["app.py"], "the memory's file is re-indexed next")
	assert.NotEmpty(t, hashes["other.py"])
	pending, err := store.GetPendingResolution(ctx, db, "repo")
	require.NoError(t, err)
	assert.Contains(t, pending, "other.py", "other lost its edge to helper")

	stats, err := idx.IndexRepo(ctx, "repo", root)
	require.NoError(t, err)
	assert.Equal(t, 1, stats.FilesIndexed, "only app.py is re-processed")
	assert.Equal(t, 1, stats.MemoriesRelinked)

	helper, err = store.GetSymbolByName(ctx, db, "repo", "helper")
	require.NoError(t, err)
	callers, err := store.GetDependenciesByCallee(ctx, db, helper.ID)
	require.NoError(t, err)
	assert.Len(t, callers, 3, "Service.run, handler and other")

	mems, err := store.GetMemoriesBySymbolName(ctx, db, "repo", "helper", 0, 10)
	require.NoError(t, err)
	require.Len(t, mems, 1, "memory is linked to the live symbol")
	assert.False(t, mems[0].IsStale)
}

func countRows(t *testing.T, db *store.DB, query string, args ...any) int {
	t.Helper()
	var n int
	require.NoError(t, db.QueryRowContext(context.Background(), query, args...).Scan(&n))
	return n
}
