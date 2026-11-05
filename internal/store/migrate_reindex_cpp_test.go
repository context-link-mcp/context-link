package store

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMigration014_ReindexesOnlyCAndCppFiles checks that migration 014
// blanks the hash of C and C++ files only (by symbol language or by
// extension), so the next index re-indexes them with the new qualified names,
// keeps every other hash, and deletes nothing.
func TestMigration014_ReindexesOnlyCAndCppFiles(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openMigratedBefore(t, "014")

	files := []struct {
		repo, path, lang string // lang "" means the file has no symbols
		blanked          bool
	}{
		{"r", "src/foo.cpp", "cpp", true},
		{"r", "src/foo.hpp", "cpp", true},
		{"r", "src/lib.c", "c", true},
		{"r", "include/lib.h", "c", true},
		{"r", "src/ext.cc", "", true},     // no symbols, C++ extension
		{"r", "src/ext.CXX", "", true},    // extension match ignores case
		{"r", "src/odd.inl", "cpp", true}, // C++ symbols, unusual extension
		{"r", "main.go", "go", false},
		{"r", "app.py", "python", false},
		{"r", "README.md", "", false},
		{"r", "src/cache.cpp.go", "go", false}, // only the final extension counts
		{"other", "main.go", "go", false},
		{"other", "src/foo.cpp", "cpp", true},
	}
	var symIDs []int64
	for _, f := range files {
		_, err := db.ExecContext(ctx,
			`INSERT INTO files (repo_name, path, content_hash) VALUES (?, ?, 'h')`, f.repo, f.path)
		require.NoError(t, err)
		if f.lang == "" {
			continue
		}
		res, err := db.ExecContext(ctx, `
			INSERT INTO symbols (repo_name, name, qualified_name, kind, file_path, content_hash, code_block, start_line, end_line, language)
			VALUES (?, 'bar', 'bar', 'method', ?, 'c', 'code', 1, 2, ?)`, f.repo, f.path, f.lang)
		require.NoError(t, err)
		id, err := res.LastInsertId()
		require.NoError(t, err)
		symIDs = append(symIDs, id)
	}
	_, err := db.ExecContext(ctx, `INSERT INTO memories (symbol_id, repo_name, note) VALUES (?, 'r', 'keep me')`, symIDs[0])
	require.NoError(t, err)

	// Only 014: later migrations (015 re-indexes Go files) blank other hashes.
	applyMigrationFile(t, db, "014_reindex_cpp_qualified_names.sql")

	for _, f := range files {
		var hash string
		require.NoError(t, db.QueryRowContext(ctx,
			`SELECT content_hash FROM files WHERE repo_name = ? AND path = ?`, f.repo, f.path).Scan(&hash))
		if f.blanked {
			assert.Empty(t, hash, "%s:%s must be re-indexed", f.repo, f.path)
		} else {
			assert.Equal(t, "h", hash, "%s:%s must keep its hash", f.repo, f.path)
		}
	}

	count := func(query string, args ...any) int {
		var n int
		require.NoError(t, db.QueryRowContext(ctx, query, args...).Scan(&n))
		return n
	}
	assert.Equal(t, len(files), count(`SELECT COUNT(*) FROM files`))
	assert.Equal(t, len(symIDs), count(`SELECT COUNT(*) FROM symbols`))
	assert.Equal(t, 1, count(`SELECT COUNT(*) FROM memories WHERE symbol_id = ? AND is_stale = 0`, symIDs[0]))
}
