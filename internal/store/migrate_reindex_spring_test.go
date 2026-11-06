package store

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMigration016_ReindexesOnlyJavaAndKotlinFiles checks that migration 016
// blanks the hash of Java and Kotlin files only (by symbol language or by
// extension), so the next index extracts their Spring routes, keeps every
// other hash, and deletes nothing.
func TestMigration016_ReindexesOnlyJavaAndKotlinFiles(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openMigratedBefore(t, "016")

	files := []struct {
		repo, path, lang string // lang "" means the file has no symbols
		blanked          bool
	}{
		{"r", "src/UserController.java", "java", true},
		{"r", "src/UserController.kt", "kotlin", true},
		{"r", "build.gradle.kts", "", true}, // no symbols, Kotlin script
		{"r", "src/Empty.java", "", true},   // no symbols, Java extension
		{"r", "src/Odd.KT", "", true},       // extension match ignores case
		{"r", "src/Gen.jav", "java", true},  // Java symbols, unusual extension
		{"r", "main.go", "go", false},
		{"r", "app.py", "python", false},
		{"r", "src/foo.cpp", "cpp", false},
		{"r", "README.md", "", false},
		{"r", "src/kt.java.go", "go", false}, // only the final extension counts
		{"other", "main.go", "go", false},
		{"other", "src/App.java", "java", true},
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
			VALUES (?, 'get', 'C.get', 'method', ?, 'c', 'code', 1, 2, ?)`, f.repo, f.path, f.lang)
		require.NoError(t, err)
		id, err := res.LastInsertId()
		require.NoError(t, err)
		symIDs = append(symIDs, id)
	}
	_, err := db.ExecContext(ctx, `INSERT INTO memories (symbol_id, repo_name, note) VALUES (?, 'r', 'keep me')`, symIDs[0])
	require.NoError(t, err)
	_, err = db.ExecContext(ctx,
		`INSERT INTO routes (repo_name, method, path_pattern, normalized_path, file_path, start_line, framework)
		 VALUES ('r', 'GET', '/x', '/x', 'app.py', 3, 'flask')`)
	require.NoError(t, err)

	// Only 016: later migrations may blank other hashes.
	applyMigrationFile(t, db, "016_reindex_spring_routes.sql")

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
	assert.Equal(t, 1, count(`SELECT COUNT(*) FROM routes`))
	assert.Equal(t, 1, count(`SELECT COUNT(*) FROM memories WHERE symbol_id = ? AND is_stale = 0`, symIDs[0]))
}
