package store_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/context-link-mcp/context-link/internal/store"
	"github.com/context-link-mcp/context-link/pkg/models"
)

// openMemTestDB opens a migrated temp DB for memory tests.
func openMemTestDB(t *testing.T) *store.DB {
	t.Helper()
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "mem_test.db"))
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() }) //nolint:errcheck
	require.NoError(t, store.Migrate(db))
	return db
}

// seedSymbol inserts a minimal symbol and returns its ID.
func seedSymbol(t *testing.T, db *store.DB, repo, qualifiedName, filePath string) int64 {
	t.Helper()
	ctx := context.Background()
	res, err := db.ExecContext(ctx, `
		INSERT INTO symbols (repo_name, name, qualified_name, kind, file_path, content_hash, code_block, start_line, end_line, language)
		VALUES (?, ?, ?, 'function', ?, 'hash1', 'func foo() {}', 1, 5, 'go')
	`, repo, qualifiedName, qualifiedName, filePath)
	require.NoError(t, err)
	id, err := res.LastInsertId()
	require.NoError(t, err)
	return id
}

func TestSaveMemory_Basic(t *testing.T) {
	t.Parallel()
	db := openMemTestDB(t)
	ctx := context.Background()

	symID := seedSymbol(t, db, "repo", "validateToken", "auth.go")
	mem := &models.Memory{
		SymbolID: &symID,
		Note:     "This function validates JWT tokens.",
		Author:   "agent",
		LastKnownSymbol: "validateToken",
		LastKnownFile:   "auth.go",
	}

	id, err := store.SaveMemory(ctx, db, mem)
	require.NoError(t, err)
	assert.Greater(t, id, int64(0))
}

func TestSaveMemory_NoteTooLong(t *testing.T) {
	t.Parallel()
	db := openMemTestDB(t)
	ctx := context.Background()

	symID := seedSymbol(t, db, "repo", "foo", "foo.go")
	longNote := make([]rune, 2001)
	for i := range longNote {
		longNote[i] = 'x'
	}
	mem := &models.Memory{
		SymbolID: &symID,
		Note:     string(longNote),
		Author:   "agent",
	}
	_, err := store.SaveMemory(ctx, db, mem)
	assert.ErrorContains(t, err, "2000")
}

func TestSaveMemory_DuplicateRejected(t *testing.T) {
	t.Parallel()
	db := openMemTestDB(t)
	ctx := context.Background()

	symID := seedSymbol(t, db, "repo", "bar", "bar.go")
	mem := &models.Memory{SymbolID: &symID, Note: "same note", Author: "agent"}

	_, err := store.SaveMemory(ctx, db, mem)
	require.NoError(t, err)

	_, err = store.SaveMemory(ctx, db, mem)
	assert.ErrorContains(t, err, "identical note")
}

func TestSaveMemory_NilSymbolID(t *testing.T) {
	t.Parallel()
	db := openMemTestDB(t)
	ctx := context.Background()

	mem := &models.Memory{
		SymbolID:        nil,
		Note:            "orphaned note",
		Author:          "developer",
		LastKnownSymbol: "deletedFunc",
		LastKnownFile:   "old.go",
	}
	id, err := store.SaveMemory(ctx, db, mem)
	require.NoError(t, err)
	assert.Greater(t, id, int64(0))
}

func TestGetMemoriesBySymbolID(t *testing.T) {
	t.Parallel()
	db := openMemTestDB(t)
	ctx := context.Background()

	symID := seedSymbol(t, db, "repo", "myFunc", "file.go")

	for _, note := range []string{"note1", "note2"} {
		_, err := store.SaveMemory(ctx, db, &models.Memory{SymbolID: &symID, Note: note, Author: "agent"})
		require.NoError(t, err)
	}

	mems, err := store.GetMemoriesBySymbolID(ctx, db, symID)
	require.NoError(t, err)
	assert.Len(t, mems, 2)
}

func TestGetMemoriesBySymbolName(t *testing.T) {
	t.Parallel()
	db := openMemTestDB(t)
	ctx := context.Background()

	symID := seedSymbol(t, db, "repo", "validateToken", "auth.go")
	_, err := store.SaveMemory(ctx, db, &models.Memory{SymbolID: &symID, Note: "JWT logic here", Author: "agent"})
	require.NoError(t, err)

	mems, err := store.GetMemoriesBySymbolName(ctx, db, "repo", "validateToken", 0, 10)
	require.NoError(t, err)
	assert.Len(t, mems, 1)
	assert.Equal(t, "JWT logic here", mems[0].Note)
}

func TestGetMemoriesBySymbolName_NoMatch(t *testing.T) {
	t.Parallel()
	db := openMemTestDB(t)
	ctx := context.Background()

	mems, err := store.GetMemoriesBySymbolName(ctx, db, "repo", "nonExistent", 0, 10)
	require.NoError(t, err)
	assert.Empty(t, mems)
}

func TestGetMemoriesByFilePath(t *testing.T) {
	t.Parallel()
	db := openMemTestDB(t)
	ctx := context.Background()

	sym1 := seedSymbol(t, db, "repo", "funcA", "service.go")
	sym2 := seedSymbol(t, db, "repo", "funcB", "service.go")
	other := seedSymbol(t, db, "repo", "funcC", "other.go")

	_, err := store.SaveMemory(ctx, db, &models.Memory{SymbolID: &sym1, Note: "note A", Author: "agent"})
	require.NoError(t, err)
	_, err = store.SaveMemory(ctx, db, &models.Memory{SymbolID: &sym2, Note: "note B", Author: "agent"})
	require.NoError(t, err)
	_, err = store.SaveMemory(ctx, db, &models.Memory{SymbolID: &other, Note: "note C", Author: "agent"})
	require.NoError(t, err)

	mems, err := store.GetMemoriesByFilePath(ctx, db, "repo", "service.go", 0, 10)
	require.NoError(t, err)
	assert.Len(t, mems, 2)
}

func TestGetMemoriesByFilePath_Pagination(t *testing.T) {
	t.Parallel()
	db := openMemTestDB(t)
	ctx := context.Background()

	symID := seedSymbol(t, db, "repo", "bigFunc", "big.go")
	for i := 0; i < 5; i++ {
		_, err := store.SaveMemory(ctx, db, &models.Memory{
			SymbolID: &symID,
			Note:     "note " + string(rune('A'+i)),
			Author:   "agent",
		})
		require.NoError(t, err)
	}

	page1, err := store.GetMemoriesByFilePath(ctx, db, "repo", "big.go", 0, 2)
	require.NoError(t, err)
	assert.Len(t, page1, 2)

	page2, err := store.GetMemoriesByFilePath(ctx, db, "repo", "big.go", 2, 2)
	require.NoError(t, err)
	assert.Len(t, page2, 2)
}

func TestSnapshotAndMarkStale(t *testing.T) {
	t.Parallel()
	db := openMemTestDB(t)
	ctx := context.Background()

	symID := seedSymbol(t, db, "repo", "changedFunc", "mod.go")
	_, err := store.SaveMemory(ctx, db, &models.Memory{SymbolID: &symID, Note: "original note", Author: "agent"})
	require.NoError(t, err)

	n, err := store.SnapshotAndMarkStale(ctx, db, symID, "changedFunc", "mod.go", "hash_changed")
	require.NoError(t, err)
	assert.Equal(t, 1, n, "one memory should have been staled")

	mems, err := store.GetMemoriesBySymbolID(ctx, db, symID)
	require.NoError(t, err)
	require.Len(t, mems, 1)
	assert.True(t, mems[0].IsStale)
	assert.Equal(t, "hash_changed", mems[0].StaleReason)
	assert.Equal(t, "changedFunc", mems[0].LastKnownSymbol)
	assert.Equal(t, "mod.go", mems[0].LastKnownFile)
}

func TestGetOrphanedMemoriesBySymbol(t *testing.T) {
	t.Parallel()
	db := openMemTestDB(t)
	ctx := context.Background()

	// Orphaned memory (no symbol_id).
	mem := &models.Memory{
		SymbolID:        nil,
		Note:            "orphaned note",
		Author:          "agent",
		LastKnownSymbol: "deletedFunc",
		LastKnownFile:   "gone.go",
	}
	_, err := store.SaveMemory(ctx, db, mem)
	require.NoError(t, err)

	orphans, err := store.GetOrphanedMemoriesBySymbol(ctx, db, "deletedFunc", "gone.go")
	require.NoError(t, err)
	assert.Len(t, orphans, 1)
	assert.Equal(t, "orphaned note", orphans[0].Note)
}

func TestRelinkMemory(t *testing.T) {
	t.Parallel()
	db := openMemTestDB(t)
	ctx := context.Background()

	// Create orphaned memory.
	mem := &models.Memory{
		SymbolID:        nil,
		Note:            "relink me",
		Author:          "agent",
		LastKnownSymbol: "renamedFunc",
		LastKnownFile:   "service.go",
	}
	memID, err := store.SaveMemory(ctx, db, mem)
	require.NoError(t, err)

	// Create new symbol.
	newSymID := seedSymbol(t, db, "repo", "renamedFunc", "service.go")

	err = store.RelinkMemory(ctx, db, memID, newSymID)
	require.NoError(t, err)

	mems, err := store.GetMemoriesBySymbolID(ctx, db, newSymID)
	require.NoError(t, err)
	require.Len(t, mems, 1)
	assert.Equal(t, "relink me", mems[0].Note)
	assert.False(t, mems[0].IsStale)
	assert.Equal(t, newSymID, *mems[0].SymbolID)
}

func TestCountMemoriesBySymbolID(t *testing.T) {
	t.Parallel()
	db := openMemTestDB(t)
	ctx := context.Background()

	symID := seedSymbol(t, db, "repo", "counter", "cnt.go")

	count, err := store.CountMemoriesBySymbolID(ctx, db, symID)
	require.NoError(t, err)
	assert.Equal(t, 0, count)

	_, err = store.SaveMemory(ctx, db, &models.Memory{SymbolID: &symID, Note: "one", Author: "agent"})
	require.NoError(t, err)
	_, err = store.SaveMemory(ctx, db, &models.Memory{SymbolID: &symID, Note: "two", Author: "agent"})
	require.NoError(t, err)

	count, err = store.CountMemoriesBySymbolID(ctx, db, symID)
	require.NoError(t, err)
	assert.Equal(t, 2, count)
}

func TestCountMemoriesBySymbolIDs(t *testing.T) {
	t.Parallel()
	db := openMemTestDB(t)
	ctx := context.Background()

	sym1 := seedSymbol(t, db, "repo", "s1", "f1.go")
	sym2 := seedSymbol(t, db, "repo", "s2", "f2.go")

	_, err := store.SaveMemory(ctx, db, &models.Memory{SymbolID: &sym1, Note: "n1", Author: "agent"})
	require.NoError(t, err)
	_, err = store.SaveMemory(ctx, db, &models.Memory{SymbolID: &sym1, Note: "n2", Author: "agent"})
	require.NoError(t, err)
	_, err = store.SaveMemory(ctx, db, &models.Memory{SymbolID: &sym2, Note: "n3", Author: "agent"})
	require.NoError(t, err)

	counts, err := store.CountMemoriesBySymbolIDs(ctx, db, []int64{sym1, sym2})
	require.NoError(t, err)
	assert.Equal(t, 2, counts[sym1])
	assert.Equal(t, 1, counts[sym2])
}

func TestPurgeStaleMemories_All(t *testing.T) {
	t.Parallel()
	db := openMemTestDB(t)
	ctx := context.Background()

	symID := seedSymbol(t, db, "repo", "staleFunc", "f.go")
	_, err := store.SaveMemory(ctx, db, &models.Memory{SymbolID: &symID, Note: "will go stale", Author: "agent"})
	require.NoError(t, err)

	_, err = store.SnapshotAndMarkStale(ctx, db, symID, "staleFunc", "f.go", "hash_changed")
	require.NoError(t, err)

	n, err := store.PurgeStaleMemories(ctx, db, "repo", false)
	require.NoError(t, err)
	assert.Equal(t, 1, n)
}

func TestPurgeStaleMemories_OrphanedOnly(t *testing.T) {
	t.Parallel()
	db := openMemTestDB(t)
	ctx := context.Background()

	// Orphaned stale memory.
	mem := &models.Memory{
		SymbolID:        nil,
		Note:            "orphaned stale",
		Author:          "agent",
		LastKnownSymbol: "dead",
		LastKnownFile:   "dead.go",
	}
	_, err := store.SaveMemory(ctx, db, mem)
	require.NoError(t, err)

	// Manually mark it stale via direct SQL (no symbol_id to call SnapshotAndMarkStale).
	_, err = db.ExecContext(ctx, `UPDATE memories SET is_stale = 1 WHERE symbol_id IS NULL`)
	require.NoError(t, err)

	// Non-orphaned stale memory (linked symbol).
	symID := seedSymbol(t, db, "repo", "live", "live.go")
	_, err = store.SaveMemory(ctx, db, &models.Memory{SymbolID: &symID, Note: "live stale", Author: "agent"})
	require.NoError(t, err)
	_, err = store.SnapshotAndMarkStale(ctx, db, symID, "live", "live.go", "hash_changed")
	require.NoError(t, err)

	// Purge orphaned only — should only delete the orphaned one.
	n, err := store.PurgeStaleMemories(ctx, db, "repo", true)
	require.NoError(t, err)
	assert.Equal(t, 1, n)

	// The linked stale memory should still exist.
	mems, err := store.GetMemoriesBySymbolID(ctx, db, symID)
	require.NoError(t, err)
	assert.Len(t, mems, 1)
}
