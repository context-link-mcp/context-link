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

// FTSSearch performs a full-text search using FTS5 BM25 ranking.
// Returns results ordered by relevance (best match first).
func FTSSearch(ctx context.Context, db *DB, repoName, query string, limit int) ([]FTSResult, error) {
	if limit <= 0 {
		limit = 10
	}

	// Sanitize query for FTS5: escape double quotes.
	sanitized := strings.ReplaceAll(query, "\"", "\"\"")
	// Use OR to match any token (broadens recall for natural-language queries).
	tokens := strings.Fields(sanitized)
	if len(tokens) == 0 {
		return nil, nil
	}
	ftsQuery := "\"" + strings.Join(tokens, "\" OR \"") + "\""

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
