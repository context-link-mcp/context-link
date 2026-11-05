package store

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMigration017_ReindexDjangoRoutes checks that 017 blanks only Python
// files' hashes, so the next index extracts Django routes without
// re-indexing the rest, and keeps existing routes.
func TestMigration017_ReindexDjangoRoutes(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openMigratedBefore(t, "017")

	for _, p := range []string{"blog/urls.py", "app.py", "main.go", "web/server.js", "notes.pyi", "py/readme.md"} {
		_, err := db.ExecContext(ctx,
			`INSERT INTO files (repo_name, path, content_hash) VALUES ('r', ?, 'hash')`, p)
		require.NoError(t, err)
	}
	_, err := db.ExecContext(ctx,
		`INSERT INTO routes (repo_name, method, path_pattern, normalized_path, file_path, start_line, framework)
		 VALUES ('r', 'GET', '/x', '/x', 'app.py', 3, 'flask')`)
	require.NoError(t, err)

	applyMigrationFile(t, db, "017_reindex_django_routes.sql")

	hashes, err := GetFileHashIndex(ctx, db, "r")
	require.NoError(t, err)
	assert.Equal(t, map[string]string{
		"blog/urls.py": "", "app.py": "",
		"main.go": "hash", "web/server.js": "hash", "notes.pyi": "hash", "py/readme.md": "hash",
	}, hashes)

	routes, err := FindRoutes(ctx, db, "r", RouteFilter{})
	require.NoError(t, err)
	assert.Len(t, routes, 1, "no route is deleted")
}
