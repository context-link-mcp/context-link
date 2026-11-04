package tools

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/context-link-mcp/context-link/internal/store"
	"github.com/context-link-mcp/context-link/pkg/models"
)

// RegisterMemoryTools registers save_symbol_memory, get_symbol_memories, and
// purge_stale_memories with the MCP server. To expose only some of them, use
// the per-tool Register functions.
func RegisterMemoryTools(s *server.MCPServer, db *store.DB, repoName string, timeout time.Duration) {
	RegisterSaveMemoryTool(s, db, repoName, timeout)
	RegisterGetMemoriesTool(s, db, repoName, timeout)
	RegisterPurgeMemoriesTool(s, db, repoName, timeout)
}

// RegisterSaveMemoryTool registers the save_symbol_memory tool.
func RegisterSaveMemoryTool(s *server.MCPServer, db *store.DB, repoName string, timeout time.Duration) {
	tool := mcp.NewTool("save_symbol_memory",
		mcp.WithDescription(
			"Attaches a persistent note to a named code symbol. The note survives "+
				"re-indexing and is flagged stale automatically when the symbol changes. "+
				"symbol_name must match a symbol's name or qualified name exactly; a partial name "+
				"is rejected with suggestions, never attached to a guessed symbol. "+
				"Use this to record findings, gotchas, or architectural decisions about specific functions or classes.",
		),
		mcp.WithString("symbol_name",
			mcp.Required(),
			mcp.Description("Name or qualified name of the symbol to annotate (e.g., 'validateToken' or 'UserAuth.validateToken')."),
		),
		mcp.WithString("note",
			mcp.Required(),
			mcp.Description("The note to attach. Max 2000 characters."),
		),
		mcp.WithString("author",
			mcp.Description("Who is saving this note: 'agent' (default) or 'developer'."),
		),
	)
	s.AddTool(tool, WithTimeout(timeout, saveMemoryHandler(db, repoName)))
}

// RegisterGetMemoriesTool registers the get_symbol_memories tool.
func RegisterGetMemoriesTool(s *server.MCPServer, db *store.DB, repoName string, timeout time.Duration) {
	tool := mcp.NewTool("get_symbol_memories",
		mcp.WithDescription(
			"Retrieves persistent notes attached to a symbol or all symbols in a file. "+
				"Returns both fresh and stale memories; stale entries include a stale_reason field. "+
				"Results are sorted newest-first and support pagination.",
		),
		mcp.WithString("symbol_name",
			mcp.Description("Exact symbol name or qualified name to look up memories for (no partial matching). Provide either this or file_path."),
		),
		mcp.WithString("file_path",
			mcp.Description("File path to retrieve memories for all symbols in the file. Provide either this or symbol_name."),
		),
		mcp.WithNumber("offset",
			mcp.Description("Pagination offset (default 0)."),
		),
		mcp.WithNumber("limit",
			mcp.Description("Maximum results to return (default 20, values above 100 are capped to 100)."),
		),
	)
	s.AddTool(tool, WithTimeout(timeout, getMemoriesHandler(db, repoName)))
}

// RegisterPurgeMemoriesTool registers the destructive purge_stale_memories
// tool.
func RegisterPurgeMemoriesTool(s *server.MCPServer, db *store.DB, repoName string, timeout time.Duration) {
	tool := mcp.NewTool("purge_stale_memories",
		mcp.WithDescription(
			"Deletes stale memories for the repository. By default deletes all stale memories. "+
				"Set orphaned_only=true to delete only memories whose symbol has been permanently removed.",
		),
		mcp.WithBoolean("orphaned_only",
			mcp.Description("If true, only delete memories with no linked symbol (symbol deleted). Default false."),
		),
	)
	s.AddTool(tool, WithTimeout(timeout, purgeMemoriesHandler(db, repoName)))
}

// saveMemoryHandler returns the handler for save_symbol_memory.
func saveMemoryHandler(db *store.DB, repoName string) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		start := time.Now()

		symbolName, err := req.RequireString("symbol_name")
		symbolName = strings.TrimSpace(symbolName)
		if err != nil || symbolName == "" {
			return mcp.NewToolResultError("save_symbol_memory: 'symbol_name' parameter is required and must not be empty"), nil
		}
		note, err := req.RequireString("note")
		if err != nil || strings.TrimSpace(note) == "" {
			return mcp.NewToolResultError("save_symbol_memory: 'note' parameter is required and must not be empty"), nil
		}
		author := req.GetString("author", "agent")
		if author != "agent" && author != "developer" {
			author = "agent"
		}

		// Exact name or qualified name only: a note must never be attached to
		// a symbol guessed from a partial name.
		res, lookupErr := store.ResolveSymbol(ctx, db, repoName, symbolName, false)
		if lookupErr != nil {
			return mcp.NewToolResultError(symbolLookupMessage("save_symbol_memory", repoName, symbolName, res, lookupErr)), nil
		}
		sym := res.Symbol

		mem := &models.Memory{
			SymbolID:        &sym.ID,
			RepoName:        repoName,
			Note:            note,
			Author:          author,
			LastKnownSymbol: sym.QualifiedName,
			LastKnownFile:   sym.FilePath,
		}
		memID, saveErr := store.SaveMemory(ctx, db, mem)
		if saveErr != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to save memory: %s", saveErr.Error())), nil
		}

		result := map[string]any{
			"memory_id":   memID,
			"symbol_name": sym.QualifiedName,
			"file_path":   sym.FilePath,
			"author":      author,
			"note_length": len([]rune(note)),
			"metadata": models.ToolMetadata{
				TimingMs: time.Since(start).Milliseconds(),
			},
		}
		return mcp.NewToolResultJSON(result)
	}
}

// getMemoriesHandler returns the handler for get_symbol_memories.
func getMemoriesHandler(db *store.DB, repoName string) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		start := time.Now()

		symbolName := strings.TrimSpace(req.GetString("symbol_name", ""))
		filePath := strings.TrimSpace(req.GetString("file_path", ""))

		if symbolName == "" && filePath == "" {
			return mcp.NewToolResultError("get_symbol_memories: provide either symbol_name or file_path"), nil
		}
		if filePath != "" {
			cleaned, err := cleanRepoPath("", filePath)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("get_symbol_memories: invalid file_path %q: %v", filePath, err)), nil
			}
			filePath = cleaned
		}

		offset := req.GetInt("offset", 0)
		if offset < 0 {
			offset = 0
		}
		limit := req.GetInt("limit", 20)
		if limit <= 0 {
			limit = 20
		}
		if limit > 100 {
			limit = 100
		}

		var mems []models.Memory
		var queryErr error

		if symbolName != "" {
			mems, queryErr = store.GetMemoriesBySymbolName(ctx, db, repoName, symbolName, offset, limit)
		} else {
			mems, queryErr = store.GetMemoriesByFilePath(ctx, db, repoName, filePath, offset, limit)
		}
		if queryErr != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to retrieve memories: %s", queryErr.Error())), nil
		}

		type memoryResult struct {
			ID              int64  `json:"id"`
			SymbolID        *int64 `json:"symbol_id"`
			Note            string `json:"note"`
			Author          string `json:"author"`
			IsStale         bool   `json:"is_stale"`
			StaleReason     string `json:"stale_reason,omitempty"`
			LastKnownSymbol string `json:"last_known_symbol,omitempty"`
			LastKnownFile   string `json:"last_known_file,omitempty"`
			CreatedAt       string `json:"created_at"`
		}

		results := make([]memoryResult, len(mems))
		for i, m := range mems {
			results[i] = memoryResult{
				ID:              m.ID,
				SymbolID:        m.SymbolID,
				Note:            m.Note,
				Author:          m.Author,
				IsStale:         m.IsStale,
				StaleReason:     m.StaleReason,
				LastKnownSymbol: m.LastKnownSymbol,
				LastKnownFile:   m.LastKnownFile,
				CreatedAt:       m.CreatedAt.Format(time.RFC3339),
			}
		}

		response := map[string]any{
			"memories":       results,
			"total_returned": len(results),
			"offset":         offset,
			"limit":          limit,
			"metadata": models.ToolMetadata{
				TimingMs: time.Since(start).Milliseconds(),
			},
		}
		return mcp.NewToolResultJSON(response)
	}
}

// purgeMemoriesHandler returns the handler for purge_stale_memories.
func purgeMemoriesHandler(db *store.DB, repoName string) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		start := time.Now()

		orphanedOnly := req.GetBool("orphaned_only", false)

		n, err := store.PurgeStaleMemories(ctx, db, repoName, orphanedOnly)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to purge memories: %s", err.Error())), nil
		}

		result := map[string]any{
			"purged_count":  n,
			"orphaned_only": orphanedOnly,
			"metadata": models.ToolMetadata{
				TimingMs: time.Since(start).Milliseconds(),
			},
		}
		return mcp.NewToolResultJSON(result)
	}
}
