package indexer

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/context-link-mcp/context-link/internal/vectorstore"
)

// TestIndexRepo_ForceReindexClearsDimensionMismatch covers M11: after
// switching embedders, a --force run re-embedded everything but the recorded
// dimension stayed at the old value, so the mismatch warning never cleared.
func TestIndexRepo_ForceReindexClearsDimensionMismatch(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	root := t.TempDir()
	db := setupTestDB(t)
	registry := newPythonRegistry(t)
	require.NoError(t, os.WriteFile(filepath.Join(root, "a.py"), []byte("def alpha():\n    return 1\n\ndef beta():\n    return 2\n"), 0o644))

	old := vectorstore.NewMockEmbedder(384)
	require.NoError(t, vectorstore.EnsureEmbeddingDimension(ctx, db, old.Dim()))
	_, err := NewIndexer(registry, db, 1, old).IndexRepo(ctx, "repo", root)
	require.NoError(t, err)

	cur := vectorstore.NewMockEmbedder(128)
	require.ErrorIs(t, vectorstore.EnsureEmbeddingDimension(ctx, db, cur.Dim()), vectorstore.ErrDimensionMismatch,
		"precondition: switching embedders is a mismatch")

	// An incremental run re-embeds nothing (files unchanged): still a mismatch.
	_, err = NewIndexer(registry, db, 1, cur).IndexRepo(ctx, "repo", root)
	require.NoError(t, err)
	assert.ErrorIs(t, vectorstore.EnsureEmbeddingDimension(ctx, db, cur.Dim()), vectorstore.ErrDimensionMismatch)

	// --force replaces every vector: the mismatch clears.
	stats, err := NewIndexer(registry, db, 1, cur).WithForce(true).IndexRepo(ctx, "repo", root)
	require.NoError(t, err)
	require.Equal(t, 2, stats.EmbeddingsGenerated)
	assert.NoError(t, vectorstore.EnsureEmbeddingDimension(ctx, db, cur.Dim()))

	var dim string
	require.NoError(t, db.QueryRowContext(ctx, `SELECT value FROM vec_meta WHERE key = 'embedding_dim'`).Scan(&dim))
	assert.Equal(t, "128", dim)
}
