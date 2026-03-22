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

// skipDirs are directories that should never be watched.
var skipDirs = map[string]bool{
	"node_modules": true,
	".git":         true,
	"dist":         true,
	"build":        true,
	"vendor":       true,
	"__pycache__":  true,
	"bin":          true,
	".next":        true,
	".nuxt":        true,
	"coverage":     true,
	"target":       true, // Rust/Maven
	".idea":        true,
	".vscode":      true,
}

// Watcher watches a project directory for file changes and triggers
// incremental re-indexing via the existing indexer pipeline.
type Watcher struct {
	registry *indexer.LanguageRegistry
	db       *store.DB
	embedder vectorstore.Embedder
	repoRoot string
	repoName string
	workers  int
	debounce time.Duration

	mu      sync.Mutex
	pending map[string]struct{}
	timer   *time.Timer
}

// New creates a new Watcher configured for the given project.
func New(registry *indexer.LanguageRegistry, db *store.DB, embedder vectorstore.Embedder, repoRoot, repoName string, workers int) *Watcher {
	return &Watcher{
		registry: registry,
		db:       db,
		embedder: embedder,
		repoRoot: repoRoot,
		repoName: repoName,
		workers:  workers,
		debounce: 500 * time.Millisecond,
		pending:  make(map[string]struct{}),
	}
}

// Watch starts watching the project directory for file changes.
// It blocks until ctx is cancelled or an unrecoverable error occurs.
func (w *Watcher) Watch(ctx context.Context) error {
	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer fsw.Close()

	// Recursively add directories.
	if err := w.addDirs(fsw, w.repoRoot); err != nil {
		return err
	}

	slog.Info("file watcher started", "root", w.repoRoot)

	for {
		select {
		case <-ctx.Done():
			w.mu.Lock()
			if w.timer != nil {
				w.timer.Stop()
			}
			w.mu.Unlock()
			slog.Info("file watcher stopped")
			return nil

		case event, ok := <-fsw.Events:
			if !ok {
				return nil
			}
			if !w.isRelevantEvent(event) {
				continue
			}

			// If a new directory is created, watch it.
			if event.Has(fsnotify.Create) {
				if info, err := os.Stat(event.Name); err == nil && info.IsDir() {
					base := filepath.Base(event.Name)
					if !skipDirs[base] {
						_ = w.addDirs(fsw, event.Name)
					}
				}
			}

			w.enqueue(ctx, event.Name)

		case err, ok := <-fsw.Errors:
			if !ok {
				return nil
			}
			slog.Warn("file watcher error", "error", err)
		}
	}
}

// addDirs recursively adds directories to the fsnotify watcher,
// skipping directories in the skipDirs set.
func (w *Watcher) addDirs(fsw *fsnotify.Watcher, root string) error {
	return filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // skip inaccessible directories
		}
		if !d.IsDir() {
			return nil
		}
		base := filepath.Base(path)
		if skipDirs[base] && path != root {
			return filepath.SkipDir
		}
		return fsw.Add(path)
	})
}

// isRelevantEvent returns true if the file change should trigger re-indexing.
func (w *Watcher) isRelevantEvent(event fsnotify.Event) bool {
	if !event.Has(fsnotify.Create) && !event.Has(fsnotify.Write) && !event.Has(fsnotify.Remove) {
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

	w.pending[path] = struct{}{}

	if w.timer != nil {
		w.timer.Stop()
	}
	w.timer = time.AfterFunc(w.debounce, func() {
		w.flush(ctx)
	})
}

// flush performs a full incremental re-index. The existing indexer pipeline
// skips unchanged files via content hash, making this fast for small changes.
func (w *Watcher) flush(ctx context.Context) {
	w.mu.Lock()
	count := len(w.pending)
	w.pending = make(map[string]struct{})
	w.mu.Unlock()

	if count == 0 {
		return
	}

	slog.Info("re-indexing after file changes", "changed_files", count)

	idx := indexer.NewIndexer(w.registry, w.db, w.workers, w.embedder)
	stats, err := idx.IndexRepo(ctx, w.repoName, w.repoRoot)
	if err != nil {
		slog.Error("re-index failed", "error", err)
		return
	}

	slog.Info("re-index complete",
		"files_indexed", stats.FilesIndexed,
		"files_unchanged", stats.FilesUnchanged,
		"symbols_extracted", stats.SymbolsExtracted,
		"duration", stats.Duration,
	)
}
