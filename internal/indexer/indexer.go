package indexer

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/context-link-mcp/context-link/internal/store"
	"github.com/context-link-mcp/context-link/internal/vectorstore"
	"github.com/context-link-mcp/context-link/pkg/models"
)

// IndexStats reports the results of an indexing run.
type IndexStats struct {
	FilesDiscovered     int            `json:"files_discovered"`
	FilesIndexed        int            `json:"files_indexed"`
	FilesSkipped        int            `json:"files_skipped"`
	FilesUnchanged      int            `json:"files_unchanged"`
	FilesDeleted        int            `json:"files_deleted"`
	SymbolsExtracted    int            `json:"symbols_extracted"`
	DepsExtracted       int            `json:"deps_extracted"`
	DepsResolved        int            `json:"deps_resolved"`
	RoutesExtracted     int            `json:"routes_extracted"`
	EmbeddingsGenerated int            `json:"embeddings_generated"`
	MemoriesOrphaned    int            `json:"memories_orphaned"`
	MemoriesRelinked    int            `json:"memories_relinked"`
	Duration            time.Duration  `json:"duration"`
	Errors              int            `json:"errors"`
	FailedFiles         []IndexFailure `json:"failed_files,omitempty"`
}

// IndexFailure records a file that could not be parsed or stored.
type IndexFailure struct {
	Path  string `json:"path"`
	Error string `json:"error"`
}

// fileResult holds the parsed output for a single file from Phase 3.
type fileResult struct {
	file    DiscoveredFile
	symbols []models.Symbol
	deps    []ExtractedDep
	routes  []ExtractedRoute
}

// ErrIndexerClosed is returned by IndexRepo after Drain.
var ErrIndexerClosed = errors.New("indexer: closed")

// Indexer orchestrates the full indexing pipeline: walk → parse → extract → store → embed.
//
// Index runs on one Indexer are serialised: IndexRepo waits for a run in
// progress to finish before it starts. Everything that indexes a repo in one
// process (serve's reindex_project tool and its --watch watcher) must share
// one Indexer, because overlapping runs replace the same files' rows and race
// on the Indexer's fields.
type Indexer struct {
	registry  *LanguageRegistry
	pools     *ParserPoolManager
	extractor *Extractor
	db        *store.DB
	embedder  vectorstore.Embedder // nil = skip embedding generation
	workers   int
	force     bool // when true, bypass incremental hash check

	// run is a one-slot semaphore held for the duration of an index run: a
	// channel rather than a sync.Mutex so that a waiting run honours its ctx.
	run chan struct{}
	// closed and repoRoot are only accessed while holding run.
	closed   bool
	repoRoot string
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
		run:       make(chan struct{}, 1),
	}
}

// WithForce returns the indexer configured to bypass incremental hashing (full re-index).
func (idx *Indexer) WithForce(force bool) *Indexer {
	idx.force = force
	return idx
}

// acquire waits until no other index run holds idx, or ctx ends.
func (idx *Indexer) acquire(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("indexer: indexing interrupted: %w", err)
	}
	select {
	case idx.run <- struct{}{}:
	case <-ctx.Done():
		return fmt.Errorf("indexer: interrupted while waiting for the running index: %w", ctx.Err())
	}
	if idx.closed {
		<-idx.run
		return ErrIndexerClosed
	}
	return nil
}

// release ends the index run started by acquire.
func (idx *Indexer) release() { <-idx.run }

// Drain waits for the index run in progress, if any, and makes every later
// IndexRepo call fail with ErrIndexerClosed. Call it before closing the
// embedder or database the Indexer uses.
func (idx *Indexer) Drain() {
	idx.run <- struct{}{}
	idx.closed = true
	<-idx.run
}

// IndexRepo indexes a repository at the given root path. It first waits for
// an index run already in progress on idx (see Indexer); if ctx ends while it
// waits, it returns ctx's error without indexing.
func (idx *Indexer) IndexRepo(ctx context.Context, repoName, repoRoot string) (*IndexStats, error) {
	if err := idx.acquire(ctx); err != nil {
		return nil, err
	}
	defer idx.release()

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
	var (
		mu      sync.Mutex
		results []fileResult
	)

	g, gCtx := errgroup.WithContext(ctx)
	g.SetLimit(idx.workers)

	for _, f := range filesToIndex {
		f := f // capture loop variable
		g.Go(func() error {
			symbols, deps, routes, err := idx.processFile(gCtx, f, repoName)
			if err != nil {
				slog.Warn("indexer: failed to process file",
					"file", f.RelPath,
					"error", err,
				)
				mu.Lock()
				stats.Errors++
				stats.FailedFiles = append(stats.FailedFiles, IndexFailure{Path: f.RelPath, Error: err.Error()})
				mu.Unlock()
				return nil // don't abort on single file failure
			}

			mu.Lock()
			results = append(results, fileResult{file: f, symbols: symbols, deps: deps, routes: routes})
			mu.Unlock()
			return nil
		})
	}

	if err := g.Wait(); err != nil {
		return nil, fmt.Errorf("indexer: parallel processing failed: %w", err)
	}
	// A cancelled context surfaces above only as per-file errors; fail the run.
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("indexer: indexing interrupted: %w", err)
	}

	// Phase 4: Store results in database (single writer).
	// Re-inserted symbols get new IDs and edges are resolved by name, so the
	// files whose edges must be resolved again are marked first: callers of
	// names the changed files gain or lose (here), and callers with an edge
	// into a changed file (in the transaction that replaces it). The marks are
	// persisted, so Phase 5 of this run or of a later one restores those edges
	// even if this run stops in between. Each file is replaced atomically with
	// a pending (empty) hash; the real hashes are committed only after phases
	// 5-7, so an interrupted run is retried.
	newNames := make(map[string][]string, len(results))
	reindexed := make(map[string]bool, len(results))
	for _, r := range results {
		newNames[r.file.RelPath] = symbolNames(r.symbols)
		reindexed[r.file.RelPath] = true
	}
	if err := store.MarkNameCallers(ctx, idx.db, repoName, newNames, reindexed); err != nil {
		return nil, fmt.Errorf("indexer: failed to mark callers for re-resolution: %w", err)
	}
	indexedHashes := make(map[string]string, len(results))
	stored := make([]fileResult, 0, len(results))
	for _, r := range results {
		if err := idx.storeFileResults(ctx, r.file, r.symbols, r.deps, repoName); err != nil {
			slog.Warn("indexer: failed to store results",
				"file", r.file.RelPath,
				"error", err,
			)
			stats.Errors++
			stats.FailedFiles = append(stats.FailedFiles, IndexFailure{Path: r.file.RelPath, Error: err.Error()})
			continue
		}
		indexedHashes[r.file.RelPath] = r.file.ContentHash
		stored = append(stored, r)
		stats.FilesIndexed++
		stats.SymbolsExtracted += len(r.symbols)
		stats.DepsExtracted += len(r.deps)
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("indexer: indexing interrupted: %w", err)
	}

	// Pre-compute symbol map for phases 5-7 (single query instead of 3×N per-file lookups).
	// Phase 5 resolves against every symbol of the repo and replaces the edges
	// of the files it resolves, so an incomplete map would delete edges for
	// good. Fail the run instead: the hashes are not committed and the pending
	// marks stay, so the next run retries.
	symbolsByFile, err := store.GetSymbolsByRepo(ctx, idx.db, repoName)
	if err != nil {
		return nil, fmt.Errorf("indexer: failed to load symbols for resolution: %w", err)
	}

	// Store HTTP routes, resolving handler names to the symbol IDs just inserted.
	stats.RoutesExtracted = idx.storeRoutes(ctx, repoName, results, symbolsByFile)

	// Phase 5: Resolve the edges of the files indexed by this run (from their
	// already-extracted deps) and of every file marked for re-resolution.
	resolved, err := idx.resolveDependencies(ctx, repoName, stored, symbolsByFile)
	if err != nil {
		return nil, fmt.Errorf("indexer: dependency resolution failed: %w", err)
	}
	stats.DepsResolved = resolved

	// Phase 6: Generate embeddings for all newly indexed symbols.
	if idx.embedder != nil {
		indexedPaths := make([]string, 0, len(results))
		for _, r := range results {
			indexedPaths = append(indexedPaths, r.file.RelPath)
		}
		stats.EmbeddingsGenerated = idx.generateEmbeddings(ctx, repoName, indexedPaths, symbolsByFile)

		// Re-check the recorded embedding dimension: once a --force run has
		// replaced every vector, the mismatch from a previous embedder clears.
		if err := vectorstore.EnsureEmbeddingDimension(ctx, idx.db, idx.embedder.Dim()); err != nil {
			slog.Warn("indexer: embeddings of another dimension remain; searches skip them until a --force re-index",
				"repo", repoName, "error", err)
		}
	}

	// Phase 7: Orphan recovery — re-link orphaned memories to newly inserted symbols.
	indexedPathsForRelink := make([]string, 0, len(results))
	for _, r := range results {
		indexedPathsForRelink = append(indexedPathsForRelink, r.file.RelPath)
	}
	relinked := idx.relinkOrphanedMemories(ctx, repoName, indexedPathsForRelink, symbolsByFile)
	stats.MemoriesRelinked = relinked

	// Commit the content hashes last: until now the files indexed by this run
	// carry an empty hash, so an interrupted run re-indexes them next time.
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("indexer: indexing interrupted: %w", err)
	}
	if err := store.SetFileHashes(ctx, idx.db, repoName, indexedHashes); err != nil {
		return nil, fmt.Errorf("indexer: failed to commit file hashes: %w", err)
	}

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
// deleted count and orphaned memory count. Files that called into the deleted
// files are marked for re-resolution (see store.DeleteFileIndex).
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

	// Batch-load all known file hashes (single query instead of N per-file lookups).
	dbHashIndex, hashErr := store.GetFileHashIndex(ctx, idx.db, repoName)
	if hashErr != nil {
		return changed, unchanged, deleted, orphaned, fmt.Errorf("indexer: failed to load file hash index: %w", hashErr)
	}

	for _, f := range files {
		dbHash, exists := dbHashIndex[f.RelPath]
		if !exists {
			// File not in DB — new file.
			changed = append(changed, f)
			continue
		}

		if dbHash == f.ContentHash && !idx.force {
			unchanged++
			continue
		}

		// Hash changed, or force mode re-indexes every file: snapshot memories
		// as stale and detach them before the file's symbols are replaced, so
		// Phase 7 relinks them by name and file to the new symbol IDs.
		reason := "hash_changed"
		if dbHash == f.ContentHash {
			reason = "reindexed"
		}
		orphaned += idx.snapshotMemoriesForFile(ctx, repoName, f.RelPath, reason)
		changed = append(changed, f)
	}

	// Detect files in DB that no longer exist on disk.
	for dbPath := range dbHashIndex {
		if _, found := discovered[dbPath]; found {
			continue
		}
		// File removed from disk — orphan its memories, then delete symbols + file record.
		orphaned += idx.snapshotMemoriesForFile(ctx, repoName, dbPath, "symbol_deleted")
		if markErr := store.MarkNameCallers(ctx, idx.db, repoName, map[string][]string{dbPath: nil}, nil); markErr != nil {
			slog.Warn("indexer: failed to mark callers of removed file", "file", dbPath, "error", markErr)
			continue // keep the file record so the next run retries
		}
		// One transaction removes the file's FTS rows, embeddings, edges, routes,
		// call sites, symbols and file record (the same rows a re-index
		// replaces), so no ghost search hit outlives the file.
		if delErr := store.DeleteFileIndex(ctx, idx.db, repoName, dbPath); delErr != nil {
			slog.Warn("indexer: failed to delete index of removed file", "file", dbPath, "error", delErr)
			continue // keep the file record so the next run retries
		}
		deleted++
		slog.Info("indexer: removed deleted file", "file", dbPath)
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
		// Null out symbol_id now rather than relying on ON DELETE SET NULL, so
		// the memories are orphaned with their snapshot before any delete.
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
) ([]models.Symbol, []ExtractedDep, []ExtractedRoute, error) {
	adapter, ok := idx.registry.GetAdapter(file.Extension)
	if !ok {
		return nil, nil, nil, fmt.Errorf("indexer: no adapter for extension %s", file.Extension)
	}

	source, err := os.ReadFile(file.Path)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("indexer: failed to read %s: %w", file.Path, err)
	}

	pool := idx.pools.GetPool(adapter)
	tree, err := pool.Parse(ctx, source)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("indexer: failed to parse %s: %w", file.RelPath, err)
	}

	symbols, err := idx.extractor.ExtractSymbols(ctx, tree, source, adapter, repoName, file.RelPath)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("indexer: failed to extract symbols from %s: %w", file.RelPath, err)
	}

	deps, err := idx.extractor.ExtractDependencies(ctx, tree, source, adapter, file.RelPath)
	if err != nil {
		slog.Warn("indexer: failed to extract dependencies", "file", file.RelPath, "error", err)
		// Non-fatal: continue without deps.
		deps = nil
	}

	routes, err := idx.extractor.ExtractRoutes(ctx, tree, source, adapter, file.RelPath)
	if err != nil {
		slog.Warn("indexer: failed to extract routes", "file", file.RelPath, "error", err)
		// Non-fatal: continue without routes.
		routes = nil
	}

	return symbols, deps, routes, nil
}

// storeFileResults atomically replaces a file's symbols, FTS rows, embeddings,
// edges and route definitions. The file record is written with an empty
// (pending) hash; IndexRepo commits the real hash once the run completes.
// Fresh routes and edges are stored after all files' symbol IDs exist.
func (idx *Indexer) storeFileResults(
	ctx context.Context,
	file DiscoveredFile,
	symbols []models.Symbol,
	deps []ExtractedDep,
	repoName string,
) error {
	modelFile := models.File{
		RepoName:  repoName,
		Path:      file.RelPath,
		SizeBytes: file.SizeBytes,
	}
	if err := store.ReplaceFileIndex(ctx, idx.db, &modelFile, symbols, callSites(deps)); err != nil {
		return fmt.Errorf("indexer: failed to store %s: %w", file.RelPath, err)
	}
	return nil
}

// resolveDependencies resolves the call/extends/implements references of the
// files stored by this run (from their already-extracted deps, no re-parse)
// and of every file marked for re-resolution, by this run or an interrupted
// earlier one (from their stored call sites). See resolver for the rules.
// Each resolved file's outgoing edges are replaced and its mark cleared in a
// single transaction.
func (idx *Indexer) resolveDependencies(ctx context.Context, repoName string, stored []fileResult, symbolsByFile map[string][]models.Symbol) (int, error) {
	pending, err := store.GetPendingResolution(ctx, idx.db, repoName)
	if err != nil {
		return 0, fmt.Errorf("indexer: failed to load pending resolution: %w", err)
	}

	sites := make(map[string][]store.CallSite, len(stored)+len(pending))
	files := make([]string, 0, len(stored)+len(pending))
	for _, r := range stored {
		sites[r.file.RelPath] = callSites(r.deps)
		files = append(files, r.file.RelPath)
	}
	var load []string
	for _, p := range pending {
		if _, ok := sites[p]; !ok {
			load = append(load, p)
		}
	}
	loaded, err := store.GetCallSitesByFiles(ctx, idx.db, repoName, load)
	if err != nil {
		return 0, fmt.Errorf("indexer: failed to load call sites: %w", err)
	}
	for _, p := range load {
		sites[p] = loaded[p]
		files = append(files, p)
	}

	res := newResolver(symbolsByFile)
	res.goModule = goModulePath(idx.repoRoot)
	var allDeps []models.Dependency
	for _, f := range files {
		allDeps = append(allDeps, res.resolveFile(f, symbolsByFile[f], sites[f])...)
	}

	if err := store.ReplaceResolvedDependencies(ctx, idx.db, repoName, files, allDeps, pending); err != nil {
		return 0, fmt.Errorf("indexer: failed to store resolved dependencies: %w", err)
	}
	return len(allDeps), nil
}

// callSites converts extracted deps to the call sites stored for a file.
func callSites(deps []ExtractedDep) []store.CallSite {
	out := make([]store.CallSite, 0, len(deps))
	for _, d := range deps {
		name := d.Symbol
		if d.Kind == "import" {
			name = d.Source
		}
		if name == "" {
			continue
		}
		out = append(out, store.CallSite{Line: d.Line, Kind: d.Kind, Name: name, Qualifier: d.Qualifier})
	}
	return out
}

// symbolNames returns the names and qualified names the symbols define.
func symbolNames(syms []models.Symbol) []string {
	out := make([]string, 0, 2*len(syms))
	for _, s := range syms {
		out = append(out, s.Name)
		if s.QualifiedName != s.Name {
			out = append(out, s.QualifiedName)
		}
	}
	return out
}

// enclosingSymbol returns the innermost symbol whose line range contains line:
// the smallest span, and on a tie the one that starts last, then the first in
// syms. It reports false when no symbol contains the line.
func enclosingSymbol(syms []models.Symbol, line int) (models.Symbol, bool) {
	var best models.Symbol
	found := false
	for _, s := range syms {
		if line < s.StartLine || line > s.EndLine {
			continue
		}
		span, bestSpan := s.EndLine-s.StartLine, best.EndLine-best.StartLine
		if !found || span < bestSpan || (span == bestSpan && s.StartLine > best.StartLine) {
			best, found = s, true
		}
	}
	return best, found
}

// relinkOrphanedMemories scans newly indexed symbols and re-links any orphaned
// memories of repoName that have matching last_known_symbol + last_known_file.
// Orphans are scoped by repo so a note never attaches to another repo's
// symbol at the same path.
func (idx *Indexer) relinkOrphanedMemories(ctx context.Context, repoName string, filePaths []string, symbolsByFile map[string][]models.Symbol) int {
	// Batch-load all orphaned memories (single query instead of per-symbol lookups).
	orphanMap, err := store.GetAllOrphanedMemories(ctx, idx.db, repoName)
	if err != nil {
		slog.Warn("indexer: failed to load orphaned memories", "error", err)
		return 0
	}
	if len(orphanMap) == 0 {
		return 0
	}

	relinked := 0
	for _, path := range filePaths {
		// symbolsByFile lists a file's symbols by start line. Same-name symbols
		// in one file (overloads, several init()) share a key: the first one,
		// the lowest start line, takes the memories, and the key is consumed so
		// a later one does not take them over.
		syms := symbolsByFile[path]
		for _, s := range syms {
			key := s.QualifiedName + "\x00" + s.FilePath
			orphans, ok := orphanMap[key]
			if !ok || len(orphans) == 0 {
				continue
			}
			delete(orphanMap, key)
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

// generateEmbeddings generates and stores vector embeddings and FTS5 entries
// for all symbols belonging to the given file paths. Returns the count of
// embeddings stored.
func (idx *Indexer) generateEmbeddings(ctx context.Context, repoName string, filePaths []string, symbolsByFile map[string][]models.Symbol) int {
	embedded := 0

	// Batch-fetch callee names for all symbols across all files.
	// This single query is much faster than per-symbol queries.
	var allSymbolIDs []int64
	for _, path := range filePaths {
		for _, s := range symbolsByFile[path] {
			allSymbolIDs = append(allSymbolIDs, s.ID)
		}
	}
	calleeMap, err := store.GetCalleeNamesForSymbols(ctx, idx.db, allSymbolIDs)
	if err != nil {
		slog.Warn("indexer: failed to fetch callee names for keyword enrichment", "error", err)
		calleeMap = make(map[int64][]string) // Continue with empty map
	}

	for _, path := range filePaths {
		syms := symbolsByFile[path]
		if len(syms) == 0 {
			continue
		}

		// Extract keywords (with callee names) and build FTS entries for this file's symbols.
		symKeywords := make([]string, len(syms))
		ftsEntries := make([]store.FTSSymbol, len(syms))
		for j, s := range syms {
			sig := s.CodeBlock
			if idx := strings.IndexByte(sig, '\n'); idx >= 0 {
				sig = sig[:idx]
			}
			kw := BuildSymbolKeywords(s.CodeBlock, s.Kind, calleeMap[s.ID])
			symKeywords[j] = kw
			ftsEntries[j] = store.FTSSymbol{
				SymbolID:      s.ID,
				RepoName:      repoName,
				Name:          s.Name,
				QualifiedName: s.QualifiedName,
				Kind:          s.Kind,
				Signature:     sig,
				ExtraKeywords: kw,
			}
		}
		if err := store.BatchInsertFTSSymbols(ctx, idx.db, ftsEntries); err != nil {
			slog.Warn("indexer: failed to insert FTS entries", "file", path, "error", err)
		}

		for i := 0; i < len(syms); i += vectorstore.DefaultBatchSize {
			end := i + vectorstore.DefaultBatchSize
			if end > len(syms) {
				end = len(syms)
			}
			batch := syms[i:end]

			texts := make([]string, len(batch))
			for j, s := range batch {
				// Split space-separated keywords back to slice for variadic call.
				kwSlice := []string{}
				if symKeywords[i+j] != "" {
					kwSlice = strings.Fields(symKeywords[i+j])
				}
				texts[j] = vectorstore.SymbolEmbedText(s.Kind, s.QualifiedName, s.CodeBlock, kwSlice...)
			}

			vecs, err := idx.embedder.EmbedBatch(ctx, texts)
			if err != nil {
				slog.Warn("indexer: embedding batch failed", "file", path, "error", err)
				continue
			}

			ids := make([]int64, len(batch))
			for j, s := range batch {
				ids[j] = s.ID
			}
			if err := vectorstore.BatchUpsertEmbeddings(ctx, idx.db, repoName, ids, vecs); err != nil {
				slog.Warn("indexer: failed to batch upsert embeddings", "file", path, "error", err)
				continue
			}
			embedded += len(batch)
		}

		if embedded > 0 && embedded%100 == 0 {
			slog.Info("indexer: embedding progress", "embeddings", embedded)
		}
	}
	slog.Info("indexer: embedding complete", "embeddings_generated", embedded)
	return embedded
}
