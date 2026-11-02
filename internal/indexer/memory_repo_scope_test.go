package indexer

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/context-link-mcp/context-link/internal/store"
	"github.com/context-link-mcp/context-link/pkg/models"
)

// TestRelinkOrphanedMemories_ScopedToRepo indexes two repos with identical
// file paths into one DB. An orphaned memory from repoA must not attach to
// repoB's same-named symbol, and must come back to repoA once its symbol does.
func TestRelinkOrphanedMemories_ScopedToRepo(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := setupTestDB(t)
	idx := buildTestIndexer(t, db)

	src := "package main\n\nfunc Greet() string {\n\treturn \"hello\"\n}\n"
	rootA, rootB := t.TempDir(), t.TempDir()
	writeGoFile(t, rootA, "greet.go", src)
	writeGoFile(t, rootB, "greet.go", src)

	_, err := idx.IndexRepo(ctx, "repoA", rootA)
	require.NoError(t, err)
	_, err = idx.IndexRepo(ctx, "repoB", rootB)
	require.NoError(t, err)

	symA, err := store.GetSymbolByName(ctx, db, "repoA", "Greet")
	require.NoError(t, err)
	_, err = store.SaveMemory(ctx, db, &models.Memory{
		SymbolID: &symA.ID, Note: "repoA note", Author: "agent",
		LastKnownSymbol: symA.QualifiedName, LastKnownFile: symA.FilePath,
	})
	require.NoError(t, err)

	// Orphan repoA's memory by deleting its file.
	require.NoError(t, os.Remove(filepath.Join(rootA, "greet.go")))
	stats, err := idx.IndexRepo(ctx, "repoA", rootA)
	require.NoError(t, err)
	require.Equal(t, 1, stats.MemoriesOrphaned)

	// Re-index repoB with a changed greet.go: its Greet is re-inserted.
	writeGoFile(t, rootB, "greet.go", "package main\n\nfunc Greet() string {\n\treturn \"hi\"\n}\n")
	stats, err = idx.IndexRepo(ctx, "repoB", rootB)
	require.NoError(t, err)
	assert.Zero(t, stats.MemoriesRelinked, "repoA's orphan must not relink to repoB's symbol")

	symB, err := store.GetSymbolByName(ctx, db, "repoB", "Greet")
	require.NoError(t, err)
	memsB, err := store.GetMemoriesBySymbolID(ctx, db, symB.ID)
	require.NoError(t, err)
	assert.Empty(t, memsB)

	// Restore repoA's file: the orphan relinks to repoA's new symbol.
	writeGoFile(t, rootA, "greet.go", src)
	stats, err = idx.IndexRepo(ctx, "repoA", rootA)
	require.NoError(t, err)
	assert.Equal(t, 1, stats.MemoriesRelinked)

	symA, err = store.GetSymbolByName(ctx, db, "repoA", "Greet")
	require.NoError(t, err)
	memsA, err := store.GetMemoriesBySymbolID(ctx, db, symA.ID)
	require.NoError(t, err)
	require.Len(t, memsA, 1)
	assert.Equal(t, "repoA note", memsA[0].Note)
	assert.False(t, memsA[0].IsStale)
}
