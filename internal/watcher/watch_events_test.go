package watcher

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/context-link-mcp/context-link/internal/store"
	"github.com/context-link-mcp/context-link/internal/vectorstore"
)

// eventDeadline bounds every wait for fsnotify-driven re-indexing. Events on
// Windows can take a while to arrive under load; the tests poll, so a passing
// run finishes as soon as the index is updated.
const eventDeadline = 20 * time.Second

// runningWatcher is a Watcher started on a temp repo by startWatcher.
type runningWatcher struct {
	w       *Watcher
	db      *store.DB
	root    string
	indexed atomic.Int64 // re-index attempts so far
	cancel  context.CancelFunc
	done    chan error
}

// startWatcher indexes root once, then starts watching it with a short
// debounce and returns when the initial directories are watched.
func startWatcher(t *testing.T, root string, embedder vectorstore.Embedder) *runningWatcher {
	t.Helper()
	db := setupTestDB(t)
	rw := &runningWatcher{db: db, root: root, done: make(chan error, 1)}
	rw.w = New(buildTestRegistry(t), db, embedder, root, "test-repo", 1).
		WithOnIndexed(func() { rw.indexed.Add(1) })
	rw.w.debounce = 50 * time.Millisecond

	_, err := rw.w.idx.IndexRepo(context.Background(), "test-repo", root)
	require.NoError(t, err)

	ready := make(chan struct{})
	rw.w.onReady = func() { close(ready) }
	ctx, cancel := context.WithCancel(context.Background())
	rw.cancel = cancel
	go func() { rw.done <- rw.w.Watch(ctx) }()
	t.Cleanup(func() {
		cancel()
		<-rw.done
	})

	select {
	case <-ready:
	case err := <-rw.done:
		t.Fatalf("Watch returned before it was ready: %v", err)
	case <-time.After(eventDeadline):
		t.Fatal("watcher never became ready")
	}
	return rw
}

// indexedFiles returns the repo-relative paths in the index.
func (rw *runningWatcher) indexedFiles(t *testing.T) []string {
	t.Helper()
	hashes, err := store.GetFileHashIndex(context.Background(), rw.db, "test-repo")
	require.NoError(t, err)
	out := make([]string, 0, len(hashes))
	for p := range hashes {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// waitForFiles polls until the index holds exactly want.
func (rw *runningWatcher) waitForFiles(t *testing.T, want ...string) {
	t.Helper()
	sort.Strings(want)
	if want == nil {
		want = []string{}
	}
	deadline := time.Now().Add(eventDeadline)
	var got []string
	for time.Now().Before(deadline) {
		got = rw.indexedFiles(t)
		if assert.ObjectsAreEqual(want, got) {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("index never reached %v; last seen %v", want, got)
}

// waitForIndexRuns polls until at least n re-index attempts happened.
func (rw *runningWatcher) waitForIndexRuns(t *testing.T, n int64) {
	t.Helper()
	deadline := time.Now().Add(eventDeadline)
	for time.Now().Before(deadline) {
		if rw.indexed.Load() >= n {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("expected %d re-index runs, saw %d", n, rw.indexed.Load())
}

const goSource = "package p\n\nfunc F() int { return 1 }\n"

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
}

// TestWatcher_WatchesDirectoryCreatedAfterStart is the M8 regression for
// new directories: the Create event of a directory has no source extension,
// so the old extension filter dropped it before the directory was watched,
// and files written into it later were never indexed.
func TestWatcher_WatchesDirectoryCreatedAfterStart(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	rw := startWatcher(t, root, &mockEmbedder{})

	require.NoError(t, os.Mkdir(filepath.Join(root, "pkg"), 0o755))
	// The new directory itself triggers a re-index (it may already hold
	// files); once that ran, the directory is watched.
	rw.waitForIndexRuns(t, 1)

	writeFile(t, filepath.Join(root, "pkg", "a.go"), goSource)
	rw.waitForFiles(t, "pkg/a.go")

	require.NoError(t, os.Mkdir(filepath.Join(root, "pkg", "sub"), 0o755))
	rw.waitForIndexRuns(t, 3)
	writeFile(t, filepath.Join(root, "pkg", "sub", "b.go"), goSource)
	rw.waitForFiles(t, "pkg/a.go", "pkg/sub/b.go")
}

// TestWatcher_DirectoryMovedInIsIndexed covers a directory tree that appears
// with files already in it (git checkout, mv, unzip): no file events arrive
// for its contents, only the directory's Create.
func TestWatcher_DirectoryMovedInIsIndexed(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	staging := t.TempDir()
	writeFile(t, filepath.Join(staging, "tree", "deep", "c.go"), goSource)
	rw := startWatcher(t, root, &mockEmbedder{})

	require.NoError(t, os.Rename(filepath.Join(staging, "tree"), filepath.Join(root, "tree")))
	rw.waitForFiles(t, "tree/deep/c.go")
}

// TestWatcher_RenamedFileIsReindexed is the M8 regression for renames: the
// old name only gets a Rename event, which was ignored, so the file stayed
// indexed under a path that no longer exists.
func TestWatcher_RenamedFileIsReindexed(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "a.go"), goSource)
	rw := startWatcher(t, root, &mockEmbedder{})
	require.Equal(t, []string{"a.go"}, rw.indexedFiles(t))

	// Renamed to a name the indexer ignores: only the Rename event matters.
	require.NoError(t, os.Rename(filepath.Join(root, "a.go"), filepath.Join(root, "a.go.bak")))
	rw.waitForFiles(t)
}

// TestWatcher_RenamedDirectoryIsReindexed checks that renaming a watched
// directory drops the old paths and indexes (and watches) the new ones.
func TestWatcher_RenamedDirectoryIsReindexed(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "old", "a.go"), goSource)
	rw := startWatcher(t, root, &mockEmbedder{})
	require.Equal(t, []string{"old/a.go"}, rw.indexedFiles(t))

	require.NoError(t, os.Rename(filepath.Join(root, "old"), filepath.Join(root, "new")))
	rw.waitForFiles(t, "new/a.go")

	runs := rw.indexed.Load()
	writeFile(t, filepath.Join(root, "new", "b.go"), goSource)
	rw.waitForIndexRuns(t, runs+1)
	rw.waitForFiles(t, "new/a.go", "new/b.go")
}

// TestWatcher_IgnoredNewDirectoryIsNotIndexed checks that a directory the
// walker skips is not indexed when it appears.
func TestWatcher_IgnoredNewDirectoryIsNotIndexed(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeFile(t, filepath.Join(root, ".gitignore"), "generated/\n")
	rw := startWatcher(t, root, &mockEmbedder{})

	writeFile(t, filepath.Join(root, "node_modules", "dep", "x.go"), goSource)
	writeFile(t, filepath.Join(root, "generated", "y.go"), goSource)
	// A later, relevant change proves the earlier ones were seen and skipped.
	writeFile(t, filepath.Join(root, "main.go"), goSource)
	rw.waitForFiles(t, "main.go")
}

// gateEmbedder blocks EmbedBatch until release is closed and reports when
// the first call arrives.
type gateEmbedder struct {
	mockEmbedder
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (e *gateEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	e.once.Do(func() { close(e.entered) })
	<-e.release
	return e.mockEmbedder.EmbedBatch(ctx, texts)
}

// TestWatcher_StopWaitsForRunningReindex guards serve's shutdown order: Watch
// must not return while a re-index is still using the embedder, because
// serve closes the embedder and database right after.
func TestWatcher_StopWaitsForRunningReindex(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	// The initial index of the empty root embeds nothing.
	emb := &gateEmbedder{entered: make(chan struct{}), release: make(chan struct{})}
	rw := startWatcher(t, root, emb)

	writeFile(t, filepath.Join(root, "a.go"), goSource)
	select {
	case <-emb.entered:
	case <-time.After(eventDeadline):
		t.Fatal("the watcher never re-indexed")
	}

	rw.cancel()
	select {
	case <-rw.done:
		t.Fatal("Watch returned while a re-index was still embedding")
	case <-time.After(100 * time.Millisecond):
	}

	close(emb.release)
	select {
	case err := <-rw.done:
		require.NoError(t, err)
		rw.done <- err // for the cleanup
	case <-time.After(eventDeadline):
		t.Fatal("Watch did not return after the re-index finished")
	}
}
