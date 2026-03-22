package tools

import (
	"context"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/context-link-mcp/context-link/internal/store"
	"github.com/context-link-mcp/context-link/pkg/models"
)

// RegisterRoutesTool registers the find_http_routes MCP tool.
func RegisterRoutesTool(s *server.MCPServer, db *store.DB, repoName string, timeout time.Duration, tracker *SessionTokenTracker) {
	tool := mcp.NewTool("find_http_routes",
		mcp.WithDescription(
			"Discovers HTTP route definitions and call sites in the codebase. "+
				"Supports Express, Gin, FastAPI, Flask, and similar frameworks. "+
				"Matches route definitions to their call sites with confidence scoring. "+
				"Example: find_http_routes(method='GET', path='/api/users').",
		),
		mcp.WithString("method",
			mcp.Description("Optional: filter by HTTP method (GET, POST, PUT, DELETE, PATCH)."),
		),
		mcp.WithString("path",
			mcp.Description("Optional: filter routes by path substring (e.g., '/api/users')."),
		),
		mcp.WithString("file_path",
			mcp.Description("Optional: limit search to a specific file path."),
		),
	)
	s.AddTool(tool, WithTimeout(timeout, routesHandler(db, repoName, tracker)))
}

// routeEntry is one route in the response.
type routeEntry struct {
	Method      string `json:"method"`
	PathPattern string `json:"path_pattern"`
	Handler     string `json:"handler,omitempty"`
	FilePath    string `json:"file_path"`
	StartLine   int    `json:"start_line"`
	Framework   string `json:"framework"`
	Kind        string `json:"kind"`
}

// routeMatchEntry is one matched pair in the response.
type routeMatchEntry struct {
	Definition routeEntry `json:"definition"`
	CallSite   routeEntry `json:"call_site"`
	Confidence float64    `json:"confidence"`
}

// routesHandler returns the MCP tool handler for find_http_routes.
func routesHandler(db *store.DB, repoName string, tracker *SessionTokenTracker) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		start := time.Now()

		filter := store.RouteFilter{
			Method:   req.GetString("method", ""),
			Path:     req.GetString("path", ""),
			FilePath: req.GetString("file_path", ""),
			Limit:    100,
		}

		routes, err := store.FindRoutes(ctx, db, repoName, filter)
		if err != nil {
			return mcp.NewToolResultError("find_http_routes: " + err.Error()), nil
		}

		// Build route entries and look up handler symbol names.
		entries := make([]routeEntry, len(routes))
		for i, r := range routes {
			var handler string
			if r.HandlerSymbolID != nil {
				if sym, err := store.GetSymbolByID(ctx, db, *r.HandlerSymbolID); err == nil {
					handler = sym.QualifiedName
				}
			}
			entries[i] = routeEntry{
				Method:      r.Method,
				PathPattern: r.PathPattern,
				Handler:     handler,
				FilePath:    r.FilePath,
				StartLine:   r.StartLine,
				Framework:   r.Framework,
				Kind:        r.Kind,
			}
		}

		// Find matches between definitions and call sites.
		matches, _ := store.MatchRoutes(ctx, db, repoName)
		var matchEntries []routeMatchEntry
		for _, m := range matches {
			matchEntries = append(matchEntries, routeMatchEntry{
				Definition: routeEntry{
					Method:      m.Definition.Method,
					PathPattern: m.Definition.PathPattern,
					FilePath:    m.Definition.FilePath,
					StartLine:   m.Definition.StartLine,
					Framework:   m.Definition.Framework,
					Kind:        m.Definition.Kind,
				},
				CallSite: routeEntry{
					Method:      m.CallSite.Method,
					PathPattern: m.CallSite.PathPattern,
					FilePath:    m.CallSite.FilePath,
					StartLine:   m.CallSite.StartLine,
					Framework:   m.CallSite.Framework,
					Kind:        m.CallSite.Kind,
				},
				Confidence: m.Confidence,
			})
		}

		// Token savings.
		fileSet := map[string]struct{}{}
		for _, r := range routes {
			fileSet[r.FilePath] = struct{}{}
		}
		var totalFileBytes int
		for fp := range fileSet {
			if f, err := store.GetFileByPath(ctx, db, repoName, fp); err == nil {
				totalFileBytes += int(f.SizeBytes)
			}
		}
		responseBytes := len(entries) * 100
		savings := ComputeSavings(totalFileBytes, responseBytes)
		sessionTotal := tracker.Record(savings.Saved)
		timingMs := time.Since(start).Milliseconds()

		result := map[string]any{
			"routes":      entries,
			"route_count": len(entries),
			"matches":     matchEntries,
			"match_count": len(matchEntries),
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
