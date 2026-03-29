package tools

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCallTree_Callees(t *testing.T) {
	t.Parallel()
	db := openToolTestDB(t)

	// Setup: root -> callee1, callee2
	rootID := insertSymbol(t, db, "repo", "root", "function", "func root() {}")
	callee1ID := insertSymbol(t, db, "repo", "callee1", "function", "func callee1() {}")
	callee2ID := insertSymbol(t, db, "repo", "callee2", "function", "func callee2() {}")
	insertDependency(t, db, rootID, callee1ID, "call")
	insertDependency(t, db, rootID, callee2ID, "call")

	handler := callTreeHandler(db, "repo", NewSessionTokenTracker())
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"symbol_name": "root",
		"direction":   "callees",
		"depth":       1,
	}

	result, err := handler(context.Background(), req)
	require.NoError(t, err)
	assert.False(t, result.IsError)

	var resp struct {
		Direction string `json:"direction"`
		EdgeCount int    `json:"edge_count"`
		Edges     []struct {
			Name  string `json:"name"`
			Depth int    `json:"depth"`
		} `json:"edges"`
	}
	raw, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	require.NoError(t, json.Unmarshal([]byte(raw.Text), &resp))

	assert.Equal(t, "callees", resp.Direction)
	assert.Equal(t, 2, resp.EdgeCount)
	names := []string{resp.Edges[0].Name, resp.Edges[1].Name}
	assert.Contains(t, names, "callee1")
	assert.Contains(t, names, "callee2")
}

func TestCallTree_Callers(t *testing.T) {
	t.Parallel()
	db := openToolTestDB(t)

	// Setup: caller1, caller2 -> target
	targetID := insertSymbol(t, db, "repo", "target", "function", "func target() {}")
	caller1ID := insertSymbol(t, db, "repo", "caller1", "function", "func caller1() {}")
	caller2ID := insertSymbol(t, db, "repo", "caller2", "function", "func caller2() {}")
	insertDependency(t, db, caller1ID, targetID, "call")
	insertDependency(t, db, caller2ID, targetID, "call")

	handler := callTreeHandler(db, "repo", NewSessionTokenTracker())
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"symbol_name": "target",
		"direction":   "callers",
		"depth":       1,
	}

	result, err := handler(context.Background(), req)
	require.NoError(t, err)
	assert.False(t, result.IsError)

	var resp struct {
		Direction string `json:"direction"`
		EdgeCount int    `json:"edge_count"`
		Edges     []struct {
			Name string `json:"name"`
		} `json:"edges"`
	}
	raw, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	require.NoError(t, json.Unmarshal([]byte(raw.Text), &resp))

	assert.Equal(t, "callers", resp.Direction)
	assert.Equal(t, 2, resp.EdgeCount)
	names := []string{resp.Edges[0].Name, resp.Edges[1].Name}
	assert.Contains(t, names, "caller1")
	assert.Contains(t, names, "caller2")
}

func TestCallTree_DepthZero(t *testing.T) {
	t.Parallel()
	db := openToolTestDB(t)

	// Setup: root -> callee
	rootID := insertSymbol(t, db, "repo", "root", "function", "func root() {}")
	calleeID := insertSymbol(t, db, "repo", "callee", "function", "func callee() {}")
	insertDependency(t, db, rootID, calleeID, "call")

	handler := callTreeHandler(db, "repo", NewSessionTokenTracker())
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"symbol_name": "root",
		"direction":   "callees",
		"depth":       0,
	}

	result, err := handler(context.Background(), req)
	require.NoError(t, err)
	assert.False(t, result.IsError)

	var resp struct {
		EdgeCount int `json:"edge_count"`
		Edges     []any `json:"edges"`
	}
	raw, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	require.NoError(t, json.Unmarshal([]byte(raw.Text), &resp))

	assert.Equal(t, 0, resp.EdgeCount, "depth 0 should return no edges")
	assert.Empty(t, resp.Edges)
}

func TestCallTree_DepthOne(t *testing.T) {
	t.Parallel()
	db := openToolTestDB(t)

	// Setup: root -> level1 -> level2
	rootID := insertSymbol(t, db, "repo", "root", "function", "func root() {}")
	level1ID := insertSymbol(t, db, "repo", "level1", "function", "func level1() {}")
	level2ID := insertSymbol(t, db, "repo", "level2", "function", "func level2() {}")
	insertDependency(t, db, rootID, level1ID, "call")
	insertDependency(t, db, level1ID, level2ID, "call")

	handler := callTreeHandler(db, "repo", NewSessionTokenTracker())
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"symbol_name": "root",
		"direction":   "callees",
		"depth":       1,
	}

	result, err := handler(context.Background(), req)
	require.NoError(t, err)
	assert.False(t, result.IsError)

	var resp struct {
		EdgeCount int `json:"edge_count"`
		Edges     []struct {
			Name  string `json:"name"`
			Depth int    `json:"depth"`
		} `json:"edges"`
	}
	raw, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	require.NoError(t, json.Unmarshal([]byte(raw.Text), &resp))

	assert.Equal(t, 1, resp.EdgeCount, "depth 1 should return only level1")
	assert.Equal(t, "level1", resp.Edges[0].Name)
	assert.Equal(t, 1, resp.Edges[0].Depth)
}

func TestCallTree_DepthThree(t *testing.T) {
	t.Parallel()
	db := openToolTestDB(t)

	// Setup: root -> l1 -> l2 -> l3 -> l4
	rootID := insertSymbol(t, db, "repo", "root", "function", "func root() {}")
	l1ID := insertSymbol(t, db, "repo", "l1", "function", "func l1() {}")
	l2ID := insertSymbol(t, db, "repo", "l2", "function", "func l2() {}")
	l3ID := insertSymbol(t, db, "repo", "l3", "function", "func l3() {}")
	l4ID := insertSymbol(t, db, "repo", "l4", "function", "func l4() {}")
	insertDependency(t, db, rootID, l1ID, "call")
	insertDependency(t, db, l1ID, l2ID, "call")
	insertDependency(t, db, l2ID, l3ID, "call")
	insertDependency(t, db, l3ID, l4ID, "call")

	handler := callTreeHandler(db, "repo", NewSessionTokenTracker())
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"symbol_name": "root",
		"direction":   "callees",
		"depth":       3,
	}

	result, err := handler(context.Background(), req)
	require.NoError(t, err)
	assert.False(t, result.IsError)

	var resp struct {
		EdgeCount int `json:"edge_count"`
		Edges     []struct {
			Name  string `json:"name"`
			Depth int    `json:"depth"`
		} `json:"edges"`
	}
	raw, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	require.NoError(t, json.Unmarshal([]byte(raw.Text), &resp))

	assert.Equal(t, 3, resp.EdgeCount, "should return l1, l2, l3 (max depth 3)")
	depths := []int{resp.Edges[0].Depth, resp.Edges[1].Depth, resp.Edges[2].Depth}
	assert.Contains(t, depths, 1)
	assert.Contains(t, depths, 2)
	assert.Contains(t, depths, 3)

	// Should not reach l4
	names := []string{resp.Edges[0].Name, resp.Edges[1].Name, resp.Edges[2].Name}
	assert.NotContains(t, names, "l4")
}

func TestCallTree_InvalidDirection(t *testing.T) {
	t.Parallel()
	db := openToolTestDB(t)

	insertSymbol(t, db, "repo", "root", "function", "func root() {}")

	handler := callTreeHandler(db, "repo", NewSessionTokenTracker())
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"symbol_name": "root",
		"direction":   "invalid",
		"depth":       1,
	}

	result, err := handler(context.Background(), req)
	require.NoError(t, err)
	assert.True(t, result.IsError, "should return error for invalid direction")

	raw, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, raw.Text, "direction", "error should mention direction")
	assert.Contains(t, raw.Text, "callees", "error should mention valid options")
	assert.Contains(t, raw.Text, "callers", "error should mention valid options")
}

func TestCallTree_CircularCall(t *testing.T) {
	t.Parallel()
	db := openToolTestDB(t)

	// Setup: a -> b -> a (circular)
	aID := insertSymbol(t, db, "repo", "a", "function", "func a() {}")
	bID := insertSymbol(t, db, "repo", "b", "function", "func b() {}")
	insertDependency(t, db, aID, bID, "call")
	insertDependency(t, db, bID, aID, "call")

	handler := callTreeHandler(db, "repo", NewSessionTokenTracker())
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"symbol_name": "a",
		"direction":   "callees",
		"depth":       3,
	}

	result, err := handler(context.Background(), req)
	require.NoError(t, err)
	assert.False(t, result.IsError)

	var resp struct {
		EdgeCount int `json:"edge_count"`
	}
	raw, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	require.NoError(t, json.Unmarshal([]byte(raw.Text), &resp))

	// BFS should handle circular deps (visit each node once)
	assert.LessOrEqual(t, resp.EdgeCount, 1, "should not duplicate in circular dependency")
}

func TestCallTree_SymbolNotFound(t *testing.T) {
	t.Parallel()
	db := openToolTestDB(t)

	handler := callTreeHandler(db, "repo", NewSessionTokenTracker())
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"symbol_name": "nonexistent",
		"direction":   "callees",
		"depth":       1,
	}

	result, err := handler(context.Background(), req)
	require.NoError(t, err)
	assert.True(t, result.IsError)

	raw, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, raw.Text, "not found")
	assert.Contains(t, raw.Text, "semantic_search_symbols")
}

func TestCallTree_EmptyGraph(t *testing.T) {
	t.Parallel()
	db := openToolTestDB(t)

	// Insert symbol with no dependencies
	insertSymbol(t, db, "repo", "isolated", "function", "func isolated() {}")

	handler := callTreeHandler(db, "repo", NewSessionTokenTracker())
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"symbol_name": "isolated",
		"direction":   "callees",
		"depth":       1,
	}

	result, err := handler(context.Background(), req)
	require.NoError(t, err)
	assert.False(t, result.IsError)

	var resp struct {
		EdgeCount int   `json:"edge_count"`
		Edges     []any `json:"edges"`
	}
	raw, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	require.NoError(t, json.Unmarshal([]byte(raw.Text), &resp))

	assert.Equal(t, 0, resp.EdgeCount, "isolated symbol has no callees")
	assert.Empty(t, resp.Edges)
}

func TestCallTree_EdgeCount(t *testing.T) {
	t.Parallel()
	db := openToolTestDB(t)

	// Setup: root -> (a, b, c)
	rootID := insertSymbol(t, db, "repo", "root", "function", "func root() {}")
	aID := insertSymbol(t, db, "repo", "a", "function", "func a() {}")
	bID := insertSymbol(t, db, "repo", "b", "function", "func b() {}")
	cID := insertSymbol(t, db, "repo", "c", "function", "func c() {}")
	insertDependency(t, db, rootID, aID, "call")
	insertDependency(t, db, rootID, bID, "call")
	insertDependency(t, db, rootID, cID, "call")

	handler := callTreeHandler(db, "repo", NewSessionTokenTracker())
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"symbol_name": "root",
		"direction":   "callees",
		"depth":       1,
	}

	result, err := handler(context.Background(), req)
	require.NoError(t, err)
	assert.False(t, result.IsError)

	var resp struct {
		EdgeCount int `json:"edge_count"`
		Edges     []struct {
			Name string `json:"name"`
		} `json:"edges"`
	}
	raw, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	require.NoError(t, json.Unmarshal([]byte(raw.Text), &resp))

	assert.Equal(t, 3, resp.EdgeCount)
	assert.Len(t, resp.Edges, 3)
	names := []string{resp.Edges[0].Name, resp.Edges[1].Name, resp.Edges[2].Name}
	assert.Contains(t, names, "a")
	assert.Contains(t, names, "b")
	assert.Contains(t, names, "c")
}
