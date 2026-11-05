package indexer

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/context-link-mcp/context-link/internal/indexer/adapters"
	"github.com/context-link-mcp/context-link/internal/store"
	"github.com/context-link-mcp/context-link/pkg/models"
)

// TestRelinkOrphanedMemories_RenamedSymbol: a memory on a pre-012 name
// (Kotlin method `find`, now `Repo.find`) relinks after the 012 re-index by
// bare name within its file, when exactly one symbol there has that name. On
// master it stayed orphaned: only the qualified name was tried.
func TestRelinkOrphanedMemories_RenamedSymbol(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		src        string
		oldName    string // the qualified name the symbol had before 012
		renamed    string // its qualified name now
		wantLinkTo string // "" = stays orphaned
	}{
		{
			name:    "unique bare name",
			src:     "class Repo {\n    fun find(id: Int): Int = id\n}\n",
			oldName: "find", renamed: "Repo.find", wantLinkTo: "Repo.find",
		},
		{
			name:    "two symbols of that name",
			src:     "class A {\n    fun find(): Int = 1\n}\n\nclass B {\n    fun find(): Int = 2\n}\n",
			oldName: "find", renamed: "A.find",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			db := setupTestDB(t)
			registry := NewLanguageRegistry()
			require.NoError(t, registry.Register(adapters.NewKotlinAdapter()))
			idx := NewIndexer(registry, db, 1, nil)
			root := t.TempDir()
			writeGoFile(t, root, "Repo.kt", tc.src)
			_, err := idx.IndexRepo(ctx, "repo", root)
			require.NoError(t, err)

			// Make the index look pre-012: the method has its bare name, and a
			// memory is linked to it under that name.
			res, err := db.ExecContext(ctx,
				`UPDATE symbols SET qualified_name = ? WHERE repo_name = 'repo' AND qualified_name = ?`,
				tc.oldName, tc.renamed)
			require.NoError(t, err)
			n, err := res.RowsAffected()
			require.NoError(t, err)
			require.EqualValues(t, 1, n)
			old, err := store.GetSymbolByName(ctx, db, "repo", tc.oldName)
			require.NoError(t, err)
			memID, err := store.SaveMemory(ctx, db, &models.Memory{
				SymbolID: &old.ID, RepoName: "repo", Note: "pre-012 note", Author: "agent",
				LastKnownSymbol: tc.oldName, LastKnownFile: "Repo.kt",
			})
			require.NoError(t, err)

			// Migration 012 blanks every hash, so the next index re-indexes.
			_, err = db.ExecContext(ctx, `UPDATE files SET content_hash = ''`)
			require.NoError(t, err)
			stats, err := idx.IndexRepo(ctx, "repo", root)
			require.NoError(t, err)
			assert.Equal(t, 1, stats.MemoriesOrphaned)

			var symbolID *int64
			require.NoError(t, db.QueryRowContext(ctx, `SELECT symbol_id FROM memories WHERE id = ?`, memID).Scan(&symbolID))
			if tc.wantLinkTo == "" {
				assert.Zero(t, stats.MemoriesRelinked)
				assert.Nil(t, symbolID, "an ambiguous bare name relinks nothing")
				return
			}
			assert.Equal(t, 1, stats.MemoriesRelinked)
			require.NotNil(t, symbolID)
			var wantID int64
			require.NoError(t, db.QueryRowContext(ctx,
				`SELECT id FROM symbols WHERE repo_name = 'repo' AND qualified_name = ?`, tc.wantLinkTo).Scan(&wantID))
			assert.Equal(t, wantID, *symbolID)
		})
	}
}

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
