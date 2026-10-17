package store

import (
	"context"
	"fmt"
)

// CountFilesByLanguage returns, per language, the number of files that
// contributed at least one symbol.
func CountFilesByLanguage(ctx context.Context, db *DB, repoName string) (map[string]int, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT language, COUNT(DISTINCT file_path) FROM symbols
		 WHERE repo_name = ? GROUP BY language`, repoName,
	)
	if err != nil {
		return nil, fmt.Errorf("store: failed to count files by language for %s: %w", repoName, err)
	}
	defer rows.Close()

	out := make(map[string]int)
	for rows.Next() {
		var lang string
		var n int
		if err := rows.Scan(&lang, &n); err != nil {
			return nil, fmt.Errorf("store: failed to scan language count: %w", err)
		}
		out[lang] = n
	}
	return out, rows.Err()
}
