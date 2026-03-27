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

// RegisterModifiedSymbolsTool registers the get_modified_symbols tool with the MCP server.
// This tool maps git diff hunks to AST symbols for git-aware context discovery.
func RegisterModifiedSymbolsTool(
	s *server.MCPServer,
	db *store.DB,
	repoName, projectRoot string,
	timeout time.Duration,
	tracker *SessionTokenTracker,
) {
	tool := mcp.NewTool("get_modified_symbols",
		mcp.WithDescription(
			"Returns symbols (functions, methods, classes) that overlap with "+
				"locally modified lines in the git working tree. Call reindex_project first "+
				"to ensure the index is current. Use this to orient yourself at the start "+
				"of a session — it shows exactly what's being actively worked on. "+
				"Diffs against base_ref (default HEAD for uncommitted changes). "+
				"Use base_ref=main for branch diff, or a commit SHA for specific comparison.",
		),
		mcp.WithString("base_ref",
			mcp.Description("Git ref to diff against (default: HEAD for uncommitted changes)"),
			mcp.DefaultString("HEAD"),
		),
		mcp.WithBoolean("include_staged",
			mcp.Description("Include staged (git add) changes in addition to unstaged (default: true)"),
			mcp.DefaultBool(true),
		),
	)

	s.AddTool(tool, WithTimeout(timeout, modifiedSymbolsHandler(db, repoName, projectRoot, tracker)))
}

// modifiedSymbolsHandler returns the MCP tool handler for get_modified_symbols.
func modifiedSymbolsHandler(
	db *store.DB,
	repoName, projectRoot string,
	tracker *SessionTokenTracker,
) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		start := time.Now()

		// Parse parameters.
		baseRef := req.GetString("base_ref", "HEAD")
		includeStaged := req.GetBool("include_staged", true)

		// Extract git diff hunks.
		gitDiff, err := ExtractGitDiff(ctx, projectRoot, baseRef, includeStaged)
		if err != nil {
			return mcp.NewToolResultError("git diff extraction failed: " + err.Error()), nil
		}

		// Map hunks to symbols.
		var results []modifiedSymbolResult
		for _, fileDiff := range gitDiff.Files {
			if fileDiff.Status == "D" {
				// Deleted files: symbols no longer exist on disk, but may still be in index.
				// We could query for them, but they're not actionable for agents.
				continue
			}

			if len(fileDiff.Hunks) == 0 {
				// No line changes (e.g., mode change only) — skip.
				continue
			}

			// Convert GitHunks to store.GitHunks for the query.
			storeHunks := make([]store.GitHunk, len(fileDiff.Hunks))
			for i, h := range fileDiff.Hunks {
				storeHunks[i] = store.GitHunk{
					StartLine: h.StartLine,
					LineCount: h.LineCount,
				}
			}

			// Query symbols that intersect with these hunks.
			symbols, err := store.GetSymbolsByFileAndLines(ctx, db, repoName, fileDiff.FilePath, storeHunks)
			if err != nil {
				// Non-fatal: file may not be indexed yet (untracked, or reindex needed).
				continue
			}

			// Build result entries with changed_lines per symbol.
			for _, sym := range symbols {
				changedLines := computeChangedLines(sym, fileDiff.Hunks)
				changeType := determineChangeType(sym, fileDiff.Hunks, fileDiff.Status)

				results = append(results, modifiedSymbolResult{
					Name:          sym.Name,
					QualifiedName: sym.QualifiedName,
					Kind:          sym.Kind,
					FilePath:      sym.FilePath,
					StartLine:     sym.StartLine,
					EndLine:       sym.EndLine,
					ChangedLines:  changedLines,
					ChangeType:    changeType,
				})
			}
		}

		response := map[string]any{
			"base_ref":      baseRef,
			"files_changed": gitDiff.FilesChanged,
			"symbols":       results,
			"metadata": models.ToolMetadata{
				TimingMs: time.Since(start).Milliseconds(),
			},
		}

		return mcp.NewToolResultJSON(response)
	}
}

// modifiedSymbolResult represents a single symbol that intersects with git changes.
type modifiedSymbolResult struct {
	Name          string `json:"name"`
	QualifiedName string `json:"qualified_name"`
	Kind          string `json:"kind"`
	FilePath      string `json:"file_path"`
	StartLine     int    `json:"start_line"`
	EndLine       int    `json:"end_line"`
	ChangedLines  []int  `json:"changed_lines"`
	ChangeType    string `json:"change_type"` // "added", "modified", "deleted"
}

// computeChangedLines returns the specific line numbers within the symbol that were changed.
func computeChangedLines(sym models.Symbol, hunks []GitHunk) []int {
	var lines []int
	for _, hunk := range hunks {
		hunkEnd := hunk.StartLine + hunk.LineCount - 1
		// Check if this hunk overlaps with the symbol's line range.
		if hunk.StartLine <= sym.EndLine && hunkEnd >= sym.StartLine {
			// Add all lines in the hunk that fall within the symbol's range.
			start := max(hunk.StartLine, sym.StartLine)
			end := min(hunkEnd, sym.EndLine)
			for line := start; line <= end; line++ {
				lines = append(lines, line)
			}
		}
	}
	return lines
}

// determineChangeType classifies the change as "added", "modified", or "deleted".
func determineChangeType(sym models.Symbol, hunks []GitHunk, fileStatus string) string {
	if fileStatus == "A" {
		return "added"
	}
	if fileStatus == "D" {
		return "deleted"
	}

	// Check if all lines of the symbol were changed (treat as "added" to this location).
	symbolLines := sym.EndLine - sym.StartLine + 1
	changedLineCount := 0
	for _, hunk := range hunks {
		hunkEnd := hunk.StartLine + hunk.LineCount - 1
		if hunk.StartLine <= sym.EndLine && hunkEnd >= sym.StartLine {
			start := max(hunk.StartLine, sym.StartLine)
			end := min(hunkEnd, sym.EndLine)
			changedLineCount += end - start + 1
		}
	}

	if changedLineCount >= symbolLines {
		return "added"
	}
	return "modified"
}

// max returns the larger of two integers.
func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// min returns the smaller of two integers.
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
