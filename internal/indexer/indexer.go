package indexer

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/context-link/context-link/internal/store"
	"github.com/context-link/context-link/internal/vectorstore"
	"github.com/context-link/context-link/pkg/models"
)

// IndexStats reports the results of an indexing run.
type IndexStats struct {
	FilesDiscovered     int           `json:"files_discovered"`
	FilesIndexed        int           `json:"files_indexed"`
	FilesSkipped        int           `json:"files_skipped"`
	FilesUnchanged      int           `json:"files_unchanged"`
	FilesDeleted        int           `json:"files_deleted"`
	SymbolsExtracted    int           `json:"symbols_extracted"`
	DepsExtracted       int           `json:"deps_extracted"`
	DepsResolved        int           `json:"deps_resolved"`
	EmbeddingsGenerated int           `json:"embeddings_generated"`
	MemoriesOrphaned    int           `json:"memories_orphaned"`
	MemoriesRelinked    int           `json:"memories_relinked"`
	Duration            time.Duration `json:"duration"`
	Errors              int           `json:"errors"`
}

// Indexer orchestrates the full indexing pipeline: walk → parse → extract → store → embed.
type Indexer struct {
	registry  *LanguageRegistry
	pools     *ParserPoolManager
	extractor *Extractor
	db        *store.DB
	embedder  vectorstore.Embedder // nil = skip embedding generation
	workers   int
	force     bool // when true, bypass incremental hash check
	repoRoot  string
}

// NewIndexer creates a new Indexer with the given configuration.
// embedder may be nil — embedding generation will be skipped.
func NewIndexer(registry *LanguageRegistry, db *store.DB, workers int, embedder vectorstore.Embedder) *Indexer {
	if workers <= 0 {
		workers = 4
	}
	return &Indexer{
		registry:  registry,
		pools:     NewParserPoolManager(),
		extractor: NewExtractor(),
		db:        db,
		embedder:  embedder,
		workers:   workers,
	}
}

// WithForce returns the indexer configured to bypass incremental hashing (full re-index).
func (idx *Indexer) WithForce(force bool) *Indexer {
	idx.force = force
	return idx
}

// fileExtension returns the file extension including the dot.
func fileExtension(path string) string {
	return filepath.Ext(path)
}

// joinPath joins a root and relative path.
func joinPath(root, rel string) string {
	return filepath.Join(root, rel)
}

// IndexRepo indexes a repository at the given root path.
func (idx *Indexer) IndexRepo(ctx context.Context, repoName, repoRoot string) (*IndexStats, error) {
	start := time.Now()
	stats := &IndexStats{}

	idx.repoRoot = repoRoot

	slog.Info("indexer: starting indexing", "repo", repoName, "root", repoRoot)

	// Phase 1: Walk the directory tree.
	walker := NewWalker(idx.registry, repoRoot)
	result, err := walker.Walk(ctx)
	if err != nil {
		return nil, fmt.Errorf("indexer: walk failed: %w", err)
	}
	stats.FilesDiscovered = len(result.Files)
	slog.Info("indexer: walk complete", "files_discovered", stats.FilesDiscovered)

	// Phase 2: Determine which files need re-indexing and remove deleted files.
	filesToIndex, unchanged, deleted, orphaned, err := idx.filterChangedFiles(ctx, result.Files, repoName)
	if err != nil {
		return nil, fmt.Errorf("indexer: failed to filter changed files: %w", err)
	}
	stats.FilesUnchanged = unchanged
	stats.FilesDeleted = deleted
	stats.MemoriesOrphaned += orphaned
	slog.Info("indexer: incremental check complete",
		"to_index", len(filesToIndex),
		"unchanged", unchanged,
	)

	// Phase 3: Parse and extract symbols in parallel.
	type fileResult struct {
		file    DiscoveredFile
		symbols []models.Symbol
		deps    []ExtractedDep
	}

	var (
		mu      sync.Mutex
		results []fileResult
	)

	g, gCtx := errgroup.WithContext(ctx)
	g.SetLimit(idx.workers)

	for _, f := range filesToIndex {
		f := f // capture loop variable
		g.Go(func() error {
			symbols, deps, err := idx.processFile(gCtx, f, repoName)
			if err != nil {
				slog.Warn("indexer: failed to process file",
					"file", f.RelPath,
					"error", err,
				)
				mu.Lock()
				stats.Errors++
				mu.Unlock()
				return nil // don't abort on single file failure
			}

			mu.Lock()
			results = append(results, fileResult{file: f, symbols: symbols, deps: deps})
			mu.Unlock()
			return nil
		})
	}

	if err := g.Wait(); err != nil {
		return nil, fmt.Errorf("indexer: parallel processing failed: %w", err)
	}

	// Phase 4: Store results in database (single writer).
	for _, r := range results {
		if err := idx.storeFileResults(ctx, r.file, r.symbols, r.deps, repoName); err != nil {
			slog.Warn("indexer: failed to store results",
				"file", r.file.RelPath,
				"error", err,
			)
			stats.Errors++
			continue
		}
		stats.FilesIndexed++
		stats.SymbolsExtracted += len(r.symbols)
		stats.DepsExtracted += len(r.deps)
	}

	// Phase 5: Resolve cross-file dependencies.
	resolved, err := idx.resolveDependencies(ctx, repoName)
	if err != nil {
		slog.Warn("indexer: dependency resolution partially failed", "error", err)
	}
	stats.DepsResolved = resolved

	// Phase 6: Generate embeddings for all newly indexed symbols.
	if idx.embedder != nil {
		indexedPaths := make([]string, 0, len(results))
		for _, r := range results {
			indexedPaths = append(indexedPaths, r.file.RelPath)
		}
		stats.EmbeddingsGenerated = idx.generateEmbeddings(ctx, repoName, indexedPaths)
	}

	// Phase 7: Orphan recovery — re-link orphaned memories to newly inserted symbols.
	indexedPathsForRelink := make([]string, 0, len(results))
	for _, r := range results {
		indexedPathsForRelink = append(indexedPathsForRelink, r.file.RelPath)
	}
	relinked := idx.relinkOrphanedMemories(ctx, repoName, indexedPathsForRelink)
	stats.MemoriesRelinked = relinked

	stats.FilesSkipped = stats.FilesDiscovered - stats.FilesIndexed - stats.FilesUnchanged - stats.FilesDeleted
	if stats.FilesSkipped < 0 {
		stats.FilesSkipped = 0
	}
	stats.Duration = time.Since(start)

	slog.Info("indexer: indexing complete",
		"repo", repoName,
		"files_indexed", stats.FilesIndexed,
		"files_deleted", stats.FilesDeleted,
		"symbols", stats.SymbolsExtracted,
		"deps_resolved", stats.DepsResolved,
		"memories_orphaned", stats.MemoriesOrphaned,
		"memories_relinked", stats.MemoriesRelinked,
		"duration", stats.Duration,
	)

	return stats, nil
}

// filterChangedFiles compares discovered files against the DB to find which files
// are new, changed, or deleted. Deleted files have their symbols removed (memories
// become orphaned via ON DELETE SET NULL). Returns changed files, unchanged count,
// deleted count, and orphaned memory count.
func (idx *Indexer) filterChangedFiles(
	ctx context.Context,
	files []DiscoveredFile,
	repoName string,
) (changed []DiscoveredFile, unchanged int, deleted int, orphaned int, err error) {
	// Build a set of discovered paths for O(1) lookup.
	discovered := make(map[string]struct{}, len(files))
	for _, f := range files {
		discovered[f.RelPath] = struct{}{}
	}

	for _, f := range files {
		if idx.force {
			// Force mode: treat every file as changed.
			changed = append(changed, f)
			continue
		}

		dbFile, dbErr := store.GetFileByPath(ctx, idx.db, repoName, f.RelPath)
		if dbErr != nil {
			// File not in DB — new file.
			changed = append(changed, f)
			continue
		}

		if dbFile.ContentHash == f.ContentHash {
			unchanged++
			continue
		}

		// Hash changed — snapshot memories as stale before re-indexing.
		orphaned += idx.snapshotMemoriesForFile(ctx, repoName, f.RelPath, "hash_changed")
		changed = append(changed, f)
	}

	// Detect files in DB that no longer exist on disk.
	dbFiles, listErr := store.ListFiles(ctx, idx.db, repoName)
	if listErr != nil {
		return changed, unchanged, deleted, orphaned, fmt.Errorf("indexer: failed to list DB files: %w", listErr)
	}
	for _, dbFile := range dbFiles {
		if _, found := discovered[dbFile.Path]; found {
			continue
		}
		// File removed from disk — orphan its memories, then delete symbols + file record.
		orphaned += idx.snapshotMemoriesForFile(ctx, repoName, dbFile.Path, "symbol_deleted")
		if delErr := store.DeleteSymbolsByFile(ctx, idx.db, repoName, dbFile.Path); delErr != nil {
			slog.Warn("indexer: failed to delete symbols for removed file", "file", dbFile.Path, "error", delErr)
		}
		if delErr := store.DeleteFileByPath(ctx, idx.db, repoName, dbFile.Path); delErr != nil {
			slog.Warn("indexer: failed to delete file record", "file", dbFile.Path, "error", delErr)
		}
		deleted++
		slog.Info("indexer: removed deleted file", "file", dbFile.Path)
	}

	return changed, unchanged, deleted, orphaned, nil
}

// snapshotMemoriesForFile snapshots and marks stale all non-stale memories for
// symbols in the given file. Returns the exact count of memories newly staled.
func (idx *Indexer) snapshotMemoriesForFile(ctx context.Context, repoName, filePath, reason string) int {
	syms, err := store.GetSymbolsByFile(ctx, idx.db, repoName, filePath)
	if err != nil {
		slog.Warn("indexer: failed to get symbols for memory snapshot", "file", filePath, "error", err)
		return 0
	}
	total := 0
	for _, s := range syms {
		n, err := store.SnapshotAndMarkStale(ctx, idx.db, s.ID, s.QualifiedName, s.FilePath, reason)
		if err != nil {
			slog.Warn("indexer: failed to snapshot memories", "symbol", s.QualifiedName, "error", err)
			continue
		}
		// Explicitly null out symbol_id so memories are orphaned regardless of
		// whether the FK ON DELETE SET NULL cascade fires in the SQLite driver.
		if n > 0 {
			if err := store.DetachMemoriesFromSymbol(ctx, idx.db, s.ID); err != nil {
				slog.Warn("indexer: failed to detach memories from symbol", "symbol", s.QualifiedName, "error", err)
			}
		}
		total += n
	}
	return total
}

// processFile parses a single file and extracts symbols and dependencies.
func (idx *Indexer) processFile(
	ctx context.Context,
	file DiscoveredFile,
	repoName string,
) ([]models.Symbol, []ExtractedDep, error) {
	adapter, ok := idx.registry.GetAdapter(file.Extension)
	if !ok {
		return nil, nil, fmt.Errorf("indexer: no adapter for extension %s", file.Extension)
	}

	source, err := os.ReadFile(file.Path)
	if err != nil {
		return nil, nil, fmt.Errorf("indexer: failed to read %s: %w", file.Path, err)
	}

	pool := idx.pools.GetPool(adapter)
	tree, err := pool.Parse(ctx, source)
	if err != nil {
		return nil, nil, fmt.Errorf("indexer: failed to parse %s: %w", file.RelPath, err)
	}

	symbols, err := idx.extractor.ExtractSymbols(ctx, tree, source, adapter, repoName, file.RelPath)
	if err != nil {
		return nil, nil, fmt.Errorf("indexer: failed to extract symbols from %s: %w", file.RelPath, err)
	}

	deps, err := idx.extractor.ExtractDependencies(ctx, tree, source, adapter, file.RelPath)
	if err != nil {
		slog.Warn("indexer: failed to extract dependencies", "file", file.RelPath, "error", err)
		// Non-fatal: return symbols without deps.
		return symbols, nil, nil
	}

	return symbols, deps, nil
}

// storeFileResults persists the extracted symbols and raw dependencies for a file.
func (idx *Indexer) storeFileResults(
	ctx context.Context,
	file DiscoveredFile,
	symbols []models.Symbol,
	deps []ExtractedDep,
	repoName string,
) error {
	// Upsert file record.
	modelFile := models.File{
		RepoName:    repoName,
		Path:        file.RelPath,
		ContentHash: file.ContentHash,
		SizeBytes:   file.SizeBytes,
	}
	if err := store.UpsertFile(ctx, idx.db, &modelFile); err != nil {
		return fmt.Errorf("indexer: failed to upsert file %s: %w", file.RelPath, err)
	}

	// Delete existing symbols for this file (will be re-inserted).
	if err := store.DeleteSymbolsByFile(ctx, idx.db, repoName, file.RelPath); err != nil {
		return fmt.Errorf("indexer: failed to delete old symbols for %s: %w", file.RelPath, err)
	}

	// Batch insert symbols.
	if err := store.BatchInsertSymbols(ctx, idx.db, symbols); err != nil {
		return fmt.Errorf("indexer: failed to insert symbols for %s: %w", file.RelPath, err)
	}

	return nil
}

// resolveDependencies resolves raw call/extends/implements dependencies to
// symbol IDs by matching names against the symbol registry in the database.
func (idx *Indexer) resolveDependencies(ctx context.Context, repoName string) (int, error) {
	// Build a name → ID lookup from all symbols in this repo.
	symbolsByName, err := store.GetSymbolNameIndex(ctx, idx.db, repoName)
	if err != nil {
		return 0, fmt.Errorf("indexer: failed to build symbol name index: %w", err)
	}

	// Get all symbols to iterate their extracted dependencies.
	allSymbols, err := idx.getAllSymbolsWithDeps(ctx, repoName, symbolsByName)
	if err != nil {
		return 0, err
	}

	resolved := 0
	for _, dep := range allSymbols {
		if err := store.InsertDependency(ctx, idx.db, &dep); err != nil {
			slog.Warn("indexer: failed to insert resolved dependency", "error", err)
			continue
		}
		resolved++
	}

	return resolved, nil
}

// getAllSymbolsWithDeps re-parses all indexed files to extract dependencies
// and resolves them against the symbol name index.
func (idx *Indexer) getAllSymbolsWithDeps(ctx context.Context, repoName string, symbolsByName map[string]int64) ([]models.Dependency, error) {
	files, err := store.ListFiles(ctx, idx.db, repoName)
	if err != nil {
		return nil, fmt.Errorf("indexer: failed to list files: %w", err)
	}

	var allDeps []models.Dependency

	for _, f := range files {
		adapter, ok := idx.registry.GetAdapter(fileExtension(f.Path))
		if !ok {
			continue
		}

		source, err := os.ReadFile(joinPath(idx.repoRoot, f.Path))
		if err != nil {
			slog.Warn("indexer: failed to read file for dep resolution", "file", f.Path, "error", err)
			continue
		}

		pool := idx.pools.GetPool(adapter)
		tree, err := pool.Parse(ctx, source)
		if err != nil {
			continue
		}

		deps, err := idx.extractor.ExtractDependencies(ctx, tree, source, adapter, f.Path)
		if err != nil {
			continue
		}

		// Get caller symbols in this file.
		callerSymbols, err := store.GetSymbolsByFile(ctx, idx.db, repoName, f.Path)
		if err != nil {
			continue
		}

		// For each extracted dep, try to resolve callee and assign to first matching caller.
		for _, d := range deps {
			if d.Kind == "import" {
				continue // imports don't map to symbol-to-symbol edges
			}
			calleeID, ok := symbolsByName[d.Symbol]
			if !ok {
				continue
			}
			// Assign to the first caller symbol in this file (conservative).
			if len(callerSymbols) > 0 {
				allDeps = append(allDeps, models.Dependency{
					CallerID: callerSymbols[0].ID,
					CalleeID: calleeID,
					Kind:     d.Kind,
				})
			}
		}
	}

	return allDeps, nil
}

// relinkOrphanedMemories scans newly indexed symbols and re-links any orphaned
// memories that have matching last_known_symbol + last_known_file.
func (idx *Indexer) relinkOrphanedMemories(ctx context.Context, repoName string, filePaths []string) int {
	relinked := 0
	for _, path := range filePaths {
		syms, err := store.GetSymbolsByFile(ctx, idx.db, repoName, path)
		if err != nil {
			continue
		}
		for _, s := range syms {
			orphans, err := store.GetOrphanedMemoriesBySymbol(ctx, idx.db, s.QualifiedName, s.FilePath)
			if err != nil || len(orphans) == 0 {
				continue
			}
			for _, m := range orphans {
				if err := store.RelinkMemory(ctx, idx.db, m.ID, s.ID); err != nil {
					slog.Warn("indexer: failed to relink memory", "memory_id", m.ID, "symbol", s.QualifiedName, "error", err)
					continue
				}
				relinked++
				slog.Debug("indexer: relinked orphaned memory", "memory_id", m.ID, "symbol", s.QualifiedName)
			}
		}
	}
	return relinked
}

// generateEmbeddings generates and stores vector embeddings for all symbols
// belonging to the given file paths. Returns the count of embeddings stored.
func (idx *Indexer) generateEmbeddings(ctx context.Context, repoName string, filePaths []string) int {
	embedded := 0
	for _, path := range filePaths {
		syms, err := store.GetSymbolsByFile(ctx, idx.db, repoName, path)
		if err != nil {
			slog.Warn("indexer: failed to load symbols for embedding", "file", path, "error", err)
			continue
		}

		for i := 0; i < len(syms); i += vectorstore.DefaultBatchSize {
			end := i + vectorstore.DefaultBatchSize
			if end > len(syms) {
				end = len(syms)
			}
			batch := syms[i:end]

			texts := make([]string, len(batch))
			for j, s := range batch {
				texts[j] = vectorstore.SymbolEmbedText(s.Kind, s.QualifiedName, s.CodeBlock)
			}

			vecs, err := idx.embedder.EmbedBatch(ctx, texts)
			if err != nil {
				slog.Warn("indexer: embedding batch failed", "file", path, "error", err)
				continue
			}

			for j, s := range batch {
				if err := vectorstore.UpsertEmbedding(ctx, idx.db, s.ID, repoName, vecs[j]); err != nil {
					slog.Warn("indexer: failed to upsert embedding", "symbol", s.QualifiedName, "error", err)
					continue
				}
				embedded++
			}
		}

		if embedded > 0 && embedded%100 == 0 {
			slog.Info("indexer: embedding progress", "embeddings", embedded)
		}
	}
	slog.Info("indexer: embedding complete", "embeddings_generated", embedded)
	return embedded
}
