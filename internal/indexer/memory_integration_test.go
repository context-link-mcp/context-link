package indexer

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/context-link/context-link/internal/indexer/adapters"
	"github.com/context-link/context-link/internal/store"
	"github.com/context-link/context-link/pkg/models"
)

// buildTestIndexer constructs a ready-to-use Indexer for integration tests.
func buildTestIndexer(t *testing.T, db *store.DB) *Indexer {
	t.Helper()
	registry := NewLanguageRegistry()
	require.NoError(t, registry.Register(adapters.NewGoAdapter()))
	return NewIndexer(registry, db, 1, nil)
}

// writeGoFile writes a minimal Go source file to dir/name and returns its path.
func writeGoFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	return path
}

// TestMemoryStaleOnReindex verifies the full flow:
//  1. Index a file with a function.
//  2. Attach a memory to that function.
//  3. Modify the file (change the function body).
//  4. Re-index.
//  5. Memory must be flagged is_stale=true with stale_reason='hash_changed'.
func TestMemoryStaleOnReindex(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := setupTestDB(t)
	idx := buildTestIndexer(t, db)

	projectRoot := t.TempDir()

	// --- Step 1: Index the original file. ---
	original := `package main

func Greet() string {
	return "hello"
}
`
	writeGoFile(t, projectRoot, "greet.go", original)

	stats, err := idx.IndexRepo(ctx, "repo", projectRoot)
	require.NoError(t, err)
	assert.Greater(t, stats.SymbolsExtracted, 0, "should have extracted symbols")

	// --- Step 2: Attach a memory to the Greet symbol. ---
	sym, _, err := store.GetSymbolWithDependencies(ctx, db, "repo", "Greet", 0)
	require.NoError(t, err, "Greet symbol must exist after indexing")

	memID, err := store.SaveMemory(ctx, db, &models.Memory{
		SymbolID:        &sym.ID,
		Note:            "Greet returns a greeting string.",
		Author:          "agent",
		LastKnownSymbol: sym.QualifiedName,
		LastKnownFile:   sym.FilePath,
	})
	require.NoError(t, err)
	assert.Greater(t, memID, int64(0))

	// Verify it's fresh.
	mems, err := store.GetMemoriesBySymbolID(ctx, db, sym.ID)
	require.NoError(t, err)
	require.Len(t, mems, 1)
	assert.False(t, mems[0].IsStale, "memory should be fresh before re-index")

	// --- Step 3: Modify the file. ---
	modified := `package main

func Greet() string {
	return "hello, world"
}
`
	writeGoFile(t, projectRoot, "greet.go", modified)

	// --- Step 4: Re-index. ---
	stats2, err := idx.IndexRepo(ctx, "repo", projectRoot)
	require.NoError(t, err)
	assert.Equal(t, 1, stats2.MemoriesOrphaned, "one memory should have been staled")

	// --- Step 5: Verify the memory is now stale. ---
	// Symbol ID may have changed after re-insert; look up by name.
	newSym, _, err := store.GetSymbolWithDependencies(ctx, db, "repo", "Greet", 0)
	require.NoError(t, err)

	// The old symbol_id was replaced. Memory was snapshotted before delete.
	// Query by last_known_symbol to find the now-orphaned memory.
	orphans, err := store.GetOrphanedMemoriesBySymbol(ctx, db, "Greet", "greet.go")
	require.NoError(t, err)

	// After Phase 7 orphan recovery the memory should be re-linked to the new symbol.
	// If relinked, it appears under the new symbol ID and is no longer orphaned.
	if len(orphans) == 0 {
		// Memory was relinked to new symbol — verify it exists there.
		mems2, err := store.GetMemoriesBySymbolID(ctx, db, newSym.ID)
		require.NoError(t, err)
		require.NotEmpty(t, mems2, "memory should have been relinked to the new symbol")
	} else {
		// Still orphaned (symbol qualified name changed) — verify stale flag.
		assert.True(t, orphans[0].IsStale)
		assert.Equal(t, "hash_changed", orphans[0].StaleReason)
	}
}

// TestMemoryOrphanSurvivesDeletion verifies:
//  1. Index a file with a function.
//  2. Attach a memory to that function.
//  3. Delete the file from disk.
//  4. Re-index.
//  5. Memory must survive as an orphan (symbol_id IS NULL, is_stale=true).
func TestMemoryOrphanSurvivesDeletion(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := setupTestDB(t)
	idx := buildTestIndexer(t, db)

	projectRoot := t.TempDir()

	// --- Step 1: Index a file with one function. ---
	src := `package main

func Handler() {}
`
	writeGoFile(t, projectRoot, "handler.go", src)

	_, err := idx.IndexRepo(ctx, "repo", projectRoot)
	require.NoError(t, err)

	sym, _, err := store.GetSymbolWithDependencies(ctx, db, "repo", "Handler", 0)
	require.NoError(t, err, "Handler symbol must exist after indexing")

	// --- Step 2: Attach a memory. ---
	memID, err := store.SaveMemory(ctx, db, &models.Memory{
		SymbolID:        &sym.ID,
		Note:            "Handler is the HTTP entry point.",
		Author:          "developer",
		LastKnownSymbol: sym.QualifiedName,
		LastKnownFile:   sym.FilePath,
	})
	require.NoError(t, err)
	assert.Greater(t, memID, int64(0))

	// --- Step 3: Delete the file from disk. ---
	require.NoError(t, os.Remove(filepath.Join(projectRoot, "handler.go")))

	// --- Step 4: Re-index. ---
	stats, err := idx.IndexRepo(ctx, "repo", projectRoot)
	require.NoError(t, err)
	assert.Equal(t, 1, stats.FilesDeleted, "one file should be detected as deleted")

	// --- Step 5: Memory must survive as orphan with stale flag. ---
	orphans, err := store.GetOrphanedMemoriesBySymbol(ctx, db, "Handler", "handler.go")
	require.NoError(t, err)
	require.Len(t, orphans, 1, "orphaned memory must survive symbol deletion")
	assert.True(t, orphans[0].IsStale, "orphaned memory must be marked stale")
	assert.Equal(t, "symbol_deleted", orphans[0].StaleReason)
	assert.Equal(t, "Handler", orphans[0].LastKnownSymbol)
	assert.Equal(t, "handler.go", orphans[0].LastKnownFile)
	assert.Equal(t, "Handler is the HTTP entry point.", orphans[0].Note, "note content must survive")
}
