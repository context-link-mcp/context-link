package tools

import (
	"context"
	"fmt"
	"time"

	"github.com/context-link/context-link/internal/store"
	"github.com/context-link/context-link/pkg/models"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// RegisterGetCodeTool registers the get_code_by_symbol tool with the MCP server.
// timeout is applied to each tool call.
func RegisterGetCodeTool(s *server.MCPServer, db *store.DB, repoName string, timeout time.Duration) {
	tool := mcp.NewTool("get_code_by_symbol",
		mcp.WithDescription(
			"Extracts the exact source code of a named symbol (function, class, interface, type) "+
				"along with its direct dependencies and import statements. Use this after "+
				"semantic_search_symbols to retrieve the actual code you need without reading entire files.",
		),
		mcp.WithString("symbol_name",
			mcp.Required(),
			mcp.Description("The name or qualified name of the symbol to retrieve (e.g., 'validateToken' or 'UserAuth.validateToken')."),
		),
		mcp.WithNumber("depth",
			mcp.Description("Dependency depth: 0 = symbol only, 1 = include direct dependencies (default), max 3."),
		),
	)

	s.AddTool(tool, WithTimeout(timeout, getCodeBySymbolHandler(db, repoName)))
}

// getCodeBySymbolHandler returns the MCP tool handler for get_code_by_symbol.
func getCodeBySymbolHandler(db *store.DB, repoName string) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		start := time.Now()

		// Validate input parameters.
		symbolName, err := req.RequireString("symbol_name")
		if err != nil {
			return mcp.NewToolResultError("symbol_name is required and must be a non-empty string"), nil
		}

		depth := req.GetInt("depth", 1)
		if depth < 0 {
			depth = 0
		}
		if depth > 3 {
			depth = 3
		}

		// Look up the symbol.
		sym, deps, err := store.GetSymbolWithDependencies(ctx, db, repoName, symbolName, depth)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf(
				"symbol %q not found in repository %q. Try using semantic_search_symbols first to discover available symbols.",
				symbolName, repoName,
			)), nil
		}

		// Build dependency list.
		var depResults []map[string]any
		for _, d := range deps {
			depResults = append(depResults, map[string]any{
				"name":           d.Name,
				"qualified_name": d.QualifiedName,
				"kind":           d.Kind,
				"file_path":      d.FilePath,
				"code_block":     d.CodeBlock,
				"start_line":     d.StartLine,
				"end_line":       d.EndLine,
			})
		}

		// Fetch associated memories (non-fatal if unavailable).
		memories, _ := store.GetMemoriesBySymbolID(ctx, db, sym.ID)
		type memResult struct {
			ID          int64  `json:"id"`
			Note        string `json:"note"`
			Author      string `json:"author"`
			IsStale     bool   `json:"is_stale"`
			StaleReason string `json:"stale_reason,omitempty"`
			CreatedAt   string `json:"created_at"`
		}
		memResults := make([]memResult, len(memories))
		for i, m := range memories {
			memResults[i] = memResult{
				ID:          m.ID,
				Note:        m.Note,
				Author:      m.Author,
				IsStale:     m.IsStale,
				StaleReason: m.StaleReason,
				CreatedAt:   m.CreatedAt.Format("2006-01-02T15:04:05Z"),
			}
		}

		timingMs := time.Since(start).Milliseconds()

		result := map[string]any{
			"symbol": map[string]any{
				"name":           sym.Name,
				"qualified_name": sym.QualifiedName,
				"kind":           sym.Kind,
				"file_path":      sym.FilePath,
				"code_block":     sym.CodeBlock,
				"start_line":     sym.StartLine,
				"end_line":       sym.EndLine,
				"language":       sym.Language,
				"content_hash":   sym.ContentHash,
			},
			"dependencies":     depResults,
			"dependency_count": len(depResults),
			"memories":         memResults,
			"memory_count":     len(memResults),
			"metadata": models.ToolMetadata{
				TimingMs: timingMs,
			},
		}

		return mcp.NewToolResultJSON(result)
	}
}
