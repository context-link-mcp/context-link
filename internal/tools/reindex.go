// Package tools implements all MCP tool handlers for context-link.
package tools

import (
	"context"
	"time"

	"github.com/context-link-mcp/context-link/internal/indexer"
	"github.com/context-link-mcp/context-link/pkg/models"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// ReindexTimeout is the timeout for reindex operations (5 minutes).
// Reindexing can involve disk I/O and embedding generation, so it needs
// more time than standard tools (30s default).
const ReindexTimeout = 5 * time.Minute

// RegisterReindexTool registers the reindex_project tool with the MCP server.
// This tool triggers an incremental re-index of the project to keep the symbol graph current.
func RegisterReindexTool(
	s *server.MCPServer,
	idx *indexer.Indexer,
	repoName, repoRoot string,
	tracker *SessionTokenTracker,
) {
	tool := mcp.NewTool("reindex_project",
		mcp.WithDescription(
			"Triggers an incremental re-index of the project. Only re-parses files "+
				"that changed since the last index. Call this after modifying files to ensure the "+
				"symbol graph, dependencies, and search index are up to date. "+
				"Returns counts of files and symbols updated. "+
				"This operation is safe to call repeatedly — if no files changed, it returns "+
				"files_changed: 0 in ~10ms.",
		),
	)

	s.AddTool(tool, WithTimeout(ReindexTimeout, reindexHandler(idx, repoName, repoRoot, tracker)))
}

// reindexHandler returns the MCP tool handler for the reindex_project tool.
func reindexHandler(
	idx *indexer.Indexer,
	repoName, repoRoot string,
	tracker *SessionTokenTracker,
) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		start := time.Now()

		// Run incremental index (never force).
		stats, err := idx.IndexRepo(ctx, repoName, repoRoot)
		if err != nil {
			return mcp.NewToolResultError("reindex failed: " + err.Error()), nil
		}

		// Build response with detailed statistics.
		result := map[string]any{
			"files_scanned":        stats.FilesDiscovered,
			"files_changed":        stats.FilesIndexed,
			"files_deleted":        stats.FilesDeleted,
			"files_unchanged":      stats.FilesUnchanged,
			"symbols_added":        stats.SymbolsExtracted,
			"symbols_removed":      0, // Symbols are deleted as part of file deletion
			"symbols_updated":      stats.SymbolsExtracted, // Incremental re-parse = symbols updated
			"dependencies_updated": stats.DepsExtracted,
			"fts_updated":          stats.FilesIndexed > 0, // FTS updated if files changed
			"embeddings_updated":   stats.EmbeddingsGenerated > 0,
			"duration_ms":          stats.Duration.Milliseconds(),
			"metadata": models.ToolMetadata{
				TimingMs: time.Since(start).Milliseconds(),
			},
		}

		return mcp.NewToolResultJSON(result)
	}
}
