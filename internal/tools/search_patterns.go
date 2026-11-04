package tools

import (
	"context"
	"fmt"
	"regexp"
	"regexp/syntax"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

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
				"Results are ordered by file path and start line. 'truncated' is true when more "+
				"symbols match than 'limit' (truncated_reason 'limit') or when the scan stopped after "+
				"50,000 candidate symbols (truncated_reason 'scan_cap'); narrow the pattern or use "+
				"file_path_prefix/kind in that case."+
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
			mcp.Description("Maximum results to return: a positive number (default: 50; values above 200 are capped to 200)."),
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

// Scan bounds for search_code_patterns: candidates are read in pages and
// matched in Go; the scan stops after maxPatternScan candidates so a broad
// pattern over a huge repo stays within the tool timeout.
const (
	patternPageSize = 500
	maxPatternScan  = 50000
)

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

		limit := req.GetInt("limit", 50)
		if limit <= 0 {
			return mcp.NewToolResultError("search_code_patterns: 'limit' must be a positive number (default 50, max 200)"), nil
		}
		if limit > 200 {
			limit = 200
		}

		// The SQL prefilter only uses substrings every match must contain,
		// so it never drops a real match; the regex decides.
		filter := store.CodePatternFilter{
			Literals:       requiredLiterals(pattern),
			FilePathPrefix: cleanPathPrefix(req.GetString("file_path_prefix", "")),
			Kind:           strings.TrimSpace(req.GetString("kind", "")),
		}

		// Page through the candidates in a stable order, applying the regex
		// before the limit.
		results := []patternMatchResult{}
		fileSet := map[string]struct{}{}
		truncatedReason := ""
		scanned := 0
		var cursor *store.SymbolCursor
	scan:
		for {
			if err := ctx.Err(); err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("search_code_patterns: search interrupted: %v", err)), nil
			}
			page, err := store.SearchCodePatterns(ctx, db, repoName, filter, cursor, patternPageSize)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf(
					"search_code_patterns: search failed: %v", err,
				)), nil
			}
			for _, sym := range page {
				scanned++
				matches := regex.FindStringIndex(sym.CodeBlock)
				if matches == nil {
					continue
				}
				if len(results) == limit {
					truncatedReason = "limit"
					break scan
				}
				results = append(results, newPatternMatch(sym, matches))
				fileSet[sym.FilePath] = struct{}{}
			}
			if len(page) < patternPageSize {
				break
			}
			if scanned >= maxPatternScan {
				truncatedReason = "scan_cap"
				break
			}
			cursor = store.CursorAfter(page[len(page)-1])
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
			"results":         results,
			"result_count":    len(results),
			"pattern":         pattern,
			"truncated":       truncatedReason != "",
			"symbols_scanned": scanned,
			"metadata": models.ToolMetadata{
				TimingMs:           timingMs,
				TokensSavedEst:     savings.Saved,
				CostAvoidedEst:     FormatCost(savings.Saved),
				SessionTokensSaved: sessionTotal,
				SessionCostAvoided: FormatCost(sessionTotal),
			},
		}
		if truncatedReason != "" {
			resp["truncated_reason"] = truncatedReason
		}

		return mcp.NewToolResultJSON(resp)
	}
}

// newPatternMatch builds the result entry for a symbol whose code block
// matched at matches[0]:matches[1], with about 25 bytes of context each side.
func newPatternMatch(sym models.Symbol, matches []int) patternMatchResult {
	matchStart, matchEnd := matches[0], matches[1]
	snippetStart := maxInt(0, matchStart-25)
	snippetEnd := minInt(len(sym.CodeBlock), matchEnd+25)
	snippet := sym.CodeBlock[snippetStart:snippetEnd]

	// Truncate snippet if it's still too long for display.
	if len(snippet) > 200 {
		snippet = snippet[:200] + "..."
	}

	return patternMatchResult{
		SymbolName:    sym.Name,
		QualifiedName: sym.QualifiedName,
		Kind:          sym.Kind,
		FilePath:      sym.FilePath,
		StartLine:     sym.StartLine,
		EndLine:       sym.EndLine,
		MatchSnippet:  snippet,
		MatchIndices:  []int{matchStart, matchEnd},
	}
}

// maxPrefilterLiterals caps the LIKE conditions of the SQL prefilter.
const maxPrefilterLiterals = 3

// requiredLiterals returns substrings that every match of pattern must
// contain, longest first (at most maxPrefilterLiterals, each at least 2
// bytes), for the SQL LIKE prefilter of search_code_patterns. It is
// conservative: alternations, optional or repeated-zero parts, character
// classes and anything else it cannot prove required contribute nothing, so
// the prefilter never excludes a symbol the regex would match. SQLite's LIKE
// is ASCII case-insensitive, which only widens the prefilter; case-folded
// literals are kept only when ASCII folding covers every fold (no non-ASCII
// runes, no k or s, whose folds include U+212A and U+017F).
func requiredLiterals(pattern string) []string {
	re, err := syntax.Parse(pattern, syntax.Perl)
	if err != nil {
		return nil
	}
	info := analyzeLiterals(re.Simplify())

	seen := map[string]bool{}
	var lits []string
	for _, l := range info.required {
		if len(l) < 2 || seen[l] {
			continue
		}
		seen[l] = true
		lits = append(lits, l)
	}
	sort.SliceStable(lits, func(i, j int) bool { return len(lits[i]) > len(lits[j]) })
	if len(lits) > maxPrefilterLiterals {
		lits = lits[:maxPrefilterLiterals]
	}
	return lits
}

// literalInfo describes what a regex node guarantees about its matches.
type literalInfo struct {
	exact    string   // the only string the node matches, when isExact
	isExact  bool     // the node matches exactly one string (possibly empty)
	required []string // substrings every match of the node contains
}

// analyzeLiterals computes the literalInfo of a parsed, simplified regex.
func analyzeLiterals(re *syntax.Regexp) literalInfo {
	switch re.Op {
	case syntax.OpLiteral:
		s := string(re.Rune)
		if re.Flags&syntax.FoldCase != 0 {
			if asciiFoldSafe(s) {
				return literalInfo{required: []string{s}}
			}
			return literalInfo{}
		}
		return literalInfo{exact: s, isExact: true, required: []string{s}}
	case syntax.OpEmptyMatch, syntax.OpBeginLine, syntax.OpEndLine,
		syntax.OpBeginText, syntax.OpEndText, syntax.OpWordBoundary, syntax.OpNoWordBoundary:
		// Zero-width: matches only the empty string at this position.
		return literalInfo{isExact: true}
	case syntax.OpCapture:
		return analyzeLiterals(re.Sub[0])
	case syntax.OpPlus:
		return literalInfo{required: analyzeLiterals(re.Sub[0]).required}
	case syntax.OpRepeat:
		if re.Min >= 1 {
			return literalInfo{required: analyzeLiterals(re.Sub[0]).required}
		}
		return literalInfo{}
	case syntax.OpConcat:
		var info literalInfo
		var run, whole strings.Builder
		allExact := true
		flush := func() {
			if run.Len() > 0 {
				info.required = append(info.required, run.String())
				run.Reset()
			}
		}
		for _, sub := range re.Sub {
			si := analyzeLiterals(sub)
			info.required = append(info.required, si.required...)
			if si.isExact {
				run.WriteString(si.exact)
				whole.WriteString(si.exact)
				continue
			}
			allExact = false
			flush()
		}
		flush()
		if allExact {
			info.isExact = true
			info.exact = whole.String()
		}
		return info
	default:
		// Alternation, star, quest, classes, any-char: nothing is required.
		return literalInfo{}
	}
}

// asciiFoldSafe reports whether SQLite's ASCII-only case-insensitive LIKE
// matches every case fold of s: s is ASCII and has no k/K or s/S (which fold
// to the Kelvin sign and long s).
func asciiFoldSafe(s string) bool {
	for _, r := range s {
		if r >= utf8.RuneSelf || unicode.ToLower(r) == 'k' || unicode.ToLower(r) == 's' {
			return false
		}
	}
	return true
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
