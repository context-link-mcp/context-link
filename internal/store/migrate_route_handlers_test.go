package store

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/context-link-mcp/context-link/pkg/models"
)

// TestMigration015_RouteHandlerNames checks that 015 keeps existing routes
// (with empty handler names) and blanks only Go files' hashes, so the next
// index extracts Go routes without re-indexing the rest.
func TestMigration015_RouteHandlerNames(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openMigratedBefore(t, "015")

	for _, p := range []string{"main.go", "app.py", "web/server.js"} {
		_, err := db.ExecContext(ctx,
			`INSERT INTO files (repo_name, path, content_hash) VALUES ('r', ?, 'hash')`, p)
		require.NoError(t, err)
	}
	_, err := db.ExecContext(ctx,
		`INSERT INTO routes (repo_name, method, path_pattern, normalized_path, file_path, start_line, framework)
		 VALUES ('r', 'GET', '/x', '/x', 'app.py', 3, 'flask')`)
	require.NoError(t, err)

	require.NoError(t, Migrate(db))

	hashes, err := GetFileHashIndex(ctx, db, "r")
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"main.go": "", "app.py": "hash", "web/server.js": "hash"}, hashes)

	routes, err := FindRoutes(ctx, db, "r", RouteFilter{})
	require.NoError(t, err)
	require.Len(t, routes, 1)
	assert.Equal(t, "", routes[0].HandlerName)

	unlinked, err := GetUnlinkedRoutes(ctx, db, "r")
	require.NoError(t, err)
	assert.Empty(t, unlinked, "a route without a handler name is not relinked")
}

// TestUnlinkedRoutesAndSetRouteHandlers covers the relink queries: only named,
// unlinked definitions of the repo are returned, and links are set by ID.
func TestUnlinkedRoutesAndSetRouteHandlers(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openMigratedBefore(t, "999")

	res, err := db.ExecContext(ctx,
		`INSERT INTO symbols (repo_name, name, qualified_name, kind, file_path, content_hash, code_block, start_line, end_line, language)
		 VALUES ('r', 'List', 'List', 'function', 'h/h.go', 'c', 'func List() {}', 3, 3, 'go')`)
	require.NoError(t, err)
	symID, err := res.LastInsertId()
	require.NoError(t, err)

	route := func(repo, path, handler, qualifier, kind string, linked *int64) models.Route {
		return models.Route{
			RepoName: repo, Method: "GET", PathPattern: path, NormalizedPath: path,
			HandlerSymbolID: linked, HandlerName: handler, HandlerQualifier: qualifier,
			FilePath: "main.go", StartLine: 1, Framework: "gin", Kind: kind,
		}
	}
	require.NoError(t, BatchInsertRoutes(ctx, db, []models.Route{
		route("r", "/a", "List", "handlers", "definition", nil), // the only one to relink
		route("r", "/b", "", "", "definition", nil),             // inline handler
		route("r", "/c", "List", "", "call_site", nil),          // not a definition
		route("r", "/d", "List", "", "definition", &symID),      // already linked
		route("other", "/e", "List", "", "definition", nil),     // another repo
	}))

	unlinked, err := GetUnlinkedRoutes(ctx, db, "r")
	require.NoError(t, err)
	require.Len(t, unlinked, 1)
	assert.Equal(t, "List", unlinked[0].HandlerName)
	assert.Equal(t, "handlers", unlinked[0].HandlerQualifier)

	require.NoError(t, SetRouteHandlers(ctx, db, "r", map[int64]int64{unlinked[0].ID: symID}))
	unlinked, err = GetUnlinkedRoutes(ctx, db, "r")
	require.NoError(t, err)
	assert.Empty(t, unlinked)

	routes, err := FindRoutes(ctx, db, "r", RouteFilter{Path: "/a"})
	require.NoError(t, err)
	require.Len(t, routes, 1)
	require.NotNil(t, routes[0].HandlerSymbolID)
	assert.Equal(t, symID, *routes[0].HandlerSymbolID)
	assert.Equal(t, "List", routes[0].HandlerName)
	assert.Equal(t, "handlers", routes[0].HandlerQualifier)
}
