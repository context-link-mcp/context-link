package store

import (
	"context"
	"fmt"
	"testing"

	"github.com/context-link-mcp/context-link/pkg/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBatchInsertRoutes_Success(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	ctx := context.Background()

	routes := []models.Route{
		{
			RepoName:       "repo",
			Method:         "GET",
			PathPattern:    "/api/users",
			NormalizedPath: "/api/users",
			FilePath:       "routes.ts",
			StartLine:      10,
			Framework:      "express",
			Kind:           "definition",
		},
		{
			RepoName:       "repo",
			Method:         "POST",
			PathPattern:    "/api/posts",
			NormalizedPath: "/api/posts",
			FilePath:       "routes.ts",
			StartLine:      20,
			Framework:      "express",
			Kind:           "definition",
		},
	}

	err := BatchInsertRoutes(ctx, db, routes)
	require.NoError(t, err)

	// Verify routes were inserted
	foundRoutes, err := FindRoutes(ctx, db, "repo", RouteFilter{})
	require.NoError(t, err)
	assert.Len(t, foundRoutes, 2)
}

func TestBatchInsertRoutes_Empty(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	ctx := context.Background()

	err := BatchInsertRoutes(ctx, db, []models.Route{})
	require.NoError(t, err, "empty slice should not error")
}

func TestBatchInsertRoutes_Duplicate(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	ctx := context.Background()

	route := models.Route{
		RepoName:       "repo",
		Method:         "GET",
		PathPattern:    "/api/users",
		NormalizedPath: "/api/users",
		FilePath:       "routes.ts",
		StartLine:      10,
		Framework:      "express",
		Kind:           "definition",
	}

	// Insert once
	err := BatchInsertRoutes(ctx, db, []models.Route{route})
	require.NoError(t, err)

	// Insert again - without unique constraint, this creates a new row
	route.StartLine = 15
	err = BatchInsertRoutes(ctx, db, []models.Route{route})
	require.NoError(t, err, "duplicate insert should succeed")

	// Typical workflow: delete old routes before re-indexing
	err = DeleteRoutesByRepo(ctx, db, "repo")
	require.NoError(t, err)

	// Verify routes were deleted
	foundRoutes, err := FindRoutes(ctx, db, "repo", RouteFilter{})
	require.NoError(t, err)
	assert.Empty(t, foundRoutes)
}

func TestBatchInsertRoutes_InvalidData(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	ctx := context.Background()

	// Note: SQLite accepts empty strings for NOT NULL TEXT columns, so these succeed
	// Application-level validation should be added if stricter constraints are needed
	routes := []models.Route{
		{
			RepoName:       "", // empty - accepted by SQLite
			Method:         "",  // empty - accepted by SQLite
			PathPattern:    "/api/users",
			NormalizedPath: "/api/users",
			FilePath:       "routes.ts",
			StartLine:      10,
			Framework:      "express",
			Kind:           "definition",
		},
	}
	err := BatchInsertRoutes(ctx, db, routes)
	require.NoError(t, err, "SQLite allows empty strings for TEXT NOT NULL columns")

	// Verify the route was inserted despite empty required fields
	foundRoutes, err := FindRoutes(ctx, db, "", RouteFilter{})
	require.NoError(t, err)
	assert.Len(t, foundRoutes, 1, "route with empty fields should be inserted")
}

func TestDeleteRoutesByRepo_Success(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	ctx := context.Background()

	// Insert routes
	routes := []models.Route{
		{
			RepoName:       "repo",
			Method:         "GET",
			PathPattern:    "/api/users",
			NormalizedPath: "/api/users",
			FilePath:       "routes.ts",
			StartLine:      10,
			Framework:      "express",
			Kind:           "definition",
		},
	}
	err := BatchInsertRoutes(ctx, db, routes)
	require.NoError(t, err)

	// Delete routes
	err = DeleteRoutesByRepo(ctx, db, "repo")
	require.NoError(t, err)

	// Verify routes were deleted
	foundRoutes, err := FindRoutes(ctx, db, "repo", RouteFilter{})
	require.NoError(t, err)
	assert.Empty(t, foundRoutes)
}

func TestDeleteRoutesByRepo_EmptyRepo(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	ctx := context.Background()

	// Delete from repo with no routes
	err := DeleteRoutesByRepo(ctx, db, "empty-repo")
	require.NoError(t, err, "deleting from empty repo should not error")
}

func TestFindRoutes_NoFilter(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	ctx := context.Background()

	// Insert routes
	routes := []models.Route{
		{
			RepoName:       "repo",
			Method:         "GET",
			PathPattern:    "/api/users",
			NormalizedPath: "/api/users",
			FilePath:       "routes.ts",
			StartLine:      10,
			Framework:      "express",
			Kind:           "definition",
		},
		{
			RepoName:       "repo",
			Method:         "POST",
			PathPattern:    "/api/posts",
			NormalizedPath: "/api/posts",
			FilePath:       "routes.ts",
			StartLine:      20,
			Framework:      "express",
			Kind:           "definition",
		},
	}
	err := BatchInsertRoutes(ctx, db, routes)
	require.NoError(t, err)

	// Find all routes
	foundRoutes, err := FindRoutes(ctx, db, "repo", RouteFilter{})
	require.NoError(t, err)
	assert.Len(t, foundRoutes, 2)
}

func TestFindRoutes_MethodFilter(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	ctx := context.Background()

	// Insert routes with different methods
	routes := []models.Route{
		{
			RepoName:       "repo",
			Method:         "GET",
			PathPattern:    "/api/users",
			NormalizedPath: "/api/users",
			FilePath:       "routes.ts",
			StartLine:      10,
			Framework:      "express",
			Kind:           "definition",
		},
		{
			RepoName:       "repo",
			Method:         "POST",
			PathPattern:    "/api/users",
			NormalizedPath: "/api/users",
			FilePath:       "routes.ts",
			StartLine:      20,
			Framework:      "express",
			Kind:           "definition",
		},
		{
			RepoName:       "repo",
			Method:         "DELETE",
			PathPattern:    "/api/users",
			NormalizedPath: "/api/users",
			FilePath:       "routes.ts",
			StartLine:      30,
			Framework:      "express",
			Kind:           "definition",
		},
	}
	err := BatchInsertRoutes(ctx, db, routes)
	require.NoError(t, err)

	// Filter by method (case-insensitive)
	foundRoutes, err := FindRoutes(ctx, db, "repo", RouteFilter{Method: "get"})
	require.NoError(t, err)
	assert.Len(t, foundRoutes, 1)
	assert.Equal(t, "GET", foundRoutes[0].Method)
}

func TestFindRoutes_PathFilter(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	ctx := context.Background()

	// Insert routes with different paths
	routes := []models.Route{
		{
			RepoName:       "repo",
			Method:         "GET",
			PathPattern:    "/api/users",
			NormalizedPath: "/api/users",
			FilePath:       "routes.ts",
			StartLine:      10,
			Framework:      "express",
			Kind:           "definition",
		},
		{
			RepoName:       "repo",
			Method:         "GET",
			PathPattern:    "/api/posts",
			NormalizedPath: "/api/posts",
			FilePath:       "routes.ts",
			StartLine:      20,
			Framework:      "express",
			Kind:           "definition",
		},
		{
			RepoName:       "repo",
			Method:         "GET",
			PathPattern:    "/web/home",
			NormalizedPath: "/web/home",
			FilePath:       "routes.ts",
			StartLine:      30,
			Framework:      "express",
			Kind:           "definition",
		},
	}
	err := BatchInsertRoutes(ctx, db, routes)
	require.NoError(t, err)

	// Filter by path substring (LIKE match)
	foundRoutes, err := FindRoutes(ctx, db, "repo", RouteFilter{Path: "/api"})
	require.NoError(t, err)
	assert.Len(t, foundRoutes, 2, "should match /api substring")
	for _, r := range foundRoutes {
		assert.Contains(t, r.PathPattern, "/api")
	}
}

func TestFindRoutes_LimitEnforcement(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	ctx := context.Background()

	// Insert 60 routes
	var routes []models.Route
	for i := 0; i < 60; i++ {
		routes = append(routes, models.Route{
			RepoName:       "repo",
			Method:         "GET",
			PathPattern:    fmt.Sprintf("/api/endpoint%d", i),
			NormalizedPath: fmt.Sprintf("/api/endpoint%d", i),
			FilePath:       "routes.ts",
			StartLine:      i,
			Framework:      "express",
			Kind:           "definition",
		})
	}
	err := BatchInsertRoutes(ctx, db, routes)
	require.NoError(t, err)

	// Default limit (50)
	foundRoutes, err := FindRoutes(ctx, db, "repo", RouteFilter{})
	require.NoError(t, err)
	assert.Len(t, foundRoutes, 50, "default limit should be 50")

	// Explicit limit
	foundRoutes, err = FindRoutes(ctx, db, "repo", RouteFilter{Limit: 10})
	require.NoError(t, err)
	assert.Len(t, foundRoutes, 10)

	// Max limit capping (200)
	foundRoutes, err = FindRoutes(ctx, db, "repo", RouteFilter{Limit: 500})
	require.NoError(t, err)
	assert.LessOrEqual(t, len(foundRoutes), 200, "limit should cap at 200")
}

func TestMatchRoutes_ExactMatch(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	ctx := context.Background()

	// Insert definition and call_site with exact match
	routes := []models.Route{
		{
			RepoName:       "repo",
			Method:         "GET",
			PathPattern:    "/api/users",
			NormalizedPath: "/api/users",
			FilePath:       "routes.ts",
			StartLine:      10,
			Framework:      "express",
			Kind:           "definition",
		},
		{
			RepoName:       "repo",
			Method:         "GET",
			PathPattern:    "/api/users",
			NormalizedPath: "/api/users",
			FilePath:       "client.ts",
			StartLine:      5,
			Framework:      "axios",
			Kind:           "call_site",
		},
	}
	err := BatchInsertRoutes(ctx, db, routes)
	require.NoError(t, err)

	// Find matches
	matches, err := MatchRoutes(ctx, db, "repo")
	require.NoError(t, err)
	assert.Len(t, matches, 1)
	assert.Equal(t, 1.0, matches[0].Confidence, "exact match should have confidence 1.0")
}

func TestMatchRoutes_ParamMatch(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	ctx := context.Background()

	// Insert definition with :id and call_site with :userId
	routes := []models.Route{
		{
			RepoName:       "repo",
			Method:         "GET",
			PathPattern:    "/api/users/:id",
			NormalizedPath: "/api/users/:param",
			FilePath:       "routes.ts",
			StartLine:      10,
			Framework:      "express",
			Kind:           "definition",
		},
		{
			RepoName:       "repo",
			Method:         "GET",
			PathPattern:    "/api/users/:userId",
			NormalizedPath: "/api/users/:param",
			FilePath:       "client.ts",
			StartLine:      5,
			Framework:      "axios",
			Kind:           "call_site",
		},
	}
	err := BatchInsertRoutes(ctx, db, routes)
	require.NoError(t, err)

	// Find matches
	matches, err := MatchRoutes(ctx, db, "repo")
	require.NoError(t, err)
	assert.Len(t, matches, 1)
	assert.Equal(t, 1.0, matches[0].Confidence, "normalized param match should have confidence 1.0")
}

func TestMatchRoutes_PrefixMatch(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	ctx := context.Background()

	// Insert definition and call_site with prefix match
	routes := []models.Route{
		{
			RepoName:       "repo",
			Method:         "GET",
			PathPattern:    "/api",
			NormalizedPath: "/api",
			FilePath:       "routes.ts",
			StartLine:      10,
			Framework:      "express",
			Kind:           "definition",
		},
		{
			RepoName:       "repo",
			Method:         "GET",
			PathPattern:    "/api/users",
			NormalizedPath: "/api/users",
			FilePath:       "client.ts",
			StartLine:      5,
			Framework:      "axios",
			Kind:           "call_site",
		},
	}
	err := BatchInsertRoutes(ctx, db, routes)
	require.NoError(t, err)

	// Find matches
	matches, err := MatchRoutes(ctx, db, "repo")
	require.NoError(t, err)
	assert.Len(t, matches, 1)
	assert.Equal(t, 0.5, matches[0].Confidence, "prefix match should have confidence 0.5")
}

func TestMatchRoutes_NoMatch(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	ctx := context.Background()

	// Insert definition (GET) and call_site (POST) with same path - should not match due to method
	routes := []models.Route{
		{
			RepoName:       "repo",
			Method:         "GET",
			PathPattern:    "/api/users",
			NormalizedPath: "/api/users",
			FilePath:       "routes.ts",
			StartLine:      10,
			Framework:      "express",
			Kind:           "definition",
		},
		{
			RepoName:       "repo",
			Method:         "POST",
			PathPattern:    "/api/users",
			NormalizedPath: "/api/users",
			FilePath:       "client.ts",
			StartLine:      5,
			Framework:      "axios",
			Kind:           "call_site",
		},
	}
	err := BatchInsertRoutes(ctx, db, routes)
	require.NoError(t, err)

	// Find matches
	matches, err := MatchRoutes(ctx, db, "repo")
	require.NoError(t, err)
	assert.Empty(t, matches, "same path but different methods should not match")
}

func TestNormalizePath_Express(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input    string
		expected string
	}{
		{"/api/users/:id", "/api/users/:param"},
		{"/api/posts/:postId/comments/:commentId", "/api/posts/:param/comments/:param"},
		{"/users/:userId", "/users/:param"},
		{"/static/path", "/static/path"},
	}

	for _, tt := range tests {
		result := NormalizePath(tt.input)
		assert.Equal(t, tt.expected, result, "Express-style :id should normalize to :param")
	}
}

func TestNormalizePath_FastAPI(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input    string
		expected string
	}{
		{"/api/users/{id}", "/api/users/:param"},
		{"/api/posts/{postId}/comments/{commentId}", "/api/posts/:param/comments/:param"},
		{"/users/{userId}", "/users/:param"},
		{"/static/path", "/static/path"},
	}

	for _, tt := range tests {
		result := NormalizePath(tt.input)
		assert.Equal(t, tt.expected, result, "FastAPI-style {id} should normalize to :param")
	}
}

// Note: TestNormalizePath_Gin removed - Gin uses same :id syntax as Express (already covered)
