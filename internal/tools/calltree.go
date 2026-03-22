package tools

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/context-link-mcp/context-link/internal/store"
	"github.com/context-link-mcp/context-link/pkg/models"
)

// RegisterCallTreeTool registers the get_call_tree MCP tool.
func RegisterCallTreeTool(s *server.MCPServer, db *store.DB, repoName string, timeout time.Duration, tracker *SessionTokenTracker) {
	tool := mcp.NewTool("get_call_tree",
		mcp.WithDescription(
			"Traverses the dependency graph to show a call hierarchy. "+
				"Use direction='callees' to see what a symbol calls, or direction='callers' to see what calls it. "+
				"Returns a flat list of edges with depth levels — no code bodies. "+
				"Example: symbol_name='Walk', direction='callees', depth=2.",
		),
		mcp.WithString("symbol_name",
			mcp.Required(),
			mcp.Description("The root symbol name or qualified name."),
		),
		mcp.WithString("direction",
			mcp.Description("'callees' (what it calls, default) or 'callers' (what calls it)."),
		),
		mcp.WithNumber("depth",
			mcp.Description("Max traversal depth (default: 1, max: 3)."),
		),
	)
	s.AddTool(tool, WithTimeout(timeout, callTreeHandler(db, repoName, tracker)))
}

// callTreeEdgeResult is one edge in the call tree response.
type callTreeEdgeResult struct {
	Depth         int    `json:"depth"`
	Name          string `json:"name"`
	QualifiedName string `json:"qualified_name"`
	Kind          string `json:"kind"`
	FilePath      string `json:"file_path"`
	StartLine     int    `json:"start_line"`
	DepKind       string `json:"dep_kind"`
}

// callTreeHandler returns the MCP tool handler for get_call_tree.
func callTreeHandler(db *store.DB, repoName string, tracker *SessionTokenTracker) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		start := time.Now()

		symbolName, err := req.RequireString("symbol_name")
		if err != nil || strings.TrimSpace(symbolName) == "" {
			return mcp.NewToolResultError("get_call_tree: 'symbol_name' parameter is required and must not be empty"), nil
		}

		direction := strings.TrimSpace(req.GetString("direction", "callees"))
		if direction != "callees" && direction != "callers" {
			return mcp.NewToolResultError("get_call_tree: 'direction' must be 'callees' or 'callers'"), nil
		}

		depth := req.GetInt("depth", 1)
		if depth < 0 {
			depth = 0
		}
		if depth > 3 {
			depth = 3
		}

		sym, err := store.ResolveSymbol(ctx, db, repoName, symbolName)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf(
				"get_call_tree: symbol %q not found in repository %q. Try using semantic_search_symbols first.",
				symbolName, repoName,
			)), nil
		}

		edges, err := store.GetCallTree(ctx, db, sym.ID, direction, depth)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("get_call_tree: traversal failed: %v", err)), nil
		}

		edgeResults := make([]callTreeEdgeResult, len(edges))
		fileSet := map[string]struct{}{sym.FilePath: {}}
		for i, e := range edges {
			edgeResults[i] = callTreeEdgeResult{
				Depth:         e.Depth,
				Name:          e.Symbol.Name,
				QualifiedName: e.Symbol.QualifiedName,
				Kind:          e.Symbol.Kind,
				FilePath:      e.Symbol.FilePath,
				StartLine:     e.Symbol.StartLine,
				DepKind:       e.DependencyKind,
			}
			fileSet[e.Symbol.FilePath] = struct{}{}
		}

		// Token savings: agent would read all traversed files; we return metadata only.
		var totalFileBytes int
		for fp := range fileSet {
			if f, err := store.GetFileByPath(ctx, db, repoName, fp); err == nil {
				totalFileBytes += int(f.SizeBytes)
			}
		}
		responseBytes := len(edgeResults) * 80
		savings := ComputeSavings(totalFileBytes, responseBytes)
		sessionTotal := tracker.Record(savings.Saved)
		timingMs := time.Since(start).Milliseconds()

		result := map[string]any{
			"root": map[string]any{
				"name":           sym.Name,
				"qualified_name": sym.QualifiedName,
				"kind":           sym.Kind,
				"file_path":      sym.FilePath,
			},
			"edges":      edgeResults,
			"edge_count": len(edgeResults),
			"direction":  direction,
			"metadata": models.ToolMetadata{
				TimingMs:           timingMs,
				TokensSavedEst:     savings.Saved,
				CostAvoidedEst:     FormatCost(savings.Saved),
				SessionTokensSaved: sessionTotal,
				SessionCostAvoided: FormatCost(sessionTotal),
			},
		}

		return mcp.NewToolResultJSON(result)
	}
}
