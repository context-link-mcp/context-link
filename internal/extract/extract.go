// Package extract builds a budgeted, ranked code-context bundle for a
// natural-language task description. It is the batch counterpart of the MCP
// tools: one call in, one JSON-serialisable Result out, with ranking, test
// filtering and budget packing done next to the data instead of over a
// per-tool protocol.
package extract

import (
	"context"
	"fmt"
	"maps"
	"sort"
	"strings"

	"github.com/context-link-mcp/context-link/internal/indexer"
	"github.com/context-link-mcp/context-link/internal/store"
	"github.com/context-link-mcp/context-link/internal/tools"
	"github.com/context-link-mcp/context-link/internal/vectorstore"
	"github.com/context-link-mcp/context-link/pkg/models"
)

// SchemaVersion is the version of the Result JSON contract.
// Version 2 replaced files[].symbols names with SymbolRef objects.
const SchemaVersion = 2

// Profile names accepted by Options.Profile.
const ProfileThreatModel = "threat-model"

// Defaults applied by Run for zero-valued Options fields.
const (
	DefaultBudgetBytes       = 50000
	DefaultMaxSymbolsPerFile = 3
	defaultTopKPerQuery      = 10
	minSimilarity            = 0.3
	// entryPointBoost lifts entry-point handlers in the threat-model profile.
	// RRF scores are ~1/(60+rank) ≈ 0.016, so this is about two top ranks.
	entryPointBoost = 0.03
)

// threatModelQueries are the security-oriented queries added by the
// threat-model profile.
var threatModelQueries = []string{
	"authentication authorization login session",
	"HTTP route handler endpoint API",
	"database query storage model",
	"input validation sanitization user data",
	"configuration secret key environment",
}

// Options controls an extraction.
type Options struct {
	RepoName          string
	Queries           []string
	Profile           string // "" or ProfileThreatModel
	BudgetBytes       int    // limit for the summed size of files[].content
	IncludeTests      bool   // keep tests, fixtures, vendored code and migrations
	MaxSymbolsPerFile int
	// SkipDependencyCode lists direct dependencies as references only instead
	// of also packing their code (CLI: --dependency-depth 0).
	SkipDependencyCode bool
}

// IndexInfo describes the index the extraction ran against. The caller fills
// FilesDiscovered and ParseFailures from the indexing run; Run fills the rest
// from the database.
type IndexInfo struct {
	FilesDiscovered int                    `json:"files_discovered"`
	FilesIndexed    int                    `json:"files_indexed"`
	ParseFailures   []indexer.IndexFailure `json:"parse_failures"`
	Symbols         int                    `json:"symbols"`
	// Languages maps language to the number of files with at least one symbol.
	Languages map[string]int `json:"languages"`
}

// EntryPoint is an HTTP route definition: part of the attack surface.
type EntryPoint struct {
	Method    string `json:"method"`
	Path      string `json:"path"`
	Handler   string `json:"handler,omitempty"`
	File      string `json:"file"`
	Line      int    `json:"line"`
	Framework string `json:"framework"`
}

// Roles of a symbol in SymbolRef.Role.
const (
	RoleMatch      = "match"      // ranked by a query or an entry point
	RoleDependency = "dependency" // packed because a match calls it
	RoleOutline    = "outline"    // other signature in a skeleton file
)

// Separators between the symbol texts in FileEntry.Content.
const (
	codeSep     = "\n\n"
	skeletonSep = "\n"
)

// DependencyRef points at a resolved direct callee of a symbol.
type DependencyRef struct {
	Name          string `json:"name"`
	QualifiedName string `json:"qualified_name"`
	Kind          string `json:"kind"`
	File          string `json:"file"`
	StartLine     int    `json:"start_line"`
	EndLine       int    `json:"end_line"`
}

// SymbolRef describes one symbol whose text is part of FileEntry.Content.
type SymbolRef struct {
	Name          string `json:"name"`
	QualifiedName string `json:"qualified_name"`
	Kind          string `json:"kind"`
	StartLine     int    `json:"start_line"` // 1-based line in the source file
	EndLine       int    `json:"end_line"`
	// Offset is the UTF-8 byte offset of the symbol's text in Content: the
	// body in code files, the signature line in skeleton files.
	Offset int    `json:"offset"`
	Role   string `json:"role"`
	// Dependencies lists every resolved direct callee, whether or not its
	// code was packed. Always empty in skeleton files.
	Dependencies []DependencyRef `json:"dependencies"`
}

// FileEntry is one file in the packed context.
type FileEntry struct {
	Path    string      `json:"path"`
	Tier    string      `json:"tier"` // "code" (full symbol bodies) or "skeleton" (signatures)
	Symbols []SymbolRef `json:"symbols"`
	Reason  string      `json:"reason"`
	Score   float64     `json:"score"`
	Content string      `json:"content"`
}

// Budget reports how much of the size limit the contents used.
type Budget struct {
	LimitBytes int `json:"limit_bytes"`
	UsedBytes  int `json:"used_bytes"`
}

// Result is the extract command's JSON output (schema_version 2).
type Result struct {
	SchemaVersion int          `json:"schema_version"`
	Repository    string       `json:"repository"`
	Index         IndexInfo    `json:"index"`
	EntryPoints   []EntryPoint `json:"entry_points"`
	Files         []FileEntry  `json:"files"`
	Budget        Budget       `json:"budget"`
	Warnings      []string     `json:"warnings"`
}

// scored accumulates the relevance of one symbol across queries.
type scored struct {
	score      float64
	bestScore  float64
	bestReason string
}

func (s *scored) add(score float64, reason string) {
	s.score += score
	if score > s.bestScore {
		s.bestScore, s.bestReason = score, reason
	}
}

// Run extracts context from an already-indexed repo.
func Run(
	ctx context.Context,
	db *store.DB,
	embedder vectorstore.Embedder,
	cache *vectorstore.VectorCache,
	opts Options,
	index IndexInfo,
) (*Result, error) {
	if opts.BudgetBytes <= 0 {
		opts.BudgetBytes = DefaultBudgetBytes
	}
	if opts.MaxSymbolsPerFile <= 0 {
		opts.MaxSymbolsPerFile = DefaultMaxSymbolsPerFile
	}

	queries := append([]string(nil), opts.Queries...)
	switch opts.Profile {
	case "":
	case ProfileThreatModel:
		queries = append(queries, threatModelQueries...)
	default:
		return nil, fmt.Errorf("extract: unknown profile %q", opts.Profile)
	}
	if len(queries) == 0 {
		return nil, fmt.Errorf("extract: at least one query or a profile is required")
	}

	res := &Result{
		SchemaVersion: SchemaVersion,
		Repository:    opts.RepoName,
		Warnings:      []string{},
		EntryPoints:   []EntryPoint{},
		Files:         []FileEntry{},
	}

	if err := fillIndexInfo(ctx, db, opts.RepoName, &index); err != nil {
		return nil, err
	}
	res.Index = index
	if len(index.ParseFailures) > 0 {
		res.Warnings = append(res.Warnings,
			fmt.Sprintf("%d files failed to index and are missing from the context", len(index.ParseFailures)))
	}

	// Entry points (routes) first: they are the attack surface.
	routes, err := store.FindRoutes(ctx, db, opts.RepoName, store.RouteFilter{Kind: "definition", Limit: 200})
	if err != nil {
		return nil, fmt.Errorf("extract: failed to load routes: %w", err)
	}

	scores := make(map[int64]*scored)
	for _, q := range queries {
		hits, err := tools.HybridSearch(ctx, db, embedder, cache, opts.RepoName, q, defaultTopKPerQuery, minSimilarity)
		if err != nil {
			return nil, fmt.Errorf("extract: search for %q failed: %w", q, err)
		}
		for _, h := range hits {
			s := scores[h.SymbolID]
			if s == nil {
				s = &scored{}
				scores[h.SymbolID] = s
			}
			s.add(h.Score, "query: "+q)
		}
	}

	if opts.Profile == ProfileThreatModel {
		for _, r := range routes {
			if r.HandlerSymbolID == nil {
				continue
			}
			s := scores[*r.HandlerSymbolID]
			if s == nil {
				s = &scored{}
				scores[*r.HandlerSymbolID] = s
			}
			s.add(entryPointBoost, fmt.Sprintf("entry point: %s %s", r.Method, r.PathPattern))
		}
	}

	ids := make([]int64, 0, len(scores))
	for id := range scores {
		ids = append(ids, id)
	}
	symbols, err := store.GetSymbolsByIDs(ctx, db, opts.RepoName, ids)
	if err != nil {
		return nil, fmt.Errorf("extract: failed to load symbols: %w", err)
	}

	res.EntryPoints = buildEntryPoints(ctx, db, opts, routes)

	candidates, excluded := groupByFile(symbols, scores, opts)
	if excluded > 0 {
		res.Warnings = append(res.Warnings,
			fmt.Sprintf("%d matches in tests, fixtures, vendored code or migrations were excluded (use --include-tests)", excluded))
	}
	if len(candidates) == 0 {
		res.Warnings = append(res.Warnings, "no matching symbols found")
	}

	var stats packStats
	res.Files, res.Budget, stats, err = pack(ctx, db, opts, candidates, scores)
	if err != nil {
		return nil, err
	}
	if stats.skippedFiles > 0 {
		res.Warnings = append(res.Warnings,
			fmt.Sprintf("%d matching files did not fit the %d byte budget", stats.skippedFiles, opts.BudgetBytes))
	}
	if stats.droppedDeps > 0 {
		res.Warnings = append(res.Warnings,
			fmt.Sprintf("%d dependencies did not fit the budget (listed as references only)", stats.droppedDeps))
	}
	return res, nil
}

// fillIndexInfo completes IndexInfo with the totals stored in the database.
func fillIndexInfo(ctx context.Context, db *store.DB, repo string, info *IndexInfo) error {
	hashes, err := store.GetFileHashIndex(ctx, db, repo)
	if err != nil {
		return fmt.Errorf("extract: failed to count files: %w", err)
	}
	n, err := store.CountSymbols(ctx, db, repo)
	if err != nil {
		return fmt.Errorf("extract: %w", err)
	}
	langs, err := store.CountFilesByLanguage(ctx, db, repo)
	if err != nil {
		return fmt.Errorf("extract: %w", err)
	}
	info.FilesIndexed = len(hashes)
	info.Symbols = int(n)
	info.Languages = langs
	if info.ParseFailures == nil {
		info.ParseFailures = []indexer.IndexFailure{}
	}
	return nil
}

// buildEntryPoints converts stored routes to entry points, skipping test
// files unless asked, and resolving handler names.
func buildEntryPoints(ctx context.Context, db *store.DB, opts Options, routes []models.Route) []EntryPoint {
	var handlerIDs []int64
	for _, r := range routes {
		if r.HandlerSymbolID != nil {
			handlerIDs = append(handlerIDs, *r.HandlerSymbolID)
		}
	}
	handlers, _ := store.GetSymbolsByIDs(ctx, db, opts.RepoName, handlerIDs)

	out := make([]EntryPoint, 0, len(routes))
	for _, r := range routes {
		if !opts.IncludeTests && IsNoisePath(r.FilePath) {
			continue
		}
		ep := EntryPoint{
			Method: r.Method, Path: r.PathPattern, File: r.FilePath,
			Line: r.StartLine, Framework: r.Framework,
		}
		if r.HandlerSymbolID != nil {
			ep.Handler = handlers[*r.HandlerSymbolID].Name
		}
		out = append(out, ep)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].File != out[j].File {
			return out[i].File < out[j].File
		}
		return out[i].Line < out[j].Line
	})
	return out
}

// fileCandidate is a file with its best-scoring symbols, ready to pack.
type fileCandidate struct {
	path    string
	score   float64
	reason  string
	symbols []models.Symbol // best first, capped at MaxSymbolsPerFile
}

// groupByFile groups scored symbols by file, drops noise paths, and returns
// the candidates best first plus how many symbols were excluded as noise.
func groupByFile(symbols map[int64]models.Symbol, scores map[int64]*scored, opts Options) ([]fileCandidate, int) {
	byFile := make(map[string][]models.Symbol)
	excluded := 0
	for _, sym := range symbols {
		if !opts.IncludeTests && IsNoisePath(sym.FilePath) {
			excluded++
			continue
		}
		byFile[sym.FilePath] = append(byFile[sym.FilePath], sym)
	}

	candidates := make([]fileCandidate, 0, len(byFile))
	for path, syms := range byFile {
		sort.Slice(syms, func(i, j int) bool {
			si, sj := scores[syms[i].ID].score, scores[syms[j].ID].score
			if si != sj {
				return si > sj
			}
			return syms[i].StartLine < syms[j].StartLine
		})
		if len(syms) > opts.MaxSymbolsPerFile {
			syms = syms[:opts.MaxSymbolsPerFile]
		}
		total := 0.0
		for _, s := range syms {
			total += scores[s.ID].score
		}
		candidates = append(candidates, fileCandidate{
			path: path, score: total, symbols: syms,
			reason: scores[syms[0].ID].bestReason,
		})
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].score != candidates[j].score {
			return candidates[i].score > candidates[j].score
		}
		return candidates[i].path < candidates[j].path
	})
	return candidates, excluded
}

// packedFile is a file entry under construction.
type packedFile struct {
	path, tier, reason string
	score              float64
	syms               []models.Symbol // in StartLine order
	roles              map[int64]string
}

// packStats reports what pack had to leave out.
type packStats struct {
	skippedFiles int // matching files with no entry at all
	droppedDeps  int // dependencies listed as references only for lack of budget
}

// pack fills the budget in three passes: full bodies of the best matches,
// then the code of their direct dependencies, then signature-only skeletons
// for matching files that did not fit. Files that fit in no form are dropped.
func pack(
	ctx context.Context,
	db *store.DB,
	opts Options,
	candidates []fileCandidate,
	scores map[int64]*scored,
) ([]FileEntry, Budget, packStats, error) {
	budget := Budget{LimitBytes: opts.BudgetBytes}
	var stats packStats
	var order []*packedFile
	byPath := make(map[string]*packedFile)
	packed := make(map[int64]bool)  // symbols whose code is in an entry
	matched := make(map[int64]bool) // matches packed in pass 1
	var deferred []fileCandidate

	var callerIDs []int64
	for _, c := range candidates {
		for _, s := range c.symbols {
			callerIDs = append(callerIDs, s.ID)
		}
	}
	deps, err := store.GetDirectDependencies(ctx, db, opts.RepoName, callerIDs)
	if err != nil {
		return nil, budget, stats, fmt.Errorf("extract: failed to load dependencies: %w", err)
	}

	// Pass 1: matches, code tier.
	for _, c := range candidates {
		syms := append([]models.Symbol(nil), c.symbols...)
		sort.Slice(syms, func(i, j int) bool { return syms[i].StartLine < syms[j].StartLine })
		size := 0
		for i, s := range syms {
			if i > 0 {
				size += len(codeSep)
			}
			size += len(s.CodeBlock)
		}
		if budget.UsedBytes+size > budget.LimitBytes {
			deferred = append(deferred, c)
			continue
		}
		budget.UsedBytes += size
		f := &packedFile{path: c.path, tier: "code", reason: c.reason, score: c.score, syms: syms, roles: map[int64]string{}}
		for _, s := range syms {
			f.roles[s.ID] = RoleMatch
			packed[s.ID] = true
			matched[s.ID] = true
		}
		order = append(order, f)
		byPath[c.path] = f
	}

	// Pass 2: direct dependencies of the packed matches, best caller first.
	if !opts.SkipDependencyCode {
		dropped := make(map[int64]bool)
		var depIDs []int64
		for _, c := range candidates {
			for _, caller := range c.symbols {
				if !matched[caller.ID] {
					continue
				}
				for _, d := range deps[caller.ID] {
					if packed[d.ID] || (!opts.IncludeTests && IsNoisePath(d.FilePath)) {
						continue
					}
					f := byPath[d.FilePath]
					size := len(d.CodeBlock)
					if f != nil {
						size += len(codeSep)
					}
					if budget.UsedBytes+size > budget.LimitBytes {
						dropped[d.ID] = true
						continue
					}
					budget.UsedBytes += size
					if f == nil {
						f = &packedFile{
							path: d.FilePath, tier: "code", reason: "dependency of " + caller.Name,
							score: scores[caller.ID].score, roles: map[int64]string{},
						}
						order = append(order, f)
						byPath[d.FilePath] = f
					}
					f.syms = append(f.syms, d)
					sort.SliceStable(f.syms, func(i, j int) bool { return f.syms[i].StartLine < f.syms[j].StartLine })
					f.roles[d.ID] = RoleDependency
					packed[d.ID] = true
					depIDs = append(depIDs, d.ID)
				}
			}
		}
		stats.droppedDeps = len(dropped)

		// Packed dependencies list their own callees too.
		more, err := store.GetDirectDependencies(ctx, db, opts.RepoName, depIDs)
		if err != nil {
			return nil, budget, stats, fmt.Errorf("extract: failed to load dependencies: %w", err)
		}
		maps.Copy(deps, more)
	}

	// Pass 3: skeletons for matching files that did not fit as code.
	for _, c := range deferred {
		if byPath[c.path] != nil {
			continue // already present with dependency code
		}
		all, err := store.GetSymbolsByFile(ctx, db, opts.RepoName, c.path)
		if err != nil {
			return nil, budget, stats, fmt.Errorf("extract: failed to load symbols for %s: %w", c.path, err)
		}
		size := 0
		for i, s := range all {
			if i > 0 {
				size += len(skeletonSep)
			}
			size += len(tools.ExtractSignature(s.CodeBlock))
		}
		if len(all) == 0 || budget.UsedBytes+size > budget.LimitBytes {
			continue
		}
		budget.UsedBytes += size
		f := &packedFile{path: c.path, tier: "skeleton", reason: c.reason, score: c.score, syms: all, roles: map[int64]string{}}
		for _, s := range all {
			f.roles[s.ID] = RoleOutline
		}
		for _, s := range c.symbols {
			f.roles[s.ID] = RoleMatch
		}
		order = append(order, f)
		byPath[c.path] = f
	}

	for _, c := range candidates {
		if byPath[c.path] == nil {
			stats.skippedFiles++
		}
	}

	files := make([]FileEntry, 0, len(order))
	for _, f := range order {
		files = append(files, f.entry(deps))
	}
	return files, budget, stats, nil
}

// entry renders a packed file with its content and symbol references.
func (f *packedFile) entry(deps map[int64][]models.Symbol) FileEntry {
	parts := make([]string, len(f.syms))
	for i, s := range f.syms {
		parts[i] = s.CodeBlock
		if f.tier == "skeleton" {
			parts[i] = tools.ExtractSignature(s.CodeBlock)
		}
	}
	sep := codeSep
	if f.tier == "skeleton" {
		sep = skeletonSep
	}
	content, offsets := buildContent(parts, sep)

	refs := make([]SymbolRef, len(f.syms))
	for i, s := range f.syms {
		depRefs := []DependencyRef{}
		if f.tier == "code" {
			for _, d := range deps[s.ID] {
				depRefs = append(depRefs, DependencyRef{
					Name: d.Name, QualifiedName: d.QualifiedName, Kind: d.Kind,
					File: d.FilePath, StartLine: d.StartLine, EndLine: d.EndLine,
				})
			}
		}
		refs[i] = SymbolRef{
			Name: s.Name, QualifiedName: s.QualifiedName, Kind: s.Kind,
			StartLine: s.StartLine, EndLine: s.EndLine,
			Offset: offsets[i], Role: f.roles[s.ID], Dependencies: depRefs,
		}
	}
	return FileEntry{
		Path: f.path, Tier: f.tier, Symbols: refs,
		Reason: f.reason, Score: round(f.score), Content: content,
	}
}

// buildContent joins parts with sep and returns the result plus the byte
// offset of each part in it.
func buildContent(parts []string, sep string) (string, []int) {
	var b strings.Builder
	offsets := make([]int, len(parts))
	for i, p := range parts {
		if i > 0 {
			b.WriteString(sep)
		}
		offsets[i] = b.Len()
		b.WriteString(p)
	}
	return b.String(), offsets
}

func round(f float64) float64 {
	return float64(int(f*1000+0.5)) / 1000
}

// noiseDirs are directory names whose contents are excluded from ranking.
var noiseDirs = map[string]bool{
	"test": true, "tests": true, "__tests__": true, "spec": true, "specs": true,
	"e2e": true, "fixtures": true, "fixture": true, "testdata": true,
	"mocks": true, "__mocks__": true, "vendor": true, "node_modules": true,
	"third_party": true, "migrations": true, "alembic": true,
}

// IsNoisePath reports whether a repo-relative path is a test, fixture,
// vendored or migration file.
func IsNoisePath(path string) bool {
	p := strings.ToLower(strings.ReplaceAll(path, "\\", "/"))
	segments := strings.Split(p, "/")
	for _, dir := range segments[:len(segments)-1] {
		if noiseDirs[dir] {
			return true
		}
	}
	base := segments[len(segments)-1]
	switch {
	case base == "conftest.py",
		strings.HasPrefix(base, "test_"),
		strings.Contains(base, "_test."),
		strings.Contains(base, ".test."),
		strings.Contains(base, ".spec."):
		return true
	}
	return false
}
