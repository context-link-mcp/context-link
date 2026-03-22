package tools

import (
	"context"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/context-link-mcp/context-link/internal/store"
	"github.com/context-link-mcp/context-link/pkg/models"
)

// RegisterDeadCodeTool registers the find_dead_code MCP tool.
func RegisterDeadCodeTool(s *server.MCPServer, db *store.DB, repoName string, timeout time.Duration, tracker *SessionTokenTracker) {
	tool := mcp.NewTool("find_dead_code",
		mcp.WithDescription(
			"Finds symbols with zero inbound dependency edges (no callers). "+
				"These are potential dead code candidates. Entry points (main, init) are excluded. "+
				"Example: find_dead_code(exclude_exported=true) returns unexported functions nobody calls.",
		),
		mcp.WithString("file_path",
			mcp.Description("Optional: limit search to a specific file path."),
		),
		mcp.WithString("kind",
			mcp.Description("Optional: filter by symbol kind (e.g., 'function', 'class', 'method')."),
		),
		mcp.WithBoolean("exclude_exported",
			mcp.Description("If true, skip symbols starting with uppercase (Go exported symbols). Default: true."),
		),
		mcp.WithNumber("limit",
			mcp.Description("Maximum number of results (default: 50, max: 200)."),
		),
	)
	s.AddTool(tool, WithTimeout(timeout, deadCodeHandler(db, repoName, tracker)))
}

// deadCodeEntry is one symbol in the dead code response.
type deadCodeEntry struct {
	Name          string `json:"name"`
	QualifiedName string `json:"qualified_name"`
	Kind          string `json:"kind"`
	FilePath      string `json:"file_path"`
	StartLine     int    `json:"start_line"`
	Language      string `json:"language"`
}

// deadCodeHandler returns the MCP tool handler for find_dead_code.
func deadCodeHandler(db *store.DB, repoName string, tracker *SessionTokenTracker) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		start := time.Now()

		opts := store.DeadCodeOptions{
			FilePath:        req.GetString("file_path", ""),
			Kind:            req.GetString("kind", ""),
			ExcludeExported: req.GetBool("exclude_exported", true),
			Limit:           req.GetInt("limit", 50),
		}

		symbols, err := store.FindDeadSymbols(ctx, db, repoName, opts)
		if err != nil {
			return mcp.NewToolResultError("find_dead_code: " + err.Error()), nil
		}

		entries := make([]deadCodeEntry, len(symbols))
		fileSet := map[string]struct{}{}
		for i, s := range symbols {
			entries[i] = deadCodeEntry{
				Name:          s.Name,
				QualifiedName: s.QualifiedName,
				Kind:          s.Kind,
				FilePath:      s.FilePath,
				StartLine:     s.StartLine,
				Language:      s.Language,
			}
			fileSet[s.FilePath] = struct{}{}
		}

		// Token savings: agent would grep/read files to find unused code manually.
		var totalFileBytes int
		for fp := range fileSet {
			if f, err := store.GetFileByPath(ctx, db, repoName, fp); err == nil {
				totalFileBytes += int(f.SizeBytes)
			}
		}
		responseBytes := len(entries) * 80
		savings := ComputeSavings(totalFileBytes, responseBytes)
		sessionTotal := tracker.Record(savings.Saved)
		timingMs := time.Since(start).Milliseconds()

		result := map[string]any{
			"dead_symbols": entries,
			"count":        len(entries),
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
