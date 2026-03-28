package tools

import (
	"context"
	"fmt"
	"time"

	"github.com/context-link-mcp/context-link/internal/store"
	"github.com/context-link-mcp/context-link/pkg/models"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// RegisterGetCodeTool registers the get_code_by_symbol MCP tool with batch support.
// Accepts either a single symbol name (string) or multiple symbol names (array of strings).
func RegisterGetCodeTool(s *server.MCPServer, db *store.DB, repoName string, timeout time.Duration, tracker *SessionTokenTracker) {
	tool := mcp.NewTool("get_code_by_symbol",
		mcp.WithDescription(
			"Extracts the exact source code of one or more named symbols (functions, classes, interfaces, types) "+
				"along with their direct dependencies and import statements. Use this after "+
				"semantic_search_symbols to retrieve the actual code you need without reading entire files."+
				"\n\n"+
				"**Accepts either a single symbol name (string) or multiple symbol names (array of strings).** "+
				"For batch operations, returns an array of results with per-item error handling. "+
				"\n\n"+
				"Examples:\n"+
				"- Single: symbol_name='validateToken'\n"+
				"- Batch: symbol_name=['validateToken', 'UserAuth.login', 'formatError']",
		),
		// Note: mcp-go doesn't natively support oneOf, but we handle both string and array via parseStringOrArray()
		mcp.WithString("symbol_name",
			mcp.Required(),
			mcp.Description("Symbol name(s). Accepts either a single string or an array of strings (max 50 symbols)."),
		),
		mcp.WithNumber("depth",
			mcp.Description("Dependency depth: 0 = symbol only, 1 = include direct dependencies (default), max 3. Applies to all symbols in batch."),
		),
	)

	s.AddTool(tool, WithTimeout(timeout, getCodeBySymbolHandlerBatch(db, repoName, tracker)))
}

// getCodeBySymbolHandlerBatch returns the MCP tool handler for get_code_by_symbol with batch support.
// Accepts either a single symbol name (string) or multiple symbol names (array of strings).
func getCodeBySymbolHandlerBatch(db *store.DB, repoName string, tracker *SessionTokenTracker) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		start := time.Now()

		// Parse polymorphic symbol_name parameter (string or array).
		args, ok := req.Params.Arguments.(map[string]any)
		if !ok {
			return mcp.NewToolResultError("get_code_by_symbol: invalid arguments"), nil
		}
		symbolNameParam, ok := args["symbol_name"]
		if !ok {
			return mcp.NewToolResultError("get_code_by_symbol: 'symbol_name' parameter is required"), nil
		}

		symbolNames, err := parseStringOrArray(symbolNameParam)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf(
				"get_code_by_symbol: invalid symbol_name parameter: %v", err,
			)), nil
		}

		// Enforce batch size limit.
		const maxBatchSize = 50
		if len(symbolNames) > maxBatchSize {
			return mcp.NewToolResultError(fmt.Sprintf(
				"get_code_by_symbol: batch size limit exceeded (max: %d, requested: %d)",
				maxBatchSize, len(symbolNames),
			)), nil
		}

		// Depth parameter applies to all symbols in batch.
		depth := req.GetInt("depth", 1)
		if depth < 0 {
			depth = 0
		}
		if depth > 3 {
			depth = 3
		}

		// Process each symbol name.
		results := make([]batchItemResult, 0, len(symbolNames))
		fileSet := map[string]struct{}{}
		totalResponseBytes := 0

		for _, symbolName := range symbolNames {
			// Look up the symbol.
			sym, deps, err := store.GetSymbolWithDependencies(ctx, db, repoName, symbolName, depth)
			if err != nil {
				results = append(results, newBatchError(symbolName, fmt.Sprintf(
					"symbol %q not found in repository %q", symbolName, repoName,
				)))
				continue
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

			// Track file set for token savings.
			fileSet[sym.FilePath] = struct{}{}
			for _, d := range deps {
				fileSet[d.FilePath] = struct{}{}
			}

			// Calculate response size.
			responseBytes := len(sym.CodeBlock)
			for _, d := range deps {
				responseBytes += len(d.CodeBlock)
			}
			totalResponseBytes += responseBytes

			results = append(results, newBatchSuccess(symbolName, map[string]any{
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
			}))
		}

		// Token savings: sum file sizes the agent would have read vs. code blocks returned.
		var totalFileBytes int
		for fp := range fileSet {
			if f, err := store.GetFileByPath(ctx, db, repoName, fp); err == nil {
				totalFileBytes += int(f.SizeBytes)
			}
		}
		savings := ComputeSavings(totalFileBytes, totalResponseBytes)
		sessionTotal := tracker.Record(savings.Saved)
		timingMs := time.Since(start).Milliseconds()

		response := map[string]any{
			"results":       results,
			"total_symbols": len(symbolNames),
			"success_count": countSuccesses(results),
			"error_count":   countErrors(results),
			"metadata": models.ToolMetadata{
				TimingMs:           timingMs,
				TokensSavedEst:     savings.Saved,
				CostAvoidedEst:     FormatCost(savings.Saved),
				SessionTokensSaved: sessionTotal,
				SessionCostAvoided: FormatCost(sessionTotal),
			},
		}

		return mcp.NewToolResultJSON(response)
	}
}
