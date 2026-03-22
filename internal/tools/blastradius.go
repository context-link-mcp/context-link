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

// RegisterBlastRadiusTool registers the get_blast_radius MCP tool.
func RegisterBlastRadiusTool(s *server.MCPServer, db *store.DB, repoName string, timeout time.Duration, tracker *SessionTokenTracker) {
	tool := mcp.NewTool("get_blast_radius",
		mcp.WithDescription(
			"Shows everything affected by changing a symbol. "+
				"Traverses callers (reverse dependencies) via BFS and groups results by file. "+
				"Use this before refactoring to understand the impact of a change. "+
				"Example: get_blast_radius(symbol_name='hashFile', depth=2).",
		),
		mcp.WithString("symbol_name",
			mcp.Required(),
			mcp.Description("The name or qualified name of the symbol to analyze."),
		),
		mcp.WithNumber("depth",
			mcp.Description("Max traversal depth for callers (default: 2, max: 3)."),
		),
	)
	s.AddTool(tool, WithTimeout(timeout, blastRadiusHandler(db, repoName, tracker)))
}

// blastRadiusAffected is one affected symbol in the blast radius response.
type blastRadiusAffected struct {
	Name          string `json:"name"`
	QualifiedName string `json:"qualified_name"`
	Kind          string `json:"kind"`
	Depth         int    `json:"depth"`
	DepKind       string `json:"dep_kind"`
}

// blastRadiusHandler returns the MCP tool handler for get_blast_radius.
func blastRadiusHandler(db *store.DB, repoName string, tracker *SessionTokenTracker) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		start := time.Now()

		symbolName, err := req.RequireString("symbol_name")
		if err != nil || strings.TrimSpace(symbolName) == "" {
			return mcp.NewToolResultError("get_blast_radius: 'symbol_name' parameter is required and must not be empty"), nil
		}

		depth := req.GetInt("depth", 2)
		if depth < 1 {
			depth = 1
		}
		if depth > 3 {
			depth = 3
		}

		sym, err := store.ResolveSymbol(ctx, db, repoName, symbolName)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf(
				"get_blast_radius: symbol %q not found in repository %q. Try using semantic_search_symbols first.",
				symbolName, repoName,
			)), nil
		}

		// BFS through callers using the existing call tree infrastructure.
		edges, err := store.GetCallTree(ctx, db, sym.ID, "callers", depth)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("get_blast_radius: traversal failed: %v", err)), nil
		}

		// Group affected symbols by file path and count per depth level.
		affectedByFile := map[string][]blastRadiusAffected{}
		byDepth := map[int]int{}
		fileSet := map[string]struct{}{sym.FilePath: {}}

		for _, e := range edges {
			entry := blastRadiusAffected{
				Name:          e.Symbol.Name,
				QualifiedName: e.Symbol.QualifiedName,
				Kind:          e.Symbol.Kind,
				Depth:         e.Depth,
				DepKind:       e.DependencyKind,
			}
			affectedByFile[e.Symbol.FilePath] = append(affectedByFile[e.Symbol.FilePath], entry)
			byDepth[e.Depth]++
			fileSet[e.Symbol.FilePath] = struct{}{}
		}

		// Token savings.
		var totalFileBytes int
		for fp := range fileSet {
			if f, err := store.GetFileByPath(ctx, db, repoName, fp); err == nil {
				totalFileBytes += int(f.SizeBytes)
			}
		}
		responseBytes := len(edges) * 80
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
			"affected_files":  affectedByFile,
			"total_affected":  len(edges),
			"files_affected":  len(affectedByFile),
			"by_depth":        byDepth,
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
