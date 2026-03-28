package store

import (
	"context"
	"fmt"
	"strings"
)

// FTSSymbol represents a symbol entry for the FTS5 index.
type FTSSymbol struct {
	SymbolID      int64
	RepoName      string
	Name          string
	QualifiedName string
	Kind          string
	Signature     string
	ExtraKeywords string
}

// FTSResult represents a single FTS5 search result with BM25 score.
type FTSResult struct {
	SymbolID int64
	Rank     float64 // BM25 rank (lower = more relevant)
}

// BatchInsertFTSSymbols inserts multiple FTS5 entries in a single transaction.
func BatchInsertFTSSymbols(ctx context.Context, db *DB, symbols []FTSSymbol) error {
	if len(symbols) == 0 {
		return nil
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: failed to begin FTS transaction: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	stmt, err := tx.PrepareContext(ctx,
		`INSERT INTO fts_symbols (symbol_id, repo_name, name, qualified_name, kind, signature, extra_keywords)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
	)
	if err != nil {
		return fmt.Errorf("store: failed to prepare FTS insert: %w", err)
	}
	defer stmt.Close()

	for _, s := range symbols {
		_, err := stmt.ExecContext(ctx,
			s.SymbolID, s.RepoName, s.Name, s.QualifiedName, s.Kind, s.Signature, s.ExtraKeywords,
		)
		if err != nil {
			return fmt.Errorf("store: failed to insert FTS symbol %s: %w", s.QualifiedName, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: failed to commit FTS batch: %w", err)
	}
	return nil
}

// DeleteFTSByRepo removes all FTS entries for a repository.
func DeleteFTSByRepo(ctx context.Context, db *DB, repoName string) error {
	_, err := db.ExecContext(ctx,
		`DELETE FROM fts_symbols WHERE repo_name = ?`,
		repoName,
	)
	if err != nil {
		return fmt.Errorf("store: failed to delete FTS entries for repo %s: %w", repoName, err)
	}
	return nil
}

// DeleteFTSByFile removes FTS entries for symbols belonging to a specific file.
func DeleteFTSByFile(ctx context.Context, db *DB, repoName, filePath string) error {
	_, err := db.ExecContext(ctx,
		`DELETE FROM fts_symbols WHERE symbol_id IN (
			SELECT id FROM symbols WHERE repo_name = ? AND file_path = ?
		)`,
		repoName, filePath,
	)
	if err != nil {
		return fmt.Errorf("store: failed to delete FTS entries for file %s/%s: %w", repoName, filePath, err)
	}
	return nil
}

// sanitizeFTSQuery escapes FTS5 special characters to prevent query syntax errors.
// FTS5 has special syntax for: AND, OR, NOT, parentheses, quotes, NEAR, *, ^
// Strategy: wrap the entire query in double quotes for literal phrase mode.
// This disables FTS5 operators but ensures no syntax errors from user input.
// Internal double quotes are escaped by doubling them.
func sanitizeFTSQuery(query string) string {
	// Escape internal quotes: " → ""
	escaped := strings.ReplaceAll(query, `"`, `""`)
	// Wrap in quotes for literal phrase mode
	return `"` + escaped + `"`
}

// FTSSearch performs a full-text search using FTS5 BM25 ranking.
// Returns results ordered by relevance (best match first).
func FTSSearch(ctx context.Context, db *DB, repoName, query string, limit int) ([]FTSResult, error) {
	if limit <= 0 {
		limit = 10
	}

	// Sanitize query to prevent FTS5 syntax errors from special characters.
	ftsQuery := sanitizeFTSQuery(query)

	rows, err := db.QueryContext(ctx,
		`SELECT symbol_id, rank
		 FROM fts_symbols
		 WHERE fts_symbols MATCH ? AND repo_name = ?
		 ORDER BY rank
		 LIMIT ?`,
		ftsQuery, repoName, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("store: FTS search failed: %w", err)
	}
	defer rows.Close()

	var results []FTSResult
	for rows.Next() {
		var r FTSResult
		if err := rows.Scan(&r.SymbolID, &r.Rank); err != nil {
			return nil, fmt.Errorf("store: failed to scan FTS result: %w", err)
		}
		results = append(results, r)
	}
	return results, rows.Err()
}
