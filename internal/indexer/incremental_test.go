package indexer

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/context-link-mcp/context-link/internal/indexer/adapters"
	"github.com/context-link-mcp/context-link/internal/store"
	"github.com/context-link-mcp/context-link/internal/vectorstore"
)

// cancellingEmbedder cancels the indexing run the first time it is asked to
// embed, simulating Ctrl-C or a timeout after symbols have been stored.
type cancellingEmbedder struct {
	vectorstore.Embedder
	cancel context.CancelFunc
}

func (e *cancellingEmbedder) EmbedBatch(ctx context.Context, _ []string) ([][]float32, error) {
	e.cancel()
	return nil, context.Canceled
}

func newPythonRegistry(t *testing.T) *LanguageRegistry {
	t.Helper()
	registry := NewLanguageRegistry()
	require.NoError(t, registry.Register(adapters.NewPythonAdapter()))
	return registry
}

func countFTSRows(t *testing.T, db *store.DB, repo string) int {
	t.Helper()
	var n int
	require.NoError(t, db.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM fts_symbols WHERE repo_name = ?`, repo).Scan(&n))
	return n
}

// TestIndexRepo_CancelledRunIsRetried guards against an interrupted run being
// reported as success and leaving files whose hash claims they are indexed.
func TestIndexRepo_CancelledRunIsRetried(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	registry := newPythonRegistry(t)
	root := t.TempDir()
	writeGoFile(t, root, "app.py", "def alpha():\n    return 1\n\n\ndef beta():\n    return alpha()\n")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	interrupted := &cancellingEmbedder{Embedder: vectorstore.NewMockEmbedder(0), cancel: cancel}
	_, err := NewIndexer(registry, db, 1, interrupted).IndexRepo(ctx, "repo", root)
	require.Error(t, err, "a cancelled run must not report success")
	assert.ErrorIs(t, err, context.Canceled)

	hashes, err := store.GetFileHashIndex(context.Background(), db, "repo")
	require.NoError(t, err)
	assert.Empty(t, hashes["app.py"], "the hash is only committed once the file is fully indexed")

	stats, err := NewIndexer(registry, db, 1, vectorstore.NewMockEmbedder(0)).IndexRepo(context.Background(), "repo", root)
	require.NoError(t, err)
	assert.Equal(t, 1, stats.FilesIndexed, "the next incremental run re-indexes the file")
	assert.Zero(t, stats.FilesUnchanged)
	assert.Equal(t, 1, stats.DepsResolved, "beta -> alpha")
	assert.Equal(t, 2, countFTSRows(t, db, "repo"), "FTS rows are rebuilt")

	stats, err = NewIndexer(registry, db, 1, vectorstore.NewMockEmbedder(0)).IndexRepo(context.Background(), "repo", root)
	require.NoError(t, err)
	assert.Equal(t, 1, stats.FilesUnchanged, "a completed run makes the next one incremental")
}

// TestIndexRepo_FailedStoreKeepsPreviousIndex guards against a DB error in the
// middle of replacing a file leaving it with zero symbols and a fresh hash.
func TestIndexRepo_FailedStoreKeepsPreviousIndex(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := setupTestDB(t)
	registry := newPythonRegistry(t)
	root := t.TempDir()
	idx := NewIndexer(registry, db, 1, nil)

	writeGoFile(t, root, "app.py", "def alpha():\n    return 1\n")
	_, err := idx.IndexRepo(ctx, "repo", root)
	require.NoError(t, err)

	// Change the file, and make inserting its new symbol fail.
	writeGoFile(t, root, "app.py", "def alpha():\n    return 1\n\n\ndef boom():\n    return 2\n")
	_, err = db.ExecContext(ctx, `CREATE TRIGGER fail_boom BEFORE INSERT ON symbols
		WHEN NEW.name = 'boom' BEGIN SELECT RAISE(ABORT, 'injected failure'); END`)
	require.NoError(t, err)

	stats, err := idx.IndexRepo(ctx, "repo", root)
	require.NoError(t, err)
	assert.Equal(t, 1, stats.Errors, "the failed file is reported")
	require.Len(t, stats.FailedFiles, 1)
	assert.Equal(t, "app.py", stats.FailedFiles[0].Path)

	_, err = store.GetSymbolByName(ctx, db, "repo", "alpha")
	assert.NoError(t, err, "the failed replace is rolled back, keeping the previous symbols")

	_, err = db.ExecContext(ctx, `DROP TRIGGER fail_boom`)
	require.NoError(t, err)

	stats, err = idx.IndexRepo(ctx, "repo", root)
	require.NoError(t, err)
	assert.Equal(t, 1, stats.FilesIndexed, "the file is retried, not treated as unchanged")
	_, err = store.GetSymbolByName(ctx, db, "repo", "boom")
	assert.NoError(t, err)
	_, err = store.GetSymbolByName(ctx, db, "repo", "alpha")
	assert.NoError(t, err)
}
