package store

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMigration013_ForcesReindexAndKeepsRows checks that migration 013 blanks
// every file hash, so the next index rewrites FTS rows and embeddings with the
// new search code, and deletes nothing.
func TestMigration013_ForcesReindexAndKeepsRows(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openMigratedBefore(t, "013")

	for _, p := range []string{"a.go", "b.go"} {
		_, err := db.ExecContext(ctx,
			`INSERT INTO files (repo_name, path, content_hash) VALUES ('r', ?, 'h')`, p)
		require.NoError(t, err)
	}
	res, err := db.ExecContext(ctx, `
		INSERT INTO symbols (repo_name, name, qualified_name, kind, file_path, content_hash, code_block, start_line, end_line, language)
		VALUES ('r', 'validateToken', 'validateToken', 'function', 'a.go', 'c', 'code', 1, 2, 'go')`)
	require.NoError(t, err)
	symID, err := res.LastInsertId()
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `INSERT INTO memories (symbol_id, repo_name, note) VALUES (?, 'r', 'keep me')`, symID)
	require.NoError(t, err)

	require.NoError(t, Migrate(db))

	count := func(query string, args ...any) int {
		var n int
		require.NoError(t, db.QueryRowContext(ctx, query, args...).Scan(&n))
		return n
	}
	assert.Equal(t, 2, count(`SELECT COUNT(*) FROM files WHERE content_hash = ''`),
		"every file hash is blanked so the next index re-indexes every file")
	assert.Equal(t, 1, count(`SELECT COUNT(*) FROM symbols WHERE id = ?`, symID))
	assert.Equal(t, 1, count(`SELECT COUNT(*) FROM memories WHERE symbol_id = ? AND is_stale = 0`, symID))
}
