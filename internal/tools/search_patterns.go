package tools

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/context-link-mcp/context-link/internal/store"
	"github.com/context-link-mcp/context-link/pkg/models"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// RegisterSearchCodePatternsTool registers the search_code_patterns MCP tool.
func RegisterSearchCodePatternsTool(
	s *server.MCPServer,
	db *store.DB,
	repoName string,
	timeout time.Duration,
	tracker *SessionTokenTracker,
) {
	tool := mcp.NewTool("search_code_patterns",
		mcp.WithDescription(
			"Database-driven search for code patterns across indexed symbols using regex. "+
				"Searches the code_block text of all symbols and returns matching symbols with "+
				"line ranges and code snippets. Useful for finding error sentinels, retry logic, "+
				"specific function calls, or any code pattern matching a regex. "+
				"\n\n"+
				"**IMPORTANT LIMITATION:** This tool searches indexed symbol code blocks only "+
				"(functions, classes, methods, types, variables). It does NOT search file-level code "+
				"outside symbols such as: decorators (e.g., @app.route in Flask), top-level statements, "+
				"module docstrings, configuration dictionaries, or import statements. For file-level "+
				"patterns, read the file directly using the Read tool."+
				"\n\n"+
				"Example: pattern='errors\\\\.New\\\\(\".*\"\\\\)' finds error sentinel definitions.",
		),
		mcp.WithString("pattern",
			mcp.Required(),
			mcp.Description("Regex pattern to search for in code blocks. Must be valid Go regex syntax (RE2)."),
		),
		mcp.WithString("file_path_prefix",
			mcp.Description("Optional: filter to symbols in files starting with this prefix (e.g., 'internal/store/')."),
		),
		mcp.WithString("kind",
			mcp.Description("Optional: filter by symbol kind ('function', 'class', 'interface', 'type', 'variable', 'method')."),
		),
		mcp.WithNumber("limit",
			mcp.Description("Maximum results to return (default: 50, max: 200)."),
		),
	)
	s.AddTool(tool, WithTimeout(timeout, searchCodePatternsHandler(db, repoName, tracker)))
}

// patternMatchResult represents one matching symbol in the response.
type patternMatchResult struct {
	SymbolName    string `json:"symbol_name"`
	QualifiedName string `json:"qualified_name"`
	Kind          string `json:"kind"`
	FilePath      string `json:"file_path"`
	StartLine     int    `json:"start_line"`
	EndLine       int    `json:"end_line"`
	MatchSnippet  string `json:"match_snippet"`
	MatchIndices  []int  `json:"match_indices"` // [start, end] positions in code_block
}

// searchCodePatternsHandler returns the MCP tool handler for search_code_patterns.
func searchCodePatternsHandler(
	db *store.DB,
	repoName string,
	tracker *SessionTokenTracker,
) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		start := time.Now()

		// Validate input parameters.
		pattern, err := req.RequireString("pattern")
		if err != nil || strings.TrimSpace(pattern) == "" {
			return mcp.NewToolResultError("search_code_patterns: 'pattern' parameter is required and must not be empty"), nil
		}

		// Validate regex pattern before executing.
		regex, err := regexp.Compile(pattern)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf(
				"search_code_patterns: invalid regex pattern: %v", err,
			)), nil
		}

		filePathPrefix := strings.TrimSpace(req.GetString("file_path_prefix", ""))
		kindFilter := strings.TrimSpace(req.GetString("kind", ""))
		limit := req.GetInt("limit", 50)

		// For LIKE prefiltering, extract a literal substring from the pattern if possible.
		// Simple heuristic: if pattern contains no regex metacharacters, use it directly.
		likePattern := extractLiteralSubstring(pattern)

		// Query database for candidate symbols.
		// Fetch 2x limit to account for regex filtering false positives from LIKE.
		symbols, err := store.SearchCodePatterns(ctx, db, repoName, likePattern, filePathPrefix, kindFilter, limit*2)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf(
				"search_code_patterns: search failed: %v", err,
			)), nil
		}

		// Apply regex filtering on Go side.
		var results []patternMatchResult
		fileSet := map[string]struct{}{}
		for _, sym := range symbols {
			if len(results) >= limit {
				break
			}

			matches := regex.FindStringIndex(sym.CodeBlock)
			if matches == nil {
				continue
			}

			// Extract snippet (50 chars context around match).
			matchStart := matches[0]
			matchEnd := matches[1]
			snippetStart := maxInt(0, matchStart-25)
			snippetEnd := minInt(len(sym.CodeBlock), matchEnd+25)
			snippet := sym.CodeBlock[snippetStart:snippetEnd]

			// Truncate snippet if it's still too long for display.
			if len(snippet) > 200 {
				snippet = snippet[:200] + "..."
			}

			results = append(results, patternMatchResult{
				SymbolName:    sym.Name,
				QualifiedName: sym.QualifiedName,
				Kind:          sym.Kind,
				FilePath:      sym.FilePath,
				StartLine:     sym.StartLine,
				EndLine:       sym.EndLine,
				MatchSnippet:  snippet,
				MatchIndices:  []int{matchStart, matchEnd},
			})
			fileSet[sym.FilePath] = struct{}{}
		}

		// Token savings: agent would read all matched files; we return snippets only.
		var totalFileBytes int
		for fp := range fileSet {
			if f, err := store.GetFileByPath(ctx, db, repoName, fp); err == nil {
				totalFileBytes += int(f.SizeBytes)
			}
		}
		// Approximate response size: 150 bytes per result (JSON overhead + snippet).
		responseBytes := len(results) * 150
		savings := ComputeSavings(totalFileBytes, responseBytes)
		sessionTotal := tracker.Record(savings.Saved)
		timingMs := time.Since(start).Milliseconds()

		resp := map[string]any{
			"results":      results,
			"result_count": len(results),
			"pattern":      pattern,
			"metadata": models.ToolMetadata{
				TimingMs:           timingMs,
				TokensSavedEst:     savings.Saved,
				CostAvoidedEst:     FormatCost(savings.Saved),
				SessionTokensSaved: sessionTotal,
				SessionCostAvoided: FormatCost(sessionTotal),
			},
		}

		return mcp.NewToolResultJSON(resp)
	}
}

// extractLiteralSubstring extracts a literal substring from a regex pattern
// for SQL LIKE prefiltering. Returns the pattern stripped of common regex anchors.
// This is a simple heuristic to improve query efficiency.
func extractLiteralSubstring(pattern string) string {
	// Simple heuristic: remove ^ and $ anchors, backslash escapes.
	s := strings.TrimPrefix(pattern, "^")
	s = strings.TrimSuffix(s, "$")

	// If pattern has no regex metacharacters, use it directly.
	if !strings.ContainsAny(s, ".*+?[]{}()|\\") {
		return s
	}

	// Otherwise, extract first contiguous alphanumeric sequence.
	// Walk through the string and find the first run of word characters.
	for i := 0; i < len(s); i++ {
		c := s[i]
		// Skip backslash escape sequences (e.g., \b, \., \\)
		if c == '\\' && i+1 < len(s) {
			i++ // Skip the escaped character
			continue
		}
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' {
			start := i
			// Find the end of this word sequence.
			for i < len(s) {
				c := s[i]
				if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_') {
					break
				}
				i++
			}
			// Return the first word sequence that's at least 3 characters long.
			word := s[start:i]
			if len(word) >= 3 {
				return word
			}
		}
	}

	// Fallback: no suitable literal substring found, return empty.
	// The SQL query will still work but won't benefit from LIKE prefiltering.
	return ""
}

// minInt returns the smaller of two integers.
func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// maxInt returns the larger of two integers.
func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
