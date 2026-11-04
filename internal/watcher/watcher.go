// Package watcher provides file system watching for automatic re-indexing.
// It uses fsnotify to detect file changes and triggers incremental re-indexing
// with debouncing to batch rapid changes.
package watcher

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"

	"github.com/context-link-mcp/context-link/internal/indexer"
	"github.com/context-link-mcp/context-link/internal/store"
	"github.com/context-link-mcp/context-link/internal/vectorstore"
)

// Watcher watches a project directory for file changes and triggers
// incremental re-indexing via the existing indexer pipeline.
//
// It watches the directories the indexer walks (see indexer.DirFilter),
// including directories created after Watch started, and treats renames like
// removals, so files moved out of or within the tree are re-indexed.
type Watcher struct {
	registry *indexer.LanguageRegistry
	idx      *indexer.Indexer
	filter   *indexer.DirFilter
	repoRoot string
	repoName string
	workers  int
	debounce time.Duration

	// onIndexed, if non-nil, runs after every re-index attempt.
	onIndexed func()
	// onReady, if non-nil, runs once the initial directories are watched
	// (tests use it to know when changes will be seen).
	onReady func()

	// watched is the set of directories added to fsnotify. Only the Watch
	// goroutine (or a test calling addDirs directly) touches it.
	watched map[string]bool

	mu      sync.Mutex
	pending map[string]struct{}
	timer   *time.Timer
	stopped bool           // set when Watch returns; no flush starts after it
	flushes sync.WaitGroup // flushes in progress, waited for by Watch
}

// New creates a new Watcher configured for the given project. It re-indexes
// with its own Indexer; serve passes its shared one with WithIndexer so the
// watcher's runs and reindex_project calls are serialised.
func New(registry *indexer.LanguageRegistry, db *store.DB, embedder vectorstore.Embedder, repoRoot, repoName string, workers int) *Watcher {
	return &Watcher{
		registry: registry,
		idx:      indexer.NewIndexer(registry, db, workers, embedder),
		filter:   indexer.NewDirFilter(repoRoot),
		repoRoot: repoRoot,
		repoName: repoName,
		workers:  workers,
		debounce: 500 * time.Millisecond,
		watched:  make(map[string]bool),
		pending:  make(map[string]struct{}),
	}
}

// WithIndexer makes the watcher re-index through idx, which must index the
// same database. Index runs on one Indexer never overlap, so sharing it with
// other callers (reindex_project) serialises their runs. It must be set
// before Watch is called. Returns w for chaining.
func (w *Watcher) WithIndexer(idx *indexer.Indexer) *Watcher {
	w.idx = idx
	return w
}

// WithOnIndexed sets a hook that runs after every re-index attempt the
// watcher makes (success or failure), e.g. to drop caches derived from the
// index. It must be set before Watch is called. Returns w for chaining.
func (w *Watcher) WithOnIndexed(fn func()) *Watcher {
	w.onIndexed = fn
	return w
}

// Watch starts watching the project directory for file changes.
// It blocks until ctx is cancelled or an unrecoverable error occurs. Before
// it returns, it cancels the pending debounce and waits for a re-index in
// progress, so the caller may close the embedder and database afterwards.
func (w *Watcher) Watch(ctx context.Context) error {
	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer fsw.Close()
	defer w.stop()

	// Recursively add directories.
	if err := w.addDirs(fsw, w.repoRoot); err != nil {
		return err
	}

	slog.Info("file watcher started", "root", w.repoRoot)
	if w.onReady != nil {
		w.onReady()
	}

	for {
		select {
		case <-ctx.Done():
			slog.Info("file watcher stopped")
			return nil

		case event, ok := <-fsw.Events:
			if !ok {
				return nil
			}
			w.handleEvent(ctx, fsw, event)

		case err, ok := <-fsw.Errors:
			if !ok {
				return nil
			}
			slog.Warn("file watcher error", "error", err)
		}
	}
}

// stop cancels the pending debounce, prevents new flushes and waits for the
// one in progress.
func (w *Watcher) stop() {
	w.mu.Lock()
	w.stopped = true
	if w.timer != nil {
		w.timer.Stop()
	}
	w.mu.Unlock()
	w.flushes.Wait()
}

// handleEvent updates the watch list for created, removed and renamed
// directories and queues a re-index for every change that can affect the
// index.
func (w *Watcher) handleEvent(ctx context.Context, fsw *fsnotify.Watcher, event fsnotify.Event) {
	path := filepath.Clean(event.Name)

	// A new directory (created, or moved in under a new name) is watched with
	// its subdirectories. Files may already exist in it, so it is re-indexed
	// too. This runs before the extension filter: directories rarely have a
	// source-file extension.
	if event.Has(fsnotify.Create) {
		if info, err := os.Stat(path); err == nil && info.IsDir() {
			if w.filter.Skip(path) {
				return
			}
			if err := w.addDirs(fsw, path); err != nil {
				slog.Warn("file watcher: failed to watch new directory", "dir", path, "error", err)
			}
			w.enqueue(ctx, path)
			return
		}
	}

	// A watched directory that was removed or renamed away: its files are
	// gone from their indexed paths.
	if (event.Has(fsnotify.Remove) || event.Has(fsnotify.Rename)) && w.watched[path] {
		w.unwatch(fsw, path)
		w.enqueue(ctx, path)
		return
	}

	if w.isRelevantEvent(event) {
		w.enqueue(ctx, path)
	}
}

// addDirs recursively adds directories to the fsnotify watcher, skipping the
// directories the indexer's walk skips.
func (w *Watcher) addDirs(fsw *fsnotify.Watcher, root string) error {
	return filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // skip inaccessible directories
		}
		if !d.IsDir() {
			return nil
		}
		if w.filter.Skip(path) {
			return filepath.SkipDir
		}
		if err := fsw.Add(path); err != nil {
			return err
		}
		w.watched[filepath.Clean(path)] = true
		return nil
	})
}

// unwatch forgets dir and every watched directory below it. fsnotify may
// already have dropped the watches of a removed directory, so errors from
// Remove are ignored.
func (w *Watcher) unwatch(fsw *fsnotify.Watcher, dir string) {
	prefix := dir + string(filepath.Separator)
	for p := range w.watched {
		if p == dir || strings.HasPrefix(p, prefix) {
			_ = fsw.Remove(p)
			delete(w.watched, p)
		}
	}
}

// isRelevantEvent returns true if the file change should trigger re-indexing.
// A rename reports the old name (the new one arrives as a Create), so it is
// handled like a removal.
func (w *Watcher) isRelevantEvent(event fsnotify.Event) bool {
	if !event.Has(fsnotify.Create) && !event.Has(fsnotify.Write) &&
		!event.Has(fsnotify.Remove) && !event.Has(fsnotify.Rename) {
		return false
	}
	return isRelevantFile(event.Name, w.registry)
}

// isRelevantFile returns true if the file has an extension registered in the language registry.
func isRelevantFile(path string, registry *indexer.LanguageRegistry) bool {
	ext := strings.ToLower(filepath.Ext(path))
	if ext == "" {
		return false
	}
	_, ok := registry.GetAdapter(ext)
	return ok
}

// enqueue adds a changed file path to the pending set and resets the debounce timer.
func (w *Watcher) enqueue(ctx context.Context, path string) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.stopped {
		return
	}
	w.pending[path] = struct{}{}

	if w.timer != nil {
		w.timer.Stop()
	}
	w.timer = time.AfterFunc(w.debounce, func() {
		w.startFlush(ctx)
	})
}

// startFlush runs flush unless the watcher has stopped, tracking it so that
// stop can wait for it.
func (w *Watcher) startFlush(ctx context.Context) {
	w.mu.Lock()
	if w.stopped {
		w.mu.Unlock()
		return
	}
	w.flushes.Add(1)
	w.mu.Unlock()
	defer w.flushes.Done()
	w.flush(ctx)
}

// flush performs a full incremental re-index. The existing indexer pipeline
// skips unchanged files via content hash, making this fast for small changes.
// If another index run holds the Indexer, flush waits for it, so changes
// that arrive during a run are indexed by the next one, never dropped.
func (w *Watcher) flush(ctx context.Context) {
	w.mu.Lock()
	count := len(w.pending)
	w.pending = make(map[string]struct{})
	w.mu.Unlock()

	if count == 0 {
		return
	}

	slog.Info("re-indexing after file changes", "changed_files", count)

	stats, err := w.idx.IndexRepo(ctx, w.repoName, w.repoRoot)
	if w.onIndexed != nil {
		w.onIndexed()
	}
	if err != nil {
		slog.Error("re-index failed", "error", err)
		return
	}

	slog.Info("re-index complete",
		"files_indexed", stats.FilesIndexed,
		"files_unchanged", stats.FilesUnchanged,
		"files_deleted", stats.FilesDeleted,
		"symbols_extracted", stats.SymbolsExtracted,
		"duration", stats.Duration,
	)
}
