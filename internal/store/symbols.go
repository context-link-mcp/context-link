package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/context-link-mcp/context-link/pkg/models"
)

// ErrSymbolNotFound is returned when a symbol record is not found.
var ErrSymbolNotFound = errors.New("symbol not found")

// ErrAmbiguousSymbol is returned by ResolveSymbol when a name matches no
// symbol exactly and several distinct symbols by substring.
var ErrAmbiguousSymbol = errors.New("ambiguous symbol name")

// ErrEmptySymbolName is returned by ResolveSymbol for an empty or blank name.
var ErrEmptySymbolName = errors.New("symbol name is empty")

// maxSymbolCandidates is the number of substring matches ResolveSymbol
// reports as suggestions when a name does not resolve.
const maxSymbolCandidates = 5

// GetSymbolByID retrieves a symbol by its primary key.
func GetSymbolByID(ctx context.Context, db *DB, id int64) (*models.Symbol, error) {
	row := db.QueryRowContext(ctx,
		`SELECT id, repo_name, name, qualified_name, kind, file_path,
		        content_hash, code_block, start_line, end_line, language, indexed_at
		 FROM symbols WHERE id = ?`,
		id,
	)
	return scanSymbol(row)
}

// GetSymbolByName retrieves a symbol by exact name within a repo.
// If multiple symbols share the same name (in several files, or overloads in
// one file), returns the first by file path, then start line.
func GetSymbolByName(ctx context.Context, db *DB, repoName, name string) (*models.Symbol, error) {
	row := db.QueryRowContext(ctx,
		`SELECT id, repo_name, name, qualified_name, kind, file_path,
		        content_hash, code_block, start_line, end_line, language, indexed_at
		 FROM symbols WHERE repo_name = ? AND name = ?
		 ORDER BY file_path, start_line, id LIMIT 1`,
		repoName, name,
	)

	return scanSymbol(row)
}

// GetSymbolByQualifiedName retrieves a symbol by its fully qualified name.
// Overloads share a qualified name; the first by file path, then start line,
// is returned.
func GetSymbolByQualifiedName(ctx context.Context, db *DB, repoName, qualifiedName string) (*models.Symbol, error) {
	row := db.QueryRowContext(ctx,
		`SELECT id, repo_name, name, qualified_name, kind, file_path,
		        content_hash, code_block, start_line, end_line, language, indexed_at
		 FROM symbols WHERE repo_name = ? AND qualified_name = ?
		 ORDER BY file_path, start_line, id LIMIT 1`,
		repoName, qualifiedName,
	)

	return scanSymbol(row)
}

// SearchSymbolsByName returns symbols whose name or qualified name contains
// pattern as a literal substring (ASCII case-insensitive; LIKE wildcards in
// pattern are matched literally). Exact matches rank first, then prefix
// matches. An empty or blank pattern returns no symbols.
func SearchSymbolsByName(ctx context.Context, db *DB, repoName, pattern string, limit int) ([]models.Symbol, error) {
	if limit <= 0 {
		limit = 20
	}
	if strings.TrimSpace(pattern) == "" {
		return nil, nil
	}
	contains := "%" + EscapeLike(pattern) + "%"

	rows, err := db.QueryContext(ctx,
		`SELECT id, repo_name, name, qualified_name, kind, file_path,
		        content_hash, code_block, start_line, end_line, language, indexed_at
		 FROM symbols
		 WHERE repo_name = ? AND (name LIKE ? ESCAPE '\' OR qualified_name LIKE ? ESCAPE '\')
		 ORDER BY
		   CASE WHEN name = ? THEN 0
		        WHEN qualified_name = ? THEN 1
		        WHEN name LIKE ? ESCAPE '\' THEN 2
		        ELSE 3
		   END,
		   file_path, start_line, id
		 LIMIT ?`,
		repoName,
		contains, contains,
		pattern, pattern,
		EscapeLike(pattern)+"%",
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("store: failed to search symbols: %w", err)
	}
	defer rows.Close()

	return scanSymbols(rows)
}

// GetSymbolsByFile returns all symbols in a specific file.
func GetSymbolsByFile(ctx context.Context, db *DB, repoName, filePath string) ([]models.Symbol, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id, repo_name, name, qualified_name, kind, file_path,
		        content_hash, code_block, start_line, end_line, language, indexed_at
		 FROM symbols WHERE repo_name = ? AND file_path = ?
		 ORDER BY start_line`,
		repoName, filePath,
	)
	if err != nil {
		return nil, fmt.Errorf("store: failed to get symbols for file %s/%s: %w", repoName, filePath, err)
	}
	defer rows.Close()

	return scanSymbols(rows)
}

// BatchInsertSymbols inserts multiple symbols in a single transaction.
// Uses INSERT OR REPLACE to handle re-indexing of existing symbols. A symbol
// is unique by (repo, qualified name, file, start line), so same-name symbols
// at different lines of one file (overloads, several init()) are all kept.
func BatchInsertSymbols(ctx context.Context, db *DB, symbols []models.Symbol) error {
	if len(symbols) == 0 {
		return nil
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: failed to begin transaction: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	stmt, err := tx.PrepareContext(ctx,
		`INSERT OR REPLACE INTO symbols
		 (repo_name, name, qualified_name, kind, file_path, content_hash, code_block, start_line, end_line, language)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
	)
	if err != nil {
		return fmt.Errorf("store: failed to prepare symbol insert: %w", err)
	}
	defer stmt.Close()

	for _, s := range symbols {
		_, err := stmt.ExecContext(ctx,
			s.RepoName, s.Name, s.QualifiedName, s.Kind, s.FilePath,
			s.ContentHash, s.CodeBlock, s.StartLine, s.EndLine, s.Language,
		)
		if err != nil {
			return fmt.Errorf("store: failed to insert symbol %s: %w", s.QualifiedName, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: failed to commit symbol batch: %w", err)
	}
	return nil
}

// DeleteSymbolsByFile removes all symbols for a given file.
// Memory references become orphaned (SET NULL) via FK constraint.
func DeleteSymbolsByFile(ctx context.Context, db *DB, repoName, filePath string) error {
	_, err := db.ExecContext(ctx,
		`DELETE FROM symbols WHERE repo_name = ? AND file_path = ?`,
		repoName, filePath,
	)
	if err != nil {
		return fmt.Errorf("store: failed to delete symbols for %s/%s: %w", repoName, filePath, err)
	}
	return nil
}

// GetSymbolNameIndex builds a map of symbol name/qualified_name → ID for
// dependency resolution within a repo. A name shared by several symbols maps
// to the first by file path, then start line.
func GetSymbolNameIndex(ctx context.Context, db *DB, repoName string) (map[string]int64, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id, name, qualified_name FROM symbols WHERE repo_name = ?
		 ORDER BY file_path, start_line, id`,
		repoName,
	)
	if err != nil {
		return nil, fmt.Errorf("store: failed to build symbol index for %s: %w", repoName, err)
	}
	defer rows.Close()

	index := make(map[string]int64)
	for rows.Next() {
		var id int64
		var name, qualifiedName string
		if err := rows.Scan(&id, &name, &qualifiedName); err != nil {
			return nil, fmt.Errorf("store: failed to scan symbol index row: %w", err)
		}
		if _, ok := index[name]; !ok {
			index[name] = id
		}
		if _, ok := index[qualifiedName]; !ok {
			index[qualifiedName] = id
		}
	}
	return index, rows.Err()
}

// SymbolResolution is the outcome of ResolveSymbol.
type SymbolResolution struct {
	// Symbol is the resolved symbol; nil when the lookup failed.
	Symbol *models.Symbol
	// Fuzzy is true when Symbol was found by a substring match because no
	// symbol has the requested name or qualified name.
	Fuzzy bool
	// Candidates holds up to five substring matches when the name did not
	// resolve (ErrSymbolNotFound or ErrAmbiguousSymbol), as suggestions.
	Candidates []models.Symbol
}

// ResolveSymbol looks up a symbol by name: exact name first, then exact
// qualified name. When neither exists and allowFuzzy is true, a substring
// match is used only if every matching symbol shares one qualified name
// (Fuzzy is then set); several distinct matches return ErrAmbiguousSymbol with
// Candidates. With allowFuzzy false a substring match never resolves; its
// matches are returned as Candidates of ErrSymbolNotFound. A blank name
// returns ErrEmptySymbolName. Database failures are returned wrapped, never as
// ErrSymbolNotFound.
func ResolveSymbol(ctx context.Context, db *DB, repoName, name string, allowFuzzy bool) (SymbolResolution, error) {
	if strings.TrimSpace(name) == "" {
		return SymbolResolution{}, ErrEmptySymbolName
	}

	sym, err := GetSymbolByName(ctx, db, repoName, name)
	if err == nil {
		return SymbolResolution{Symbol: sym}, nil
	}
	if !errors.Is(err, ErrSymbolNotFound) {
		return SymbolResolution{}, err
	}
	sym, err = GetSymbolByQualifiedName(ctx, db, repoName, name)
	if err == nil {
		return SymbolResolution{Symbol: sym}, nil
	}
	if !errors.Is(err, ErrSymbolNotFound) {
		return SymbolResolution{}, err
	}

	candidates, err := SearchSymbolsByName(ctx, db, repoName, name, maxSymbolCandidates)
	if err != nil {
		return SymbolResolution{}, err
	}
	if len(candidates) == 0 {
		return SymbolResolution{}, ErrSymbolNotFound
	}
	if !allowFuzzy {
		return SymbolResolution{Candidates: candidates}, ErrSymbolNotFound
	}

	var distinct int
	contains := "%" + EscapeLike(name) + "%"
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(DISTINCT qualified_name) FROM symbols
		 WHERE repo_name = ? AND (name LIKE ? ESCAPE '\' OR qualified_name LIKE ? ESCAPE '\')`,
		repoName, contains, contains,
	).Scan(&distinct); err != nil {
		return SymbolResolution{}, fmt.Errorf("store: failed to count symbol matches for %q: %w", name, err)
	}
	if distinct != 1 {
		return SymbolResolution{Candidates: candidates}, ErrAmbiguousSymbol
	}
	return SymbolResolution{Symbol: &candidates[0], Fuzzy: true}, nil
}

// GetSymbolWithDependencies retrieves a symbol by exact name or qualified name
// and its dependencies up to depth hops (none when depth <= 0). It never
// resolves a name by substring; see ResolveSymbol.
func GetSymbolWithDependencies(ctx context.Context, db *DB, repoName, symbolName string, depth int) (*models.Symbol, []models.Symbol, error) {
	res, err := ResolveSymbol(ctx, db, repoName, symbolName, false)
	if err != nil {
		return nil, nil, err
	}
	deps, err := GetTransitiveDependencies(ctx, db, res.Symbol.ID, depth)
	if err != nil {
		return nil, nil, err
	}
	return res.Symbol, deps, nil
}

// GetTransitiveDependencies performs a BFS of the dependency graph from
// symbolID up to maxDepth hops and returns every reachable callee once, in
// BFS order (within a level by file path and start line).
func GetTransitiveDependencies(ctx context.Context, db *DB, symbolID int64, maxDepth int) ([]models.Symbol, error) {
	if maxDepth <= 0 {
		return nil, nil
	}

	seen := map[int64]bool{symbolID: true} // avoid revisiting the root
	var result []models.Symbol
	frontier := []int64{symbolID}

	for depth := 0; depth < maxDepth && len(frontier) > 0; depth++ {
		var level []models.Symbol
		err := forEachIDChunk(frontier, 0, func(chunk []int64) error {
			rows, err := db.QueryContext(ctx,
				`SELECT s.id, s.repo_name, s.name, s.qualified_name, s.kind, s.file_path,
				        s.content_hash, s.code_block, s.start_line, s.end_line, s.language, s.indexed_at
				 FROM dependencies d
				 JOIN symbols s ON s.id = d.callee_id
				 WHERE d.caller_id IN (`+placeholders(len(chunk))+`)`,
				idArgs(chunk)...,
			)
			if err != nil {
				return fmt.Errorf("store: failed to get batch dependencies: %w", err)
			}
			defer rows.Close()
			syms, err := scanSymbols(rows)
			if err != nil {
				return err
			}
			level = append(level, syms...)
			return nil
		})
		if err != nil {
			return nil, err
		}
		sortSymbols(level)

		var nextFrontier []int64
		for _, s := range level {
			if seen[s.ID] {
				continue
			}
			seen[s.ID] = true
			result = append(result, s)
			nextFrontier = append(nextFrontier, s.ID)
		}

		frontier = nextFrontier
	}

	return result, nil
}

// sortSymbols orders symbols by file path, start line, then ID.
func sortSymbols(syms []models.Symbol) {
	sort.SliceStable(syms, func(i, j int) bool {
		a, b := syms[i], syms[j]
		if a.FilePath != b.FilePath {
			return a.FilePath < b.FilePath
		}
		if a.StartLine != b.StartLine {
			return a.StartLine < b.StartLine
		}
		return a.ID < b.ID
	})
}

// GetSymbolsByIDs returns symbols by their IDs within a repo, keyed by ID.
// Used to batch-fetch symbol metadata for KNN search results. Any number of
// IDs is accepted; they are queried in chunks.
func GetSymbolsByIDs(ctx context.Context, db *DB, repoName string, ids []int64) (map[int64]models.Symbol, error) {
	result := make(map[int64]models.Symbol, len(ids))
	err := forEachIDChunk(ids, 0, func(chunk []int64) error {
		rows, err := db.QueryContext(ctx,
			`SELECT id, repo_name, name, qualified_name, kind, file_path,
			        content_hash, code_block, start_line, end_line, language, indexed_at
			 FROM symbols WHERE repo_name = ? AND id IN (`+placeholders(len(chunk))+`)`,
			idArgs(chunk, repoName)...,
		)
		if err != nil {
			return fmt.Errorf("store: failed to get symbols by IDs: %w", err)
		}
		defer rows.Close()
		syms, err := scanSymbols(rows)
		if err != nil {
			return err
		}
		for _, s := range syms {
			result[s.ID] = s
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// GetSymbolsByRepo returns all symbols for a repo, grouped by file path.
// Used to pre-compute a symbol map for phases that need per-file symbol lookups.
func GetSymbolsByRepo(ctx context.Context, db *DB, repoName string) (map[string][]models.Symbol, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id, repo_name, name, qualified_name, kind, file_path,
		        content_hash, code_block, start_line, end_line, language, indexed_at
		 FROM symbols WHERE repo_name = ?
		 ORDER BY file_path, start_line`,
		repoName,
	)
	if err != nil {
		return nil, fmt.Errorf("store: failed to get symbols for repo %s: %w", repoName, err)
	}
	defer rows.Close()

	result := make(map[string][]models.Symbol)
	for rows.Next() {
		var s models.Symbol
		if err := rows.Scan(
			&s.ID, &s.RepoName, &s.Name, &s.QualifiedName, &s.Kind, &s.FilePath,
			&s.ContentHash, &s.CodeBlock, &s.StartLine, &s.EndLine, &s.Language, &s.IndexedAt,
		); err != nil {
			return nil, fmt.Errorf("store: failed to scan symbol row: %w", err)
		}
		result[s.FilePath] = append(result[s.FilePath], s)
	}
	return result, rows.Err()
}

// CallTreeEdge represents one edge in a call tree traversal.
type CallTreeEdge struct {
	Depth          int
	Symbol         models.Symbol
	DependencyKind string
}

// MaxCallTreeEdges caps the number of edges GetCallTree returns.
const MaxCallTreeEdges = 100

// GetCallTree performs BFS traversal of the dependency graph from a root symbol.
// direction must be "callees" (forward: what does this call?) or "callers" (reverse: what calls this?).
// Returns a flat list of edges with depth levels, each symbol once (at its
// lowest depth; within a depth by file path and start line), only symbols of
// repoName, and at most MaxCallTreeEdges edges. truncated reports whether the
// cap dropped reachable symbols. maxDepth is clamped to 3.
func GetCallTree(ctx context.Context, db *DB, repoName string, symbolID int64, direction string, maxDepth int) (edges []CallTreeEdge, truncated bool, err error) {
	if maxDepth <= 0 {
		return nil, false, nil
	}
	if maxDepth > 3 {
		maxDepth = 3
	}

	// Direction determines which column is the frontier key and which is the target.
	var frontierCol, targetCol string
	if direction == "callers" {
		frontierCol = "callee_id"
		targetCol = "caller_id"
	} else {
		frontierCol = "caller_id"
		targetCol = "callee_id"
	}

	seen := map[int64]bool{symbolID: true}
	var result []CallTreeEdge
	frontier := []int64{symbolID}

	// Collect one edge past the cap, so truncation is reported exactly.
	for depth := 1; depth <= maxDepth && len(frontier) > 0 && len(result) <= MaxCallTreeEdges; depth++ {
		var level []CallTreeEdge
		err := forEachIDChunk(frontier, 0, func(chunk []int64) error {
			query := fmt.Sprintf(
				`SELECT s.id, s.repo_name, s.name, s.qualified_name, s.kind, s.file_path,
				        s.content_hash, s.code_block, s.start_line, s.end_line, s.language, s.indexed_at,
				        d.kind
				 FROM dependencies d
				 JOIN symbols s ON s.id = d.%s
				 WHERE d.%s IN (%s) AND s.repo_name = ?`,
				targetCol, frontierCol, placeholders(len(chunk)),
			)
			args := idArgs(chunk)
			args = append(args, repoName)
			rows, err := db.QueryContext(ctx, query, args...)
			if err != nil {
				return fmt.Errorf("store: failed to get call tree edges: %w", err)
			}
			defer rows.Close()
			for rows.Next() {
				var e CallTreeEdge
				s := &e.Symbol
				if err := rows.Scan(
					&s.ID, &s.RepoName, &s.Name, &s.QualifiedName, &s.Kind, &s.FilePath,
					&s.ContentHash, &s.CodeBlock, &s.StartLine, &s.EndLine, &s.Language, &s.IndexedAt,
					&e.DependencyKind,
				); err != nil {
					return fmt.Errorf("store: failed to scan call tree row: %w", err)
				}
				e.Depth = depth
				level = append(level, e)
			}
			if err := rows.Err(); err != nil {
				return fmt.Errorf("store: failed to read call tree rows: %w", err)
			}
			return nil
		})
		if err != nil {
			return nil, false, err
		}

		sort.SliceStable(level, func(i, j int) bool {
			a, b := level[i].Symbol, level[j].Symbol
			if a.FilePath != b.FilePath {
				return a.FilePath < b.FilePath
			}
			if a.StartLine != b.StartLine {
				return a.StartLine < b.StartLine
			}
			if a.ID != b.ID {
				return a.ID < b.ID
			}
			return level[i].DependencyKind < level[j].DependencyKind
		})

		var nextFrontier []int64
		for _, e := range level {
			if seen[e.Symbol.ID] {
				continue
			}
			seen[e.Symbol.ID] = true
			result = append(result, e)
			nextFrontier = append(nextFrontier, e.Symbol.ID)
			if len(result) > MaxCallTreeEdges {
				break
			}
		}
		frontier = nextFrontier
	}

	if len(result) > MaxCallTreeEdges {
		return result[:MaxCallTreeEdges], true, nil
	}
	return result, false, nil
}

// DeadCodeOptions controls filtering for FindDeadSymbols.
type DeadCodeOptions struct {
	FilePath        string // optional: limit to a specific file path
	Kind            string // optional: filter by symbol kind
	ExcludeExported bool   // if true, skip symbols starting with uppercase (Go exported)
	Limit           int    // max results (default 50)
}

// FindDeadSymbols returns symbols with zero inbound dependency edges and no
// call site anywhere in the repo naming them (outside their own body),
// excluding common entry points (main, init, Init, Main). The call-site check
// keeps symbols alive whose callers the resolver left unlinked on purpose,
// such as methods called on a receiver of unknown type from another module.
func FindDeadSymbols(ctx context.Context, db *DB, repoName string, opts DeadCodeOptions) ([]models.Symbol, error) {
	if opts.Limit <= 0 {
		opts.Limit = 50
	}
	if opts.Limit > 200 {
		opts.Limit = 200
	}

	var conditions []string
	var args []any

	conditions = append(conditions, "s.repo_name = ?")
	args = append(args, repoName)

	conditions = append(conditions, "d.id IS NULL")
	conditions = append(conditions, `NOT EXISTS (
		SELECT 1 FROM call_sites cs
		WHERE cs.repo_name = s.repo_name AND cs.name = s.name AND cs.kind != 'import'
		  AND NOT (cs.file_path = s.file_path AND cs.line BETWEEN s.start_line AND s.end_line))`)
	conditions = append(conditions, "s.name NOT IN ('main', 'init', 'Init', 'Main')")
	conditions = append(conditions, "s.kind NOT IN ('variable', 'import')")

	if opts.FilePath != "" {
		conditions = append(conditions, "s.file_path = ?")
		args = append(args, opts.FilePath)
	}
	if opts.Kind != "" {
		conditions = append(conditions, "s.kind = ?")
		args = append(args, opts.Kind)
	}
	if opts.ExcludeExported {
		conditions = append(conditions, "s.name NOT GLOB '[A-Z]*'")
	}

	args = append(args, opts.Limit)

	query := fmt.Sprintf(
		`SELECT s.id, s.repo_name, s.name, s.qualified_name, s.kind, s.file_path,
		        s.content_hash, s.code_block, s.start_line, s.end_line, s.language, s.indexed_at
		 FROM symbols s
		 LEFT JOIN dependencies d ON d.callee_id = s.id
		 WHERE %s
		 ORDER BY s.file_path, s.start_line
		 LIMIT ?`,
		strings.Join(conditions, " AND "),
	)

	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("store: failed to find dead symbols: %w", err)
	}
	defer rows.Close()

	return scanSymbols(rows)
}

// CountSymbols returns the total number of symbols for a repo.
func CountSymbols(ctx context.Context, db *DB, repoName string) (int64, error) {
	var count int64
	err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM symbols WHERE repo_name = ?`,
		repoName,
	).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("store: failed to count symbols: %w", err)
	}
	return count, nil
}

// GitHunk represents a contiguous block of changed lines for line-range filtering.
type GitHunk struct {
	StartLine int // 1-indexed line number where the change starts
	LineCount int // Number of lines in this change
}

// GetSymbolsByFileAndLines returns symbols whose line ranges intersect with any of the given hunks.
// Used by get_modified_symbols to map git diff hunks to symbols.
func GetSymbolsByFileAndLines(ctx context.Context, db *DB, repoName, filePath string, hunks []GitHunk) ([]models.Symbol, error) {
	if len(hunks) == 0 {
		// No hunks = no changes = no symbols to return.
		return nil, nil
	}

	// Build a query with line range intersection checks for each hunk.
	// A symbol overlaps with a hunk if: symbol.start_line <= hunk.end AND symbol.end_line >= hunk.start
	var conditions []string
	var args []any
	args = append(args, repoName, filePath)

	for _, hunk := range hunks {
		hunkEnd := hunk.StartLine + hunk.LineCount - 1
		conditions = append(conditions, "(start_line <= ? AND end_line >= ?)")
		args = append(args, hunkEnd, hunk.StartLine)
	}

	query := fmt.Sprintf(
		`SELECT id, repo_name, name, qualified_name, kind, file_path,
		        content_hash, code_block, start_line, end_line, language, indexed_at
		 FROM symbols
		 WHERE repo_name = ? AND file_path = ? AND (%s)
		 ORDER BY start_line`,
		strings.Join(conditions, " OR "),
	)

	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("store: failed to get symbols by file and lines: %w", err)
	}
	defer rows.Close()

	return scanSymbols(rows)
}

// CodePatternFilter narrows the candidates of SearchCodePatterns.
type CodePatternFilter struct {
	// Literals must each occur in the code block (ASCII case-insensitive,
	// LIKE wildcards matched literally). Callers pass only substrings that
	// every regex match must contain, so the prefilter never drops a match.
	Literals []string
	// FilePathPrefix keeps symbols whose file path starts with it (literal).
	FilePathPrefix string
	// Kind keeps symbols of this kind.
	Kind string
}

// SymbolCursor is a position in (file_path, start_line, id) order, used to
// page through SearchCodePatterns.
type SymbolCursor struct {
	FilePath  string
	StartLine int
	ID        int64
}

// CursorAfter returns the cursor positioned just after s.
func CursorAfter(s models.Symbol) *SymbolCursor {
	return &SymbolCursor{FilePath: s.FilePath, StartLine: s.StartLine, ID: s.ID}
}

// maxCodePatternPage caps one SearchCodePatterns page.
const maxCodePatternPage = 1000

// SearchCodePatterns returns one page of candidate symbols for Go-side regex
// matching: symbols of repoName passing filter, ordered by file path, start
// line and ID, after the cursor (from the start when nil). limit defaults to
// 50 and is capped at 1000; page with CursorAfter(last result) until a page
// comes back shorter than limit.
func SearchCodePatterns(ctx context.Context, db *DB, repoName string, filter CodePatternFilter, after *SymbolCursor, limit int) ([]models.Symbol, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > maxCodePatternPage {
		limit = maxCodePatternPage
	}

	var b strings.Builder
	b.WriteString(`SELECT id, repo_name, name, qualified_name, kind, file_path,
		       content_hash, code_block, start_line, end_line, language, indexed_at
		FROM symbols
		WHERE repo_name = ?`)
	args := []any{repoName}

	for _, lit := range filter.Literals {
		if lit == "" {
			continue
		}
		b.WriteString(` AND code_block LIKE ? ESCAPE '\'`)
		args = append(args, "%"+EscapeLike(lit)+"%")
	}
	if filter.FilePathPrefix != "" {
		b.WriteString(` AND substr(file_path, 1, length(?)) = ?`)
		args = append(args, filter.FilePathPrefix, filter.FilePathPrefix)
	}
	if filter.Kind != "" {
		b.WriteString(` AND kind = ?`)
		args = append(args, filter.Kind)
	}
	if after != nil {
		b.WriteString(` AND (file_path, start_line, id) > (?, ?, ?)`)
		args = append(args, after.FilePath, after.StartLine, after.ID)
	}
	b.WriteString(` ORDER BY file_path, start_line, id LIMIT ?`)
	args = append(args, limit)

	rows, err := db.QueryContext(ctx, b.String(), args...)
	if err != nil {
		return nil, fmt.Errorf("store: failed to search code patterns: %w", err)
	}
	defer rows.Close()

	return scanSymbols(rows)
}

// scanSymbol scans a single symbol row.
func scanSymbol(row *sql.Row) (*models.Symbol, error) {
	var s models.Symbol
	err := row.Scan(
		&s.ID, &s.RepoName, &s.Name, &s.QualifiedName, &s.Kind, &s.FilePath,
		&s.ContentHash, &s.CodeBlock, &s.StartLine, &s.EndLine, &s.Language, &s.IndexedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSymbolNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("store: failed to scan symbol: %w", err)
	}
	return &s, nil
}

// scanSymbols scans multiple symbol rows.
func scanSymbols(rows *sql.Rows) ([]models.Symbol, error) {
	var symbols []models.Symbol
	for rows.Next() {
		var s models.Symbol
		if err := rows.Scan(
			&s.ID, &s.RepoName, &s.Name, &s.QualifiedName, &s.Kind, &s.FilePath,
			&s.ContentHash, &s.CodeBlock, &s.StartLine, &s.EndLine, &s.Language, &s.IndexedAt,
		); err != nil {
			return nil, fmt.Errorf("store: failed to scan symbol row: %w", err)
		}
		symbols = append(symbols, s)
	}
	return symbols, rows.Err()
}
