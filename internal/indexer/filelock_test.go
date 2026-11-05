package indexer

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/context-link-mcp/context-link/internal/store"
	"github.com/context-link-mcp/context-link/internal/store/filelock"
)

// openSharedDB opens the database file at path as a separate handle, the way
// a second process (index, extract, serve) would.
func openSharedDB(t *testing.T, path string) *store.DB {
	t.Helper()
	db, err := store.Open(path)
	require.NoError(t, err)
	require.NoError(t, store.Migrate(db))
	t.Cleanup(func() { db.Close() })
	return db
}

// TestIndexRepo_SerialisesIndexersOnSameDBFile is the B6 regression: two
// separate Indexers (standing in for two processes) on one database file
// must not run at the same time. Their per-instance semaphores don't see each
// other, so only the "<db>.lock" file lock serialises them; without it both
// runs embed at once (maxSeen 2).
func TestIndexRepo_SerialisesIndexersOnSameDBFile(t *testing.T) {
	t.Parallel()
	dbPath := filepath.Join(t.TempDir(), "shared.db")
	dbA := openSharedDB(t, dbPath)
	dbB := openSharedDB(t, dbPath)

	root := t.TempDir()
	writeGoFile(t, root, "app.py", "def alpha():\n    return 1\n\n\ndef beta():\n    return alpha()\n")
	writeGoFile(t, root, "lib.py", "def gamma():\n    return 2\n")

	emb := newOverlapEmbedder(time.Second)
	indexers := []*Indexer{
		NewIndexer(newPythonRegistry(t), dbA, 2, emb),
		NewIndexer(newPythonRegistry(t), dbB, 2, emb),
	}

	var wg sync.WaitGroup
	errs := make([]error, len(indexers))
	stats := make([]*IndexStats, len(indexers))
	for i, idx := range indexers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			stats[i], errs[i] = idx.IndexRepo(context.Background(), "repo", root)
		}()
	}
	wg.Wait()

	indexed := 0
	for i, err := range errs {
		require.NoError(t, err)
		indexed += stats[i].FilesIndexed
	}
	assert.Equal(t, 1, emb.maxSeen, "index runs on one DB file must not overlap")
	assert.Equal(t, 2, indexed, "only the first run indexes; the second finds every file unchanged")

	n, err := store.CountSymbols(context.Background(), dbA, "repo")
	require.NoError(t, err)
	assert.Equal(t, int64(3), n, "no duplicate symbols")

	l, err := filelock.TryLock(dbPath + ".lock")
	require.NoError(t, err, "the lock is released when the runs end")
	require.NoError(t, l.Unlock())
}

// TestIndexRepo_WaitsForLockHeldElsewhere checks that a run waits while
// another holder (another process) has the DB's lock file, then indexes.
func TestIndexRepo_WaitsForLockHeldElsewhere(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	root := t.TempDir()
	writeGoFile(t, root, "app.py", "def alpha():\n    return 1\n")

	held, err := filelock.TryLock(db.Path() + ".lock")
	require.NoError(t, err)

	idx := NewIndexer(newPythonRegistry(t), db, 1, nil)
	done := make(chan error, 1)
	go func() {
		_, err := idx.IndexRepo(context.Background(), "repo", root)
		done <- err
	}()

	select {
	case err := <-done:
		t.Fatalf("IndexRepo ran while the DB lock was held: %v", err)
	case <-time.After(400 * time.Millisecond):
	}
	n, err := store.CountSymbols(context.Background(), db, "repo")
	require.NoError(t, err)
	assert.Zero(t, n, "nothing is written while waiting")

	require.NoError(t, held.Unlock())
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(30 * time.Second):
		t.Fatal("IndexRepo did not run after the lock was released")
	}
	n, err = store.CountSymbols(context.Background(), db, "repo")
	require.NoError(t, err)
	assert.Equal(t, int64(1), n)
}

// TestIndexRepo_LockWaitHonoursContext checks that a run waiting on the DB
// lock gives up when its context ends (reindex_project timeout, Ctrl-C), and
// that the Indexer stays usable afterwards.
func TestIndexRepo_LockWaitHonoursContext(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	root := t.TempDir()
	writeGoFile(t, root, "app.py", "def alpha():\n    return 1\n")

	held, err := filelock.TryLock(db.Path() + ".lock")
	require.NoError(t, err)

	idx := NewIndexer(newPythonRegistry(t), db, 1, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err = idx.IndexRepo(ctx, "repo", root)
	require.ErrorIs(t, err, context.DeadlineExceeded)

	require.NoError(t, held.Unlock())
	_, err = idx.IndexRepo(context.Background(), "repo", root)
	require.NoError(t, err, "the in-process slot was released after the failed wait")
}

// TestLockDB_SkipsPathlessDatabases covers DBs with no file to lock beside.
func TestLockDB_SkipsPathlessDatabases(t *testing.T) {
	t.Parallel()
	for _, db := range []*store.DB{{}, nil} {
		idx := &Indexer{db: db}
		l, err := idx.lockDB(context.Background())
		require.NoError(t, err)
		assert.Nil(t, l)
		assert.NoError(t, l.Unlock())
	}
}
