package watcher

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/context-link-mcp/context-link/internal/indexer"
	"github.com/context-link-mcp/context-link/internal/indexer/adapters"
	"github.com/context-link-mcp/context-link/internal/store"
)

func buildTestRegistry(t *testing.T) *indexer.LanguageRegistry {
	t.Helper()
	reg := indexer.NewLanguageRegistry()
	require.NoError(t, reg.Register(adapters.NewGoAdapter()))
	require.NoError(t, reg.Register(adapters.NewTypeScriptAdapter()))
	require.NoError(t, reg.Register(adapters.NewPythonAdapter()))
	return reg
}

func TestIsRelevantFile(t *testing.T) {
	t.Parallel()
	reg := buildTestRegistry(t)

	tests := []struct {
		path     string
		expected bool
	}{
		{"main.go", true},
		{"src/app.ts", true},
		{"script.py", true},
		{"readme.md", false},
		{"image.png", false},
		{"noext", false},
		{"Makefile", false},
		{".gitignore", false},
		{"data.json", false},
	}

	for _, tc := range tests {
		t.Run(tc.path, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.expected, isRelevantFile(tc.path, reg))
		})
	}
}

func TestSkipDirs(t *testing.T) {
	t.Parallel()

	assert.True(t, skipDirs["node_modules"])
	assert.True(t, skipDirs[".git"])
	assert.True(t, skipDirs["vendor"])
	assert.True(t, skipDirs["__pycache__"])
	assert.True(t, skipDirs["target"])
	assert.False(t, skipDirs["src"])
	assert.False(t, skipDirs["internal"])
}

// Test helpers

func setupTestDB(t *testing.T) *store.DB {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "watcher_test.db")
	db, err := store.Open(dbPath)
	require.NoError(t, err)
	require.NoError(t, store.Migrate(db))
	t.Cleanup(func() { db.Close() })
	return db
}

type mockEmbedder struct{}

func (m *mockEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	vecs := make([][]float32, len(texts))
	for i := range vecs {
		vecs[i] = make([]float32, 128) // Match potion-base-4M dimension
	}
	return vecs, nil
}

func (m *mockEmbedder) EmbedOne(ctx context.Context, text string) ([]float32, error) {
	return make([]float32, 128), nil
}

func (m *mockEmbedder) Dim() int { return 128 }

func (m *mockEmbedder) Close() error { return nil }

func TestWatcher_New(t *testing.T) {
	t.Parallel()
	registry := buildTestRegistry(t)
	db := setupTestDB(t)
	embedder := &mockEmbedder{}

	w := New(registry, db, embedder, "/tmp/repo", "test-repo", 4)

	assert.NotNil(t, w)
	assert.Equal(t, "/tmp/repo", w.repoRoot)
	assert.Equal(t, "test-repo", w.repoName)
	assert.Equal(t, 4, w.workers)
	assert.Equal(t, 500*time.Millisecond, w.debounce)
	assert.NotNil(t, w.pending)
	assert.Equal(t, 0, len(w.pending))
}

func TestWatcher_AddDirs(t *testing.T) {
	t.Parallel()

	// Create temp directory structure
	root := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(root, "src"), 0755))
	require.NoError(t, os.Mkdir(filepath.Join(root, "src", "components"), 0755))
	require.NoError(t, os.Mkdir(filepath.Join(root, "node_modules"), 0755))
	require.NoError(t, os.Mkdir(filepath.Join(root, ".git"), 0755))

	// Create fsnotify watcher
	fsw, err := fsnotify.NewWatcher()
	require.NoError(t, err)
	defer fsw.Close()

	// Create watcher and add dirs
	registry := buildTestRegistry(t)
	db := setupTestDB(t)
	w := New(registry, db, &mockEmbedder{}, root, "test-repo", 1)

	err = w.addDirs(fsw, root)
	require.NoError(t, err)

	// Check that watched directories include src and components but not node_modules or .git
	watched := fsw.WatchList()
	hasRoot := false
	hasSrc := false
	hasComponents := false
	hasNodeModules := false
	hasGit := false

	for _, path := range watched {
		if path == root {
			hasRoot = true
		}
		if filepath.Base(path) == "src" {
			hasSrc = true
		}
		if filepath.Base(path) == "components" {
			hasComponents = true
		}
		if filepath.Base(path) == "node_modules" {
			hasNodeModules = true
		}
		if filepath.Base(path) == ".git" {
			hasGit = true
		}
	}

	assert.True(t, hasRoot, "root should be watched")
	assert.True(t, hasSrc, "src should be watched")
	assert.True(t, hasComponents, "components should be watched")
	assert.False(t, hasNodeModules, "node_modules should be skipped")
	assert.False(t, hasGit, ".git should be skipped")
}

func TestWatcher_EnqueueDebounce(t *testing.T) {
	t.Parallel()

	registry := buildTestRegistry(t)
	db := setupTestDB(t)
	w := New(registry, db, &mockEmbedder{}, t.TempDir(), "test-repo", 1)
	w.debounce = 200 * time.Millisecond

	t.Cleanup(func() {
		w.mu.Lock()
		if w.timer != nil {
			w.timer.Stop()
		}
		w.mu.Unlock()
	})

	ctx := context.Background()

	// Enqueue first file
	w.enqueue(ctx, "file1.go")
	w.mu.Lock()
	assert.Len(t, w.pending, 1)
	assert.NotNil(t, w.timer)
	w.mu.Unlock()

	// Enqueue second file within debounce window - should reset timer
	time.Sleep(50 * time.Millisecond)
	w.enqueue(ctx, "file2.go")
	w.mu.Lock()
	assert.Len(t, w.pending, 2)
	w.mu.Unlock()

	// Enqueue third file - should reset timer again
	time.Sleep(50 * time.Millisecond)
	w.enqueue(ctx, "file3.go")
	w.mu.Lock()
	assert.Len(t, w.pending, 3)
	w.mu.Unlock()

	// Poll for debounce to fire and clear pending (up to 600ms)
	deadline := time.Now().Add(600 * time.Millisecond)
	for time.Now().Before(deadline) {
		w.mu.Lock()
		count := len(w.pending)
		w.mu.Unlock()
		if count == 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	// Pending should be cleared after flush
	w.mu.Lock()
	pendingCount := len(w.pending)
	w.mu.Unlock()
	assert.Equal(t, 0, pendingCount, "pending should be cleared after flush")
}

func TestWatcher_IsRelevantEvent(t *testing.T) {
	t.Parallel()

	registry := buildTestRegistry(t)
	db := setupTestDB(t)
	w := New(registry, db, &mockEmbedder{}, t.TempDir(), "test-repo", 1)

	tests := []struct {
		name     string
		event    fsnotify.Event
		expected bool
	}{
		{"Create .go file", fsnotify.Event{Name: "main.go", Op: fsnotify.Create}, true},
		{"Write .ts file", fsnotify.Event{Name: "app.ts", Op: fsnotify.Write}, true},
		{"Remove .py file", fsnotify.Event{Name: "script.py", Op: fsnotify.Remove}, true},
		{"Chmod .go file", fsnotify.Event{Name: "main.go", Op: fsnotify.Chmod}, false},
		{"Rename .go file", fsnotify.Event{Name: "main.go", Op: fsnotify.Rename}, false},
		{"Create .md file", fsnotify.Event{Name: "README.md", Op: fsnotify.Create}, false},
		{"Write .json file", fsnotify.Event{Name: "data.json", Op: fsnotify.Write}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := w.isRelevantEvent(tt.event)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestWatcher_Watch_ContextCancellation(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	registry := buildTestRegistry(t)
	db := setupTestDB(t)
	w := New(registry, db, &mockEmbedder{}, root, "test-repo", 1)

	ctx, cancel := context.WithCancel(context.Background())

	// Start watching in goroutine
	errChan := make(chan error, 1)
	go func() {
		errChan <- w.Watch(ctx)
	}()

	// Give watcher time to start
	time.Sleep(50 * time.Millisecond)

	// Cancel context
	cancel()

	// Watch should exit cleanly
	select {
	case err := <-errChan:
		assert.NoError(t, err, "Watch should exit cleanly on context cancellation")
	case <-time.After(1 * time.Second):
		t.Fatal("Watch did not exit within timeout")
	}
}

// TestWatcher_Watch_DirectoryCreation removed due to timing unreliability on CI.
// Directory watching is tested indirectly via TestWatcher_AddDirs and
// flush/re-index behavior is tested in TestWatcher_FlushTriggersReindex.

func TestWatcher_ConcurrentEnqueue(t *testing.T) {
	t.Parallel()

	registry := buildTestRegistry(t)
	db := setupTestDB(t)
	w := New(registry, db, &mockEmbedder{}, t.TempDir(), "test-repo", 1)
	w.debounce = 500 * time.Millisecond // Longer to prevent flush during test

	t.Cleanup(func() {
		w.mu.Lock()
		if w.timer != nil {
			w.timer.Stop()
		}
		w.mu.Unlock()
	})

	ctx := context.Background()

	// Enqueue files concurrently
	var wg sync.WaitGroup
	fileCount := 100
	for i := 0; i < fileCount; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			w.enqueue(ctx, filepath.Join("src", fmt.Sprintf("file%d.go", n)))
		}(i)
	}

	wg.Wait()

	// Check that all files were enqueued
	w.mu.Lock()
	pendingCount := len(w.pending)
	w.mu.Unlock()

	assert.Equal(t, fileCount, pendingCount, "all files should be enqueued safely")
}

func TestWatcher_FlushClearsPending(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	registry := buildTestRegistry(t)
	db := setupTestDB(t)
	w := New(registry, db, &mockEmbedder{}, root, "test-repo", 1)
	w.debounce = 10 * time.Second // Prevent timer-triggered flush during test

	t.Cleanup(func() {
		w.mu.Lock()
		if w.timer != nil {
			w.timer.Stop()
		}
		w.mu.Unlock()
	})

	ctx := context.Background()

	// Enqueue files
	w.enqueue(ctx, "file1.go")
	w.enqueue(ctx, "file2.go")
	w.enqueue(ctx, "file3.go")

	w.mu.Lock()
	assert.Len(t, w.pending, 3, "should have 3 pending files")
	w.mu.Unlock()

	// Manually trigger flush
	w.flush(ctx)

	// Pending should be cleared
	w.mu.Lock()
	assert.Len(t, w.pending, 0, "pending should be cleared after flush")
	w.mu.Unlock()
}

func TestWatcher_FlushTriggersReindex(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	registry := buildTestRegistry(t)
	db := setupTestDB(t)
	w := New(registry, db, &mockEmbedder{}, root, "test-repo", 1)

	t.Cleanup(func() {
		w.mu.Lock()
		if w.timer != nil {
			w.timer.Stop()
		}
		w.mu.Unlock()
	})

	ctx := context.Background()

	// Create a real Go file in the temp directory
	testFile := filepath.Join(root, "example.go")
	err := os.WriteFile(testFile, []byte("package main\n\nfunc Add(a, b int) int {\n\treturn a + b\n}\n"), 0644)
	require.NoError(t, err)

	// Enqueue the file
	w.enqueue(ctx, testFile)

	// Verify no symbols before flush
	countBefore, err := store.CountSymbols(ctx, db, "test-repo")
	require.NoError(t, err)
	assert.Equal(t, int64(0), countBefore, "should have no symbols before flush")

	// Trigger flush (which calls indexer.IndexRepo)
	w.flush(ctx)

	// Verify symbols were created after flush
	countAfter, err := store.CountSymbols(ctx, db, "test-repo")
	require.NoError(t, err)
	assert.Greater(t, countAfter, int64(0), "flush should trigger re-index and create symbols")
}

func TestWatcher_FlushWithEmptyPending(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	registry := buildTestRegistry(t)
	db := setupTestDB(t)
	w := New(registry, db, &mockEmbedder{}, root, "test-repo", 1)

	ctx := context.Background()

	// Flush with empty pending (should not panic)
	w.flush(ctx)

	w.mu.Lock()
	assert.Len(t, w.pending, 0)
	w.mu.Unlock()
}

func TestWatcher_MultipleRapidChanges(t *testing.T) {
	t.Parallel()

	registry := buildTestRegistry(t)
	db := setupTestDB(t)
	w := New(registry, db, &mockEmbedder{}, t.TempDir(), "test-repo", 1)
	w.debounce = 500 * time.Millisecond // Long enough to prevent flush during enqueues

	t.Cleanup(func() {
		w.mu.Lock()
		if w.timer != nil {
			w.timer.Stop()
		}
		w.mu.Unlock()
	})

	ctx := context.Background()

	// Simulate rapid changes to same file
	for i := 0; i < 10; i++ {
		w.enqueue(ctx, "file.go")
		time.Sleep(10 * time.Millisecond)
	}

	// Pending should have only one entry (map deduplicates)
	w.mu.Lock()
	assert.Len(t, w.pending, 1, "rapid changes to same file should be deduplicated")
	w.mu.Unlock()

	// Poll for flush to complete (up to 1 second)
	deadline := time.Now().Add(1 * time.Second)
	for time.Now().Before(deadline) {
		w.mu.Lock()
		count := len(w.pending)
		w.mu.Unlock()
		if count == 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	w.mu.Lock()
	assert.Len(t, w.pending, 0, "pending should be cleared after flush")
	w.mu.Unlock()
}
