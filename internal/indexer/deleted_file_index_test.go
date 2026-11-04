package indexer

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/context-link-mcp/context-link/internal/store"
	"github.com/context-link-mcp/context-link/internal/vectorstore"
)

// TestIndexRepo_DeletedFileLeavesNoGhostHits deletes an indexed file and
// checks that neither FTS nor vector search still returns its symbols (M6):
// ghost hits used up search slots after the symbols were gone.
func TestIndexRepo_DeletedFileLeavesNoGhostHits(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	root := t.TempDir()
	db := setupTestDB(t)
	registry := newPythonRegistry(t)
	embedder := vectorstore.NewMockEmbedder(0)

	require.NoError(t, os.WriteFile(filepath.Join(root, "keep.py"), []byte("def kept_helper():\n    return 1\n"), 0o644))
	gone := filepath.Join(root, "gone.py")
	require.NoError(t, os.WriteFile(gone, []byte("def zebra_ghost():\n    return 2\n"), 0o644))

	_, err := NewIndexer(registry, db, 1, embedder).IndexRepo(ctx, "repo", root)
	require.NoError(t, err)

	hits, err := store.FTSSearch(ctx, db, "repo", "zebra_ghost", 10)
	require.NoError(t, err)
	require.Len(t, hits, 1, "precondition: the symbol is searchable before the delete")

	// A cache loaded before the delete, as a running server holds.
	cache := vectorstore.NewVectorCache("repo")
	query, err := embedder.EmbedBatch(ctx, []string{"zebra_ghost"})
	require.NoError(t, err)
	before, err := vectorstore.KNNSearchCached(ctx, db, cache, query[0], 10, -1)
	require.NoError(t, err)
	require.Len(t, before, 2)

	require.NoError(t, os.Remove(gone))
	stats, err := NewIndexer(registry, db, 1, embedder).IndexRepo(ctx, "repo", root)
	require.NoError(t, err)
	require.Equal(t, 1, stats.FilesDeleted)

	hits, err = store.FTSSearch(ctx, db, "repo", "zebra_ghost", 10)
	require.NoError(t, err)
	assert.Empty(t, hits, "FTS rows of a deleted file must be removed")
	assert.Equal(t, 1, countFTSRows(t, db, "repo"), "only kept_helper stays in FTS")

	live := func(results []vectorstore.SearchResult) {
		t.Helper()
		ids := make([]int64, 0, len(results))
		for _, r := range results {
			ids = append(ids, r.SymbolID)
		}
		syms, err := store.GetSymbolsByIDs(ctx, db, "repo", ids)
		require.NoError(t, err)
		assert.Len(t, syms, len(ids), "every vector hit must be a live symbol")
		for _, s := range syms {
			assert.Equal(t, "kept_helper", s.Name)
		}
	}
	direct, err := vectorstore.KNNSearch(ctx, db, "repo", query[0], 10, -1)
	require.NoError(t, err)
	assert.Len(t, direct, 1, "embeddings of a deleted file must be removed")
	live(direct)

	// The server drops its cache after every index run (Server.NotifyIndexed).
	cache.Invalidate()
	after, err := vectorstore.KNNSearchCached(ctx, db, cache, query[0], 10, -1)
	require.NoError(t, err)
	assert.Len(t, after, 1)
	live(after)

	var files int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM files WHERE repo_name = 'repo'`).Scan(&files))
	assert.Equal(t, 1, files)
}
