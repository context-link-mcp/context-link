package indexer

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/context-link-mcp/context-link/internal/store"
	"github.com/context-link-mcp/context-link/internal/vectorstore"
)

// overlapEmbedder records how many EmbedBatch calls run at the same time.
// A call waits up to hold for a second, concurrent call before it returns,
// so two overlapping index runs are always observed together inside it.
type overlapEmbedder struct {
	vectorstore.Embedder
	hold time.Duration

	mu       sync.Mutex
	inFlight int
	maxSeen  int
	overlap  chan struct{} // closed once two calls are in flight
	once     sync.Once
}

func newOverlapEmbedder(hold time.Duration) *overlapEmbedder {
	return &overlapEmbedder{
		Embedder: vectorstore.NewMockEmbedder(0),
		hold:     hold,
		overlap:  make(chan struct{}),
	}
}

func (e *overlapEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	e.mu.Lock()
	e.inFlight++
	e.maxSeen = max(e.maxSeen, e.inFlight)
	if e.inFlight >= 2 {
		e.once.Do(func() { close(e.overlap) })
	}
	e.mu.Unlock()

	select {
	case <-e.overlap:
	case <-time.After(e.hold):
	}

	e.mu.Lock()
	e.inFlight--
	e.mu.Unlock()
	return e.Embedder.EmbedBatch(ctx, texts)
}

// TestIndexRepo_SerialisesConcurrentRuns is the M7 regression: runs on one
// Indexer (watcher flushes, reindex_project calls) must not overlap. On the
// unserialised indexer both runs embed at the same time (maxSeen 2) and the
// race detector reports the write to idx.repoRoot.
func TestIndexRepo_SerialisesConcurrentRuns(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	root := t.TempDir()
	writeGoFile(t, root, "app.py", "def alpha():\n    return 1\n\n\ndef beta():\n    return alpha()\n")
	writeGoFile(t, root, "lib.py", "def gamma():\n    return 2\n")

	emb := newOverlapEmbedder(time.Second)
	idx := NewIndexer(newPythonRegistry(t), db, 2, emb)

	const runs = 3
	var wg sync.WaitGroup
	errs := make([]error, runs)
	stats := make([]*IndexStats, runs)
	for i := range runs {
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
	assert.Equal(t, 1, emb.maxSeen, "index runs must not overlap")
	assert.Equal(t, 2, indexed, "only the first run indexes; later ones find every file unchanged")

	n, err := store.CountSymbols(context.Background(), db, "repo")
	require.NoError(t, err)
	assert.Equal(t, int64(3), n, "no duplicate symbols")
	assert.Equal(t, 3, countFTSRows(t, db, "repo"))
}

// blockingEmbedder signals when an index run reaches embedding and holds it
// there until release is closed.
type blockingEmbedder struct {
	vectorstore.Embedder
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func newBlockingEmbedder() *blockingEmbedder {
	return &blockingEmbedder{
		Embedder: vectorstore.NewMockEmbedder(0),
		entered:  make(chan struct{}),
		release:  make(chan struct{}),
	}
}

func (e *blockingEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	e.once.Do(func() { close(e.entered) })
	<-e.release
	return e.Embedder.EmbedBatch(ctx, texts)
}

// startBlockedRun starts an index run on idx and waits until it is inside
// the embedder. The returned channel yields the run's error.
func startBlockedRun(t *testing.T, idx *Indexer, emb *blockingEmbedder, root string) <-chan error {
	t.Helper()
	done := make(chan error, 1)
	go func() {
		_, err := idx.IndexRepo(context.Background(), "repo", root)
		done <- err
	}()
	select {
	case <-emb.entered:
	case err := <-done:
		t.Fatalf("index run ended before embedding: %v", err)
	case <-time.After(30 * time.Second):
		t.Fatal("index run never reached the embedder")
	}
	return done
}

// TestIndexer_DrainWaitsForRunningIndex guards serve's shutdown order: the
// embedder and DB may only be closed after the in-flight run has finished,
// and no run may start afterwards.
func TestIndexer_DrainWaitsForRunningIndex(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	root := t.TempDir()
	writeGoFile(t, root, "app.py", "def alpha():\n    return 1\n")

	emb := newBlockingEmbedder()
	idx := NewIndexer(newPythonRegistry(t), db, 1, emb)
	runDone := startBlockedRun(t, idx, emb, root)

	drained := make(chan struct{})
	go func() {
		idx.Drain()
		close(drained)
	}()

	select {
	case <-drained:
		t.Fatal("Drain returned while an index run was still embedding")
	case <-time.After(100 * time.Millisecond):
	}

	close(emb.release)
	require.NoError(t, <-runDone)
	select {
	case <-drained:
	case <-time.After(30 * time.Second):
		t.Fatal("Drain did not return after the run finished")
	}

	_, err := idx.IndexRepo(context.Background(), "repo", root)
	assert.ErrorIs(t, err, ErrIndexerClosed)
}

// TestIndexRepo_WaitingRunHonoursContext checks that a run queued behind a
// long one gives up when its context ends (e.g. the reindex_project timeout).
func TestIndexRepo_WaitingRunHonoursContext(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	root := t.TempDir()
	writeGoFile(t, root, "app.py", "def alpha():\n    return 1\n")

	emb := newBlockingEmbedder()
	idx := NewIndexer(newPythonRegistry(t), db, 1, emb)
	runDone := startBlockedRun(t, idx, emb, root)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := idx.IndexRepo(ctx, "repo", root)
	assert.ErrorIs(t, err, context.DeadlineExceeded)

	close(emb.release)
	require.NoError(t, <-runDone)
}
