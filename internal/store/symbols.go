package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/context-link-mcp/context-link/pkg/models"
)

// ErrSymbolNotFound is returned when a symbol record is not found.
var ErrSymbolNotFound = errors.New("symbol not found")

// GetSymbolByName retrieves a symbol by exact name within a repo.
// If multiple symbols share the same name, returns the first match.
func GetSymbolByName(ctx context.Context, db *DB, repoName, name string) (*models.Symbol, error) {
	row := db.QueryRowContext(ctx,
		`SELECT id, repo_name, name, qualified_name, kind, file_path,
		        content_hash, code_block, start_line, end_line, language, indexed_at
		 FROM symbols WHERE repo_name = ? AND name = ? LIMIT 1`,
		repoName, name,
	)

	return scanSymbol(row)
}

// GetSymbolByQualifiedName retrieves a symbol by its fully qualified name.
func GetSymbolByQualifiedName(ctx context.Context, db *DB, repoName, qualifiedName string) (*models.Symbol, error) {
	row := db.QueryRowContext(ctx,
		`SELECT id, repo_name, name, qualified_name, kind, file_path,
		        content_hash, code_block, start_line, end_line, language, indexed_at
		 FROM symbols WHERE repo_name = ? AND qualified_name = ? LIMIT 1`,
		repoName, qualifiedName,
	)

	return scanSymbol(row)
}

// SearchSymbolsByName performs a fuzzy search on symbol names using LIKE.
func SearchSymbolsByName(ctx context.Context, db *DB, repoName, pattern string, limit int) ([]models.Symbol, error) {
	if limit <= 0 {
		limit = 20
	}

	rows, err := db.QueryContext(ctx,
		`SELECT id, repo_name, name, qualified_name, kind, file_path,
		        content_hash, code_block, start_line, end_line, language, indexed_at
		 FROM symbols
		 WHERE repo_name = ? AND (name LIKE ? OR qualified_name LIKE ?)
		 ORDER BY
		   CASE WHEN name = ? THEN 0
		        WHEN qualified_name = ? THEN 1
		        WHEN name LIKE ? THEN 2
		        ELSE 3
		   END
		 LIMIT ?`,
		repoName,
		"%"+pattern+"%", "%"+pattern+"%",
		pattern, pattern,
		pattern+"%",
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
// Uses INSERT OR REPLACE to handle re-indexing of existing symbols.
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
// dependency resolution within a repo.
func GetSymbolNameIndex(ctx context.Context, db *DB, repoName string) (map[string]int64, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id, name, qualified_name FROM symbols WHERE repo_name = ?`,
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
		index[name] = id
		if qualifiedName != name {
			index[qualifiedName] = id
		}
	}
	return index, rows.Err()
}

// ResolveSymbol looks up a symbol by name using a cascading strategy:
// exact name match → qualified name match → fuzzy search.
// Returns ErrSymbolNotFound if no match is found.
func ResolveSymbol(ctx context.Context, db *DB, repoName, name string) (*models.Symbol, error) {
	sym, err := GetSymbolByName(ctx, db, repoName, name)
	if err != nil {
		sym, err = GetSymbolByQualifiedName(ctx, db, repoName, name)
	}
	if err != nil {
		results, searchErr := SearchSymbolsByName(ctx, db, repoName, name, 1)
		if searchErr != nil || len(results) == 0 {
			return nil, ErrSymbolNotFound
		}
		sym = &results[0]
	}
	return sym, nil
}

// GetSymbolWithDependencies retrieves a symbol and its direct dependencies
// (1-hop). Returns the symbol, its dependencies, and their import statements.
func GetSymbolWithDependencies(ctx context.Context, db *DB, repoName, symbolName string, depth int) (*models.Symbol, []models.Symbol, error) {
	sym, err := ResolveSymbol(ctx, db, repoName, symbolName)
	if err != nil {
		return nil, nil, err
	}

	if depth <= 0 {
		return sym, nil, nil
	}

	// Get transitive dependencies via BFS up to requested depth.
	deps, err := getTransitiveDependencies(ctx, db, sym.ID, depth)
	if err != nil {
		return sym, nil, nil // non-fatal
	}

	return sym, deps, nil
}

// getTransitiveDependencies performs BFS traversal of the dependency graph
// up to maxDepth hops, returning all reachable symbols without duplicates.
func getTransitiveDependencies(ctx context.Context, db *DB, symbolID int64, maxDepth int) ([]models.Symbol, error) {
	if maxDepth <= 0 {
		return nil, nil
	}

	seen := map[int64]bool{symbolID: true} // avoid revisiting the root
	var result []models.Symbol
	frontier := []int64{symbolID}

	for depth := 0; depth < maxDepth && len(frontier) > 0; depth++ {
		// Batch query: fetch all dependencies for the entire frontier at once.
		placeholders := make([]string, len(frontier))
		args := make([]any, len(frontier))
		for i, id := range frontier {
			placeholders[i] = "?"
			args[i] = id
		}

		query := fmt.Sprintf(
			`SELECT s.id, s.repo_name, s.name, s.qualified_name, s.kind, s.file_path,
			        s.content_hash, s.code_block, s.start_line, s.end_line, s.language, s.indexed_at
			 FROM dependencies d
			 JOIN symbols s ON s.id = d.callee_id
			 WHERE d.caller_id IN (%s)`,
			strings.Join(placeholders, ","),
		)

		rows, err := db.QueryContext(ctx, query, args...)
		if err != nil {
			return nil, fmt.Errorf("store: failed to get batch dependencies: %w", err)
		}

		syms, err := scanSymbols(rows)
		rows.Close()
		if err != nil {
			return nil, err
		}

		var nextFrontier []int64
		for _, s := range syms {
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

// GetImportsForFile returns all import dependency sources for a file's symbols.
func GetImportsForFile(ctx context.Context, db *DB, repoName, filePath string) ([]string, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT DISTINCT d.kind
		 FROM dependencies d
		 JOIN symbols s ON s.id = d.caller_id
		 WHERE s.repo_name = ? AND s.file_path = ? AND d.kind = 'import'`,
		repoName, filePath,
	)
	if err != nil {
		return nil, fmt.Errorf("store: failed to get imports for %s: %w", filePath, err)
	}
	defer rows.Close()

	var imports []string
	for rows.Next() {
		var imp string
		if err := rows.Scan(&imp); err != nil {
			return nil, fmt.Errorf("store: failed to scan import row: %w", err)
		}
		imports = append(imports, imp)
	}
	return imports, rows.Err()
}

// GetSymbolsByIDs returns symbols by their IDs within a repo, keyed by ID.
// Used to batch-fetch symbol metadata for KNN search results.
func GetSymbolsByIDs(ctx context.Context, db *DB, repoName string, ids []int64) (map[int64]models.Symbol, error) {
	if len(ids) == 0 {
		return make(map[int64]models.Symbol), nil
	}

	// Build parameterized IN clause.
	placeholders := make([]string, len(ids))
	args := make([]any, 0, len(ids)+1)
	args = append(args, repoName)
	for i, id := range ids {
		placeholders[i] = "?"
		args = append(args, id)
	}

	query := fmt.Sprintf(
		`SELECT id, repo_name, name, qualified_name, kind, file_path,
		        content_hash, code_block, start_line, end_line, language, indexed_at
		 FROM symbols WHERE repo_name = ? AND id IN (%s)`,
		strings.Join(placeholders, ","),
	)

	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("store: failed to get symbols by IDs: %w", err)
	}
	defer rows.Close()

	result := make(map[int64]models.Symbol, len(ids))
	for rows.Next() {
		var s models.Symbol
		if err := rows.Scan(
			&s.ID, &s.RepoName, &s.Name, &s.QualifiedName, &s.Kind, &s.FilePath,
			&s.ContentHash, &s.CodeBlock, &s.StartLine, &s.EndLine, &s.Language, &s.IndexedAt,
		); err != nil {
			return nil, fmt.Errorf("store: failed to scan symbol row: %w", err)
		}
		result[s.ID] = s
	}
	return result, rows.Err()
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

// GetCallTree performs BFS traversal of the dependency graph from a root symbol.
// direction must be "callees" (forward: what does this call?) or "callers" (reverse: what calls this?).
// Returns a flat list of edges with depth levels. Hard-capped at 100 edges.
func GetCallTree(ctx context.Context, db *DB, symbolID int64, direction string, maxDepth int) ([]CallTreeEdge, error) {
	if maxDepth <= 0 {
		return nil, nil
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

	const maxEdges = 100
	seen := map[int64]bool{symbolID: true}
	var result []CallTreeEdge
	frontier := []int64{symbolID}

	for depth := 1; depth <= maxDepth && len(frontier) > 0 && len(result) < maxEdges; depth++ {
		placeholders := make([]string, len(frontier))
		args := make([]any, len(frontier))
		for i, id := range frontier {
			placeholders[i] = "?"
			args[i] = id
		}

		query := fmt.Sprintf(
			`SELECT s.id, s.repo_name, s.name, s.qualified_name, s.kind, s.file_path,
			        s.content_hash, s.code_block, s.start_line, s.end_line, s.language, s.indexed_at,
			        d.kind
			 FROM dependencies d
			 JOIN symbols s ON s.id = d.%s
			 WHERE d.%s IN (%s)`,
			targetCol, frontierCol, strings.Join(placeholders, ","),
		)

		rows, err := db.QueryContext(ctx, query, args...)
		if err != nil {
			return nil, fmt.Errorf("store: failed to get call tree edges: %w", err)
		}

		var nextFrontier []int64
		for rows.Next() {
			var s models.Symbol
			var depKind string
			if err := rows.Scan(
				&s.ID, &s.RepoName, &s.Name, &s.QualifiedName, &s.Kind, &s.FilePath,
				&s.ContentHash, &s.CodeBlock, &s.StartLine, &s.EndLine, &s.Language, &s.IndexedAt,
				&depKind,
			); err != nil {
				rows.Close()
				return nil, fmt.Errorf("store: failed to scan call tree row: %w", err)
			}
			if seen[s.ID] {
				continue
			}
			seen[s.ID] = true
			result = append(result, CallTreeEdge{
				Depth:          depth,
				Symbol:         s,
				DependencyKind: depKind,
			})
			nextFrontier = append(nextFrontier, s.ID)
			if len(result) >= maxEdges {
				break
			}
		}
		rows.Close()

		frontier = nextFrontier
	}

	return result, nil
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

