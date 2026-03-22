package tools

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/context-link/context-link/internal/store"
	"github.com/context-link/context-link/pkg/models"
)

// RegisterUsagesTool registers the get_symbol_usages MCP tool.
func RegisterUsagesTool(s *server.MCPServer, db *store.DB, repoName string, timeout time.Duration) {
	tool := mcp.NewTool("get_symbol_usages",
		mcp.WithDescription(
			"Reverse dependency lookup — finds all callers of a symbol. "+
				"Returns which functions/methods call or reference the target symbol. "+
				"Example: symbol_name='hashFile' returns all functions that call hashFile.",
		),
		mcp.WithString("symbol_name",
			mcp.Required(),
			mcp.Description("The name or qualified name of the symbol to find usages for (e.g., 'hashFile' or 'Walker.Walk')."),
		),
	)
	s.AddTool(tool, WithTimeout(timeout, symbolUsagesHandler(db, repoName)))
}

// usageEntry is one caller in the usages response.
type usageEntry struct {
	CallerName          string `json:"caller_name"`
	CallerQualifiedName string `json:"caller_qualified_name"`
	CallerKind          string `json:"caller_kind"`
	FilePath            string `json:"file_path"`
	StartLine           int    `json:"start_line"`
	DepKind             string `json:"dep_kind"`
}

// symbolUsagesHandler returns the MCP tool handler for get_symbol_usages.
func symbolUsagesHandler(db *store.DB, repoName string) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		start := time.Now()

		symbolName, err := req.RequireString("symbol_name")
		if err != nil || strings.TrimSpace(symbolName) == "" {
			return mcp.NewToolResultError("get_symbol_usages: 'symbol_name' parameter is required and must not be empty"), nil
		}

		sym, err := store.ResolveSymbol(ctx, db, repoName, symbolName)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf(
				"get_symbol_usages: symbol %q not found in repository %q. Try using semantic_search_symbols first.",
				symbolName, repoName,
			)), nil
		}

		// Get reverse dependencies (who calls this symbol).
		deps, err := store.GetDependenciesByCallee(ctx, db, sym.ID)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("get_symbol_usages: failed to get usages: %v", err)), nil
		}

		// Batch-fetch caller symbols.
		callerIDs := make([]int64, len(deps))
		for i, d := range deps {
			callerIDs[i] = d.CallerID
		}
		callerMap, err := store.GetSymbolsByIDs(ctx, db, repoName, callerIDs)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("get_symbol_usages: failed to fetch callers: %v", err)), nil
		}

		// Build usage list.
		var usages []usageEntry
		for _, d := range deps {
			caller, ok := callerMap[d.CallerID]
			if !ok {
				continue
			}
			usages = append(usages, usageEntry{
				CallerName:          caller.Name,
				CallerQualifiedName: caller.QualifiedName,
				CallerKind:          caller.Kind,
				FilePath:            caller.FilePath,
				StartLine:           caller.StartLine,
				DepKind:             d.Kind,
			})
		}

		result := map[string]any{
			"symbol": map[string]any{
				"name":           sym.Name,
				"qualified_name": sym.QualifiedName,
				"kind":           sym.Kind,
				"file_path":      sym.FilePath,
			},
			"usages":      usages,
			"usage_count": len(usages),
			"metadata": models.ToolMetadata{
				TimingMs: time.Since(start).Milliseconds(),
			},
		}

		return mcp.NewToolResultJSON(result)
	}
}
