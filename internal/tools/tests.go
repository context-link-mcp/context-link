// Package tools implements all MCP tool handlers for context-link.
package tools

import (
	"context"
	"time"

	"github.com/context-link-mcp/context-link/internal/store"
	"github.com/context-link-mcp/context-link/pkg/models"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// RegisterTestDiscoveryTool registers the get_tests_for_symbol tool with the MCP server.
// This tool finds test functions associated with a given symbol using the dependency graph and naming heuristics.
func RegisterTestDiscoveryTool(
	s *server.MCPServer,
	db *store.DB,
	repoName string,
	timeout time.Duration,
	tracker *SessionTokenTracker,
) {
	tool := mcp.NewTool("get_tests_for_symbol",
		mcp.WithDescription(
			"Finds test functions associated with a given symbol. Uses the "+
				"dependency graph (tests that call the target) and naming conventions "+
				"(test_calculate_tax for calculate_tax) as fallback. Returns test function "+
				"signatures and optionally their full code blocks. Helps agents locate "+
				"tests to update after modifying a function.",
		),
		mcp.WithString("symbol_name",
			mcp.Required(),
			mcp.Description("Name or qualified name of the symbol to find tests for (e.g., 'ProcessOrder' or 'OrderService.ProcessOrder')"),
		),
		mcp.WithBoolean("include_code",
			mcp.Description("Include full test function bodies in the response (default: false, saves tokens)"),
			mcp.DefaultBool(false),
		),
	)

	s.AddTool(tool, WithTimeout(timeout, testDiscoveryHandler(db, repoName, tracker)))
}

// testDiscoveryHandler returns the MCP tool handler for get_tests_for_symbol.
func testDiscoveryHandler(
	db *store.DB,
	repoName string,
	tracker *SessionTokenTracker,
) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		start := time.Now()

		// Parse parameters.
		symbolName, err := req.RequireString("symbol_name")
		if err != nil {
			return mcp.NewToolResultError("symbol_name is required and must be a non-empty string"), nil
		}

		includeCode := req.GetBool("include_code", false)

		// Resolve the target symbol for response metadata.
		targetSymbol, err := store.ResolveSymbol(ctx, db, repoName, symbolName)
		if err != nil {
			return mcp.NewToolResultError("symbol not found: " + symbolName), nil
		}

		// Strategy 1: Dependency-based discovery (high confidence).
		tests, err := store.GetTestsForSymbol(ctx, db, repoName, symbolName)
		if err != nil {
			return mcp.NewToolResultError("failed to get tests for symbol: " + err.Error()), nil
		}

		// Build result set with match_reason = "calls_target".
		testResults := make(map[int64]testResult) // Use map to deduplicate
		for _, test := range tests {
			testResults[test.ID] = testResult{
				Name:          test.Name,
				QualifiedName: test.QualifiedName,
				FilePath:      test.FilePath,
				StartLine:     test.StartLine,
				EndLine:       test.EndLine,
				MatchReason:   "calls_target",
				CodeBlock:     conditionalCodeBlock(test.CodeBlock, includeCode),
			}
		}

		// Strategy 2: Name heuristic fallback (if no dependency-based results).
		if len(testResults) == 0 {
			heuristicTests, err := store.GetTestsByNameHeuristic(ctx, db, repoName, symbolName)
			if err != nil {
				// Non-fatal: proceed with empty results.
				heuristicTests = nil
			}
			for _, test := range heuristicTests {
				if _, exists := testResults[test.ID]; !exists {
					testResults[test.ID] = testResult{
						Name:          test.Name,
						QualifiedName: test.QualifiedName,
						FilePath:      test.FilePath,
						StartLine:     test.StartLine,
						EndLine:       test.EndLine,
						MatchReason:   "name_match",
						CodeBlock:     conditionalCodeBlock(test.CodeBlock, includeCode),
					}
				}
			}
		}

		// Convert map to sorted slice.
		var results []testResult
		for _, tr := range testResults {
			results = append(results, tr)
		}

		response := map[string]any{
			"symbol": map[string]any{
				"name":           targetSymbol.Name,
				"qualified_name": targetSymbol.QualifiedName,
				"kind":           targetSymbol.Kind,
				"file_path":      targetSymbol.FilePath,
			},
			"tests":      results,
			"test_count": len(results),
			"metadata": models.ToolMetadata{
				TimingMs: time.Since(start).Milliseconds(),
			},
		}

		return mcp.NewToolResultJSON(response)
	}
}

// testResult represents a single test function result.
type testResult struct {
	Name          string  `json:"name"`
	QualifiedName string  `json:"qualified_name"`
	FilePath      string  `json:"file_path"`
	StartLine     int     `json:"start_line"`
	EndLine       int     `json:"end_line"`
	MatchReason   string  `json:"match_reason"` // "calls_target" or "name_match"
	CodeBlock     *string `json:"code_block,omitempty"`
}

// conditionalCodeBlock returns the code block if includeCode is true, otherwise nil.
func conditionalCodeBlock(code string, includeCode bool) *string {
	if includeCode {
		return &code
	}
	return nil
}
