package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/context-link-mcp/context-link/internal/store"
	"github.com/context-link-mcp/context-link/pkg/models"
)

// insertRoute is a helper to insert a route into the database.
func insertRoute(t *testing.T, db *store.DB, repo, method, path, filePath, framework, kind string) {
	t.Helper()
	routes := []models.Route{{
		RepoName:       repo,
		Method:         method,
		PathPattern:    path,
		NormalizedPath: path,
		FilePath:       filePath,
		StartLine:      1,
		Framework:      framework,
		Kind:           kind,
	}}
	require.NoError(t, store.BatchInsertRoutes(context.Background(), db, routes))
}

func TestRoutes_NoFilter(t *testing.T) {
	t.Parallel()
	db := openToolTestDB(t)

	insertRoute(t, db, "repo", "GET", "/api/users", "routes.ts", "express", "definition")
	insertRoute(t, db, "repo", "POST", "/api/posts", "routes.ts", "express", "definition")

	handler := routesHandler(db, "repo", "", NewSessionTokenTracker())
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{}

	result, err := handler(context.Background(), req)
	require.NoError(t, err)
	assert.False(t, result.IsError)

	var resp struct {
		RouteCount int `json:"route_count"`
	}
	raw, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	require.NoError(t, json.Unmarshal([]byte(raw.Text), &resp))

	assert.Equal(t, 2, resp.RouteCount)
}

func TestRoutes_MethodFilter(t *testing.T) {
	t.Parallel()
	db := openToolTestDB(t)

	insertRoute(t, db, "repo", "GET", "/api/users", "routes.ts", "express", "definition")
	insertRoute(t, db, "repo", "POST", "/api/users", "routes.ts", "express", "definition")
	insertRoute(t, db, "repo", "DELETE", "/api/users", "routes.ts", "express", "definition")

	handler := routesHandler(db, "repo", "", NewSessionTokenTracker())
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"method": "GET",
	}

	result, err := handler(context.Background(), req)
	require.NoError(t, err)
	assert.False(t, result.IsError)

	var resp struct {
		Routes []struct {
			Method string `json:"method"`
		} `json:"routes"`
	}
	raw, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	require.NoError(t, json.Unmarshal([]byte(raw.Text), &resp))

	assert.Len(t, resp.Routes, 1)
	assert.Equal(t, "GET", resp.Routes[0].Method)
}

func TestRoutes_PathFilter(t *testing.T) {
	t.Parallel()
	db := openToolTestDB(t)

	insertRoute(t, db, "repo", "GET", "/api/users", "routes.ts", "express", "definition")
	insertRoute(t, db, "repo", "GET", "/api/posts", "routes.ts", "express", "definition")
	insertRoute(t, db, "repo", "GET", "/web/home", "routes.ts", "express", "definition")

	handler := routesHandler(db, "repo", "", NewSessionTokenTracker())
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"path": "/api",
	}

	result, err := handler(context.Background(), req)
	require.NoError(t, err)
	assert.False(t, result.IsError)

	var resp struct {
		Routes []struct {
			PathPattern string `json:"path_pattern"`
		} `json:"routes"`
	}
	raw, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	require.NoError(t, json.Unmarshal([]byte(raw.Text), &resp))

	assert.Len(t, resp.Routes, 2, "should match /api substring")
	for _, r := range resp.Routes {
		assert.Contains(t, r.PathPattern, "/api")
	}
}

func TestRoutes_FileFilter(t *testing.T) {
	t.Parallel()
	db := openToolTestDB(t)

	insertRoute(t, db, "repo", "GET", "/api/users", "file1.ts", "express", "definition")
	insertRoute(t, db, "repo", "GET", "/api/posts", "file2.ts", "express", "definition")

	projectRoot := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(projectRoot, "file1.ts"), []byte{}, 0644))

	handler := routesHandler(db, "repo", projectRoot, NewSessionTokenTracker())
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"file_path": "file1.ts",
	}

	result, err := handler(context.Background(), req)
	require.NoError(t, err)
	assert.False(t, result.IsError)

	var resp struct {
		Routes []struct {
			FilePath string `json:"file_path"`
		} `json:"routes"`
	}
	raw, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	require.NoError(t, json.Unmarshal([]byte(raw.Text), &resp))

	assert.Len(t, resp.Routes, 1)
	assert.Equal(t, "file1.ts", resp.Routes[0].FilePath)
}

func TestRoutes_CombinedFilters(t *testing.T) {
	t.Parallel()
	db := openToolTestDB(t)

	insertRoute(t, db, "repo", "GET", "/api/users", "routes.ts", "express", "definition")
	insertRoute(t, db, "repo", "POST", "/api/users", "routes.ts", "express", "definition")
	insertRoute(t, db, "repo", "GET", "/api/posts", "routes.ts", "express", "definition")

	handler := routesHandler(db, "repo", "", NewSessionTokenTracker())
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"method": "GET",
		"path":   "/api/users",
	}

	result, err := handler(context.Background(), req)
	require.NoError(t, err)
	assert.False(t, result.IsError)

	var resp struct {
		Routes []struct {
			Method      string `json:"method"`
			PathPattern string `json:"path_pattern"`
		} `json:"routes"`
	}
	raw, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	require.NoError(t, json.Unmarshal([]byte(raw.Text), &resp))

	assert.Len(t, resp.Routes, 1)
	assert.Equal(t, "GET", resp.Routes[0].Method)
	assert.Equal(t, "/api/users", resp.Routes[0].PathPattern)
}

func TestRoutes_HandlerResolution(t *testing.T) {
	t.Parallel()
	db := openToolTestDB(t)

	// Insert handler symbol
	handlerID := insertSymbol(t, db, "repo", "getUsersHandler", "function", "func getUsersHandler() {}")

	// Insert route with handler
	ctx := context.Background()
	routes := []models.Route{{
		RepoName:        "repo",
		Method:          "GET",
		PathPattern:     "/api/users",
		NormalizedPath:  "/api/users",
		HandlerSymbolID: &handlerID,
		FilePath:        "routes.ts",
		StartLine:       1,
		Framework:       "express",
		Kind:            "definition",
	}}
	require.NoError(t, store.BatchInsertRoutes(ctx, db, routes))

	handler := routesHandler(db, "repo", "", NewSessionTokenTracker())
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{}

	result, err := handler(context.Background(), req)
	require.NoError(t, err)
	assert.False(t, result.IsError)

	var resp struct {
		Routes []struct {
			Handler string `json:"handler"`
		} `json:"routes"`
	}
	raw, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	require.NoError(t, json.Unmarshal([]byte(raw.Text), &resp))

	assert.Len(t, resp.Routes, 1)
	assert.Equal(t, "getUsersHandler", resp.Routes[0].Handler)
}

func TestRoutes_MissingHandler(t *testing.T) {
	t.Parallel()
	db := openToolTestDB(t)

	// Insert route without handler (null handler_symbol_id)
	insertRoute(t, db, "repo", "GET", "/api/users", "routes.ts", "express", "definition")

	handler := routesHandler(db, "repo", "", NewSessionTokenTracker())
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{}

	result, err := handler(context.Background(), req)
	require.NoError(t, err)
	assert.False(t, result.IsError)

	var resp struct {
		Routes []struct {
			Handler string `json:"handler"`
		} `json:"routes"`
	}
	raw, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	require.NoError(t, json.Unmarshal([]byte(raw.Text), &resp))

	assert.Len(t, resp.Routes, 1)
	assert.Empty(t, resp.Routes[0].Handler, "handler should be empty when not set")
}

func TestRoutes_RouteMatching(t *testing.T) {
	t.Parallel()
	db := openToolTestDB(t)

	// Insert definition and call_site
	insertRoute(t, db, "repo", "GET", "/api/users", "routes.ts", "express", "definition")
	insertRoute(t, db, "repo", "GET", "/api/users", "client.ts", "axios", "call_site")

	handler := routesHandler(db, "repo", "", NewSessionTokenTracker())
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{}

	result, err := handler(context.Background(), req)
	require.NoError(t, err)
	assert.False(t, result.IsError)

	var resp struct {
		MatchCount int `json:"match_count"`
		Matches    []struct {
			Confidence float64 `json:"confidence"`
		} `json:"matches"`
	}
	raw, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	require.NoError(t, json.Unmarshal([]byte(raw.Text), &resp))

	assert.GreaterOrEqual(t, resp.MatchCount, 1, "should find at least one match")
	if len(resp.Matches) > 0 {
		assert.Greater(t, resp.Matches[0].Confidence, 0.0, "confidence should be > 0")
	}
}

func TestRoutes_ConfidenceScoring(t *testing.T) {
	t.Parallel()
	db := openToolTestDB(t)

	// Exact match should have confidence 1.0
	insertRoute(t, db, "repo", "GET", "/api/users", "routes.ts", "express", "definition")
	insertRoute(t, db, "repo", "GET", "/api/users", "client.ts", "axios", "call_site")

	handler := routesHandler(db, "repo", "", NewSessionTokenTracker())
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{}

	result, err := handler(context.Background(), req)
	require.NoError(t, err)
	assert.False(t, result.IsError)

	var resp struct {
		Matches []struct {
			Definition struct {
				PathPattern string `json:"path_pattern"`
			} `json:"definition"`
			CallSite struct {
				PathPattern string `json:"path_pattern"`
			} `json:"call_site"`
			Confidence float64 `json:"confidence"`
		} `json:"matches"`
	}
	raw, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	require.NoError(t, json.Unmarshal([]byte(raw.Text), &resp))

	// Exact match should have high confidence
	if len(resp.Matches) > 0 {
		assert.Equal(t, "/api/users", resp.Matches[0].Definition.PathPattern)
		assert.Equal(t, "/api/users", resp.Matches[0].CallSite.PathPattern)
		assert.GreaterOrEqual(t, resp.Matches[0].Confidence, 0.9, "exact match should have high confidence")
	}
}

func TestRoutes_NoRoutes(t *testing.T) {
	t.Parallel()
	db := openToolTestDB(t)

	handler := routesHandler(db, "repo", "", NewSessionTokenTracker())
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{}

	result, err := handler(context.Background(), req)
	require.NoError(t, err)
	assert.False(t, result.IsError)

	var resp struct {
		RouteCount int   `json:"route_count"`
		Routes     []any `json:"routes"`
	}
	raw, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	require.NoError(t, json.Unmarshal([]byte(raw.Text), &resp))

	assert.Equal(t, 0, resp.RouteCount)
	assert.Empty(t, resp.Routes)
}

func TestRoutes_InvalidFile(t *testing.T) {
	t.Parallel()
	db := openToolTestDB(t)

	projectRoot := t.TempDir()

	handler := routesHandler(db, "repo", projectRoot, NewSessionTokenTracker())
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"file_path": "nonexistent.ts",
	}

	result, err := handler(context.Background(), req)
	require.NoError(t, err)
	assert.True(t, result.IsError)

	raw, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, raw.Text, "does not exist")
}

func TestRoutes_TokenSavings(t *testing.T) {
	t.Parallel()
	db := openToolTestDB(t)

	insertRoute(t, db, "repo", "GET", "/api/users", "routes.ts", "express", "definition")
	insertFile(t, db, "repo", "routes.ts", 3000)

	handler := routesHandler(db, "repo", "", NewSessionTokenTracker())
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{}

	result, err := handler(context.Background(), req)
	require.NoError(t, err)
	assert.False(t, result.IsError)

	var resp struct {
		Metadata struct {
			TimingMs           int64  `json:"timing_ms"`
			TokensSavedEst     int64  `json:"tokens_saved_est"`
			CostAvoidedEst     string `json:"cost_avoided_est"`
			SessionTokensSaved int64  `json:"session_tokens_saved"`
		} `json:"metadata"`
	}
	raw, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	require.NoError(t, json.Unmarshal([]byte(raw.Text), &resp))

	assert.GreaterOrEqual(t, resp.Metadata.TimingMs, int64(0))
	assert.GreaterOrEqual(t, resp.Metadata.TokensSavedEst, int64(0))
	assert.NotEmpty(t, resp.Metadata.CostAvoidedEst)
	assert.GreaterOrEqual(t, resp.Metadata.SessionTokensSaved, int64(0))
}
