package store

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/context-link-mcp/context-link/pkg/models"
)

// TestNormalizePath_Frameworks covers parameter syntaxes master normalised
// wrongly: Flask <id> / <int:id> were left as-is, FastAPI {p:path} was left
// as-is (it is a multi-segment catch-all), and Express regex constraints left
// "(\d+)" behind.
func TestNormalizePath_Frameworks(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name, in, want string
	}{
		{"flask plain", "/users/<user_id>", "/users/:param"},
		{"flask converter", "/users/<int:user_id>/posts/<uuid:pid>", "/users/:param/posts/:param"},
		{"flask converter args", "/x/<string(length=2):code>", "/x/:param"},
		{"flask path", "/files/<path:subpath>", "/files/*"},
		{"fastapi plain", "/items/{item_id}", "/items/:param"},
		{"fastapi typed", "/items/{item_id:int}", "/items/:param"},
		{"fastapi path", "/files/{file_path:path}", "/files/*"},
		{"express", "/a/:id/b/:other", "/a/:param/b/:param"},
		{"express regex", `/a/:id(\d+)`, "/a/:param"},
		{"express optional", "/a/:id?", "/a/:param"},
		{"express repeat", "/a/:rest*", "/a/*"},
		{"express star", "/static/*", "/static/*"},
		{"express named star", "/static/*splat", "/static/*"},
		{"gin param", "/users/:id/books/:book", "/users/:param/books/:param"},
		{"gin catch-all", "/static/*filepath", "/static/*"},
		{"echo star", "/files/*", "/files/*"},
		{"chi plain", "/users/{userID}", "/users/:param"},
		{"chi regex", "/users/{id:[0-9]+}", "/users/:param"},
		{"chi regex with escape", `/articles/{slug:[a-z-]+}/{n:\d+}`, "/articles/:param/:param"},
		{"chi regex with quantifier braces", "/codes/{code:[A-Z]{3}}", "/codes/:param"},
		{"go 1.22 wildcard", "/files/{path...}", "/files/*"},
		{"go 1.22 exact root", "/{$}", "/"},
		{"go 1.22 exact subtree", "/posts/{id}/{$}", "/posts/:param/"},
		{"spring plain", "/users/{id}", "/users/:param"},
		{"spring regex", `/users/{id:\d+}`, "/users/:param"},
		{"spring regex with braces", `/codes/{code:[a-z]{2}}/{n:\d{1,3}}`, "/codes/:param/:param"},
		{"spring catch-all", "/files/{*path}", "/files/*"},
		{"spring double star", "/static/**", "/static/*"},
		{"spring double star mid-path", "/a/**/b", "/a/*/b"},
		{"literal", "/static/path", "/static/path"},
		{"root", "/", "/"},
		{"angle bracket not a param", "/a<b", "/a<b"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, NormalizePath(tt.in))
		})
	}
}

// TestMatchConfidence covers segment-aware matching. On master the root "/"
// prefix-matched every path at 0.5, "/api" matched "/apiv2", and catch-alls
// never matched deeper paths.
func TestMatchConfidence(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		def, call string
		want      float64
	}{
		{"exact", "/api/users", "/api/users", 1.0},
		{"param", "/api/users/:param", "/api/users/42", 0.9},
		{"trailing slash", "/api/users/", "/api/users", 0.9},
		{"segment prefix", "/api", "/api/users", 0.5},
		{"segment prefix reversed", "/api/users/:param", "/api/users", 0.5},
		{"root does not prefix everything", "/", "/api/users", 0},
		{"call root does not prefix everything", "/api/users", "/", 0},
		{"root exact", "/", "/", 1.0},
		{"not segment aligned", "/api", "/apiv2/users", 0},
		{"catch-all one segment", "/files/*", "/files/a.txt", 0.9},
		{"catch-all many segments", "/files/*", "/files/a/b/c.txt", 0.9},
		{"catch-all needs a segment", "/files/*", "/files", 0.5},
		{"different path", "/api/users", "/api/posts", 0},
		{"different length params", "/a/:param", "/a/b/c", 0.5},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			def := models.Route{Method: "GET", NormalizedPath: tt.def}
			call := models.Route{Method: "GET", NormalizedPath: tt.call}
			assert.Equal(t, tt.want, matchConfidence(def, call))
		})
	}
}

// TestMatchRoutesDetailed_Truncated checks that the comparison cap is
// reported. On master MatchRoutes silently compared only 200 definitions.
func TestMatchRoutesDetailed_Truncated(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	ctx := context.Background()

	var routes []models.Route
	for i := 0; i <= matchRouteLimit; i++ {
		p := fmt.Sprintf("/r/%03d", i)
		routes = append(routes, models.Route{
			RepoName: "repo", Method: "GET", PathPattern: p, NormalizedPath: p,
			FilePath: "routes.ts", StartLine: i + 1, Framework: "express", Kind: "definition",
		})
	}
	routes = append(routes, models.Route{
		RepoName: "repo", Method: "GET", PathPattern: "/r/000", NormalizedPath: "/r/000",
		FilePath: "client.ts", StartLine: 1, Framework: "axios", Kind: "call_site",
	})
	require.NoError(t, BatchInsertRoutes(ctx, db, routes))

	res, err := MatchRoutesDetailed(ctx, db, "repo")
	require.NoError(t, err)
	assert.True(t, res.Truncated)
	require.Len(t, res.Matches, 1)
	assert.Equal(t, "/r/000", res.Matches[0].Definition.NormalizedPath)

	// Below the cap nothing is truncated.
	small := setupTestDB(t)
	require.NoError(t, BatchInsertRoutes(ctx, small, routes[len(routes)-2:]))
	res, err = MatchRoutesDetailed(ctx, small, "repo")
	require.NoError(t, err)
	assert.False(t, res.Truncated)
}
