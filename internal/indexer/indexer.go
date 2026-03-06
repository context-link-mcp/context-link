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
	FilesDiscovered    int           `json:"files_discovered"`
	FilesIndexed       int           `json:"files_indexed"`
	FilesSkipped       int           `json:"files_skipped"`
	FilesUnchanged     int           `json:"files_unchanged"`
	SymbolsExtracted   int           `json:"symbols_extracted"`
	DepsExtracted      int           `json:"deps_extracted"`
	DepsResolved       int           `json:"deps_resolved"`
	EmbeddingsGenerated int          `json:"embeddings_generated"`
	Duration           time.Duration `json:"duration"`
	Errors             int           `json:"errors"`
}

// Indexer orchestrates the full indexing pipeline: walk → parse → extract → store → embed.
type Indexer struct {
	registry  *LanguageRegistry
	pools     *ParserPoolManager
	extractor *Extractor
	db        *store.DB
	embedder  vectorstore.Embedder // nil = skip embedding generation
	workers   int
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

	// Phase 2: Determine which files need re-indexing.
	filesToIndex, unchanged, err := idx.filterChangedFiles(ctx, result.Files, repoName)
	if err != nil {
		return nil, fmt.Errorf("indexer: failed to filter changed files: %w", err)
	}
	stats.FilesUnchanged = unchanged
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

	stats.FilesSkipped = stats.FilesDiscovered - stats.FilesIndexed - stats.FilesUnchanged
	stats.Duration = time.Since(start)

	slog.Info("indexer: indexing complete",
		"repo", repoName,
		"files_indexed", stats.FilesIndexed,
		"symbols", stats.SymbolsExtracted,
		"deps_resolved", stats.DepsResolved,
		"duration", stats.Duration,
	)

	return stats, nil
}

// filterChangedFiles compares discovered files against the DB to find
// which files are new or changed.
func (idx *Indexer) filterChangedFiles(
	ctx context.Context,
	files []DiscoveredFile,
	repoName string,
) (changed []DiscoveredFile, unchanged int, err error) {
	for _, f := range files {
		dbFile, err := store.GetFileByPath(ctx, idx.db, repoName, f.RelPath)
		if err != nil {
			// File not in DB — new file.
			changed = append(changed, f)
			continue
		}

		if dbFile.ContentHash == f.ContentHash {
			unchanged++
			continue
		}

		// Hash changed — needs re-indexing.
		changed = append(changed, f)
	}
	return changed, unchanged, nil
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
