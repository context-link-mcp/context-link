package store

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/context-link-mcp/context-link/pkg/models"
)

// TestMigration012_KeepsRowsAndAllowsSameNameSymbols upgrades a DB holding
// symbols, an edge, a memory, an embedding and a route (foreign keys on, as
// Open sets them) and checks that the new unique key deletes nothing, lets
// same-name symbols at different lines coexist, and forces a re-index.
func TestMigration012_KeepsRowsAndAllowsSameNameSymbols(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openMigratedBefore(t, "012")

	var fk int
	require.NoError(t, db.QueryRowContext(ctx, `PRAGMA foreign_keys`).Scan(&fk))
	require.Equal(t, 1, fk, "the test must run with foreign keys enforced")

	_, err := db.ExecContext(ctx,
		`INSERT INTO files (repo_name, path, content_hash) VALUES ('r', 'A.java', 'h1')`)
	require.NoError(t, err)
	insertSym := func(qn, name string, line int) int64 {
		res, err := db.ExecContext(ctx, `
			INSERT INTO symbols (repo_name, name, qualified_name, kind, file_path, content_hash, code_block, start_line, end_line, language)
			VALUES ('r', ?, ?, 'method', 'A.java', 'c', 'code', ?, ?, 'java')`, name, qn, line, line+1)
		require.NoError(t, err)
		id, err := res.LastInsertId()
		require.NoError(t, err)
		return id
	}
	callee := insertSym("A.f", "f", 2)
	caller := insertSym("A.run", "run", 10)

	_, err = db.ExecContext(ctx, `INSERT INTO dependencies (caller_id, callee_id, kind) VALUES (?, ?, 'call')`, caller, callee)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `INSERT INTO memories (symbol_id, repo_name, note) VALUES (?, 'r', 'keep me')`, callee)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `INSERT INTO vec_symbols (symbol_id, repo_name, embedding) VALUES (?, 'r', x'00000000')`, callee)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `
		INSERT INTO routes (repo_name, method, path_pattern, normalized_path, handler_symbol_id, file_path, start_line, framework)
		VALUES ('r', 'GET', '/a', '/a', ?, 'A.java', 2, 'test')`, callee)
	require.NoError(t, err)

	require.NoError(t, Migrate(db))

	count := func(query string, args ...any) int {
		var n int
		require.NoError(t, db.QueryRowContext(ctx, query, args...).Scan(&n))
		return n
	}
	assert.Equal(t, 2, count(`SELECT COUNT(*) FROM symbols`))
	assert.Equal(t, 1, count(`SELECT COUNT(*) FROM dependencies WHERE caller_id = ? AND callee_id = ?`, caller, callee))
	assert.Equal(t, 1, count(`SELECT COUNT(*) FROM memories WHERE symbol_id = ? AND is_stale = 0`, callee))
	assert.Equal(t, 1, count(`SELECT COUNT(*) FROM vec_symbols WHERE symbol_id = ?`, callee))
	assert.Equal(t, 1, count(`SELECT COUNT(*) FROM routes WHERE handler_symbol_id = ?`, callee))
	assert.Equal(t, 1, count(`SELECT COUNT(*) FROM files WHERE content_hash = ''`),
		"every file hash is blanked so the next index re-indexes with the new queries")

	// An overload of A.f at another line is now kept next to the first one.
	overload := insertSym("A.f", "f", 5)
	assert.NotEqual(t, callee, overload)
	assert.Equal(t, 2, count(`SELECT COUNT(*) FROM symbols WHERE qualified_name = 'A.f'`))

	// The same symbol at the same line is still unique.
	_, err = db.ExecContext(ctx, `
		INSERT INTO symbols (repo_name, name, qualified_name, kind, file_path, content_hash, code_block, start_line, end_line, language)
		VALUES ('r', 'f', 'A.f', 'method', 'A.java', 'c', 'code', 2, 3, 'java')`)
	assert.Error(t, err, "(repo, qualified_name, file, start_line) stays unique")

	// Lookups by name are deterministic: the lowest start line wins.
	got, err := GetSymbolByQualifiedName(ctx, db, "r", "A.f")
	require.NoError(t, err)
	assert.Equal(t, callee, got.ID)
	got, err = GetSymbolByName(ctx, db, "r", "f")
	require.NoError(t, err)
	assert.Equal(t, callee, got.ID)
}

// TestReplaceFileIndex_KeepsSameNameSymbols checks that every same-name
// symbol of a file is stored (M1): Go's several init() and Java overloads.
func TestReplaceFileIndex_KeepsSameNameSymbols(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := setupTestDB(t)

	sym := func(qn, name string, line int) models.Symbol {
		return models.Symbol{
			RepoName: "r", Name: name, QualifiedName: qn, Kind: "function", FilePath: "a.go",
			ContentHash: "c", CodeBlock: "code", StartLine: line, EndLine: line, Language: "go",
		}
	}
	syms := []models.Symbol{sym("init", "init", 3), sym("init", "init", 7), sym("A.f", "f", 10), sym("A.f", "f", 12)}
	require.NoError(t, ReplaceFileIndex(ctx, db, &models.File{RepoName: "r", Path: "a.go", ContentHash: "h"}, syms, nil))

	stored, err := GetSymbolsByFile(ctx, db, "r", "a.go")
	require.NoError(t, err)
	var lines []int
	for _, s := range stored {
		lines = append(lines, s.StartLine)
	}
	assert.Equal(t, []int{3, 7, 10, 12}, lines)
}
