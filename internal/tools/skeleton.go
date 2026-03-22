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

// RegisterSkeletonTool registers the get_file_skeleton MCP tool.
func RegisterSkeletonTool(s *server.MCPServer, db *store.DB, repoName string, timeout time.Duration, tracker *SessionTokenTracker) {
	tool := mcp.NewTool("get_file_skeleton",
		mcp.WithDescription(
			"Returns a structural outline of a file — symbol signatures only, no code bodies. "+
				"Use this to understand file structure at a fraction of the token cost of reading the file. "+
				"Example: file_path='internal/store/symbols.go' returns all function/type signatures.",
		),
		mcp.WithString("file_path",
			mcp.Required(),
			mcp.Description("Relative file path (e.g., 'internal/store/symbols.go')."),
		),
	)
	s.AddTool(tool, WithTimeout(timeout, fileSkeletonHandler(db, repoName, tracker)))
}

// skeletonEntry is one symbol in the skeleton response.
type skeletonEntry struct {
	Name          string `json:"name"`
	QualifiedName string `json:"qualified_name"`
	Kind          string `json:"kind"`
	Signature     string `json:"signature"`
	StartLine     int    `json:"start_line"`
	EndLine       int    `json:"end_line"`
}

// fileSkeletonHandler returns the MCP tool handler for get_file_skeleton.
func fileSkeletonHandler(db *store.DB, repoName string, tracker *SessionTokenTracker) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		start := time.Now()

		filePath, err := req.RequireString("file_path")
		if err != nil || strings.TrimSpace(filePath) == "" {
			return mcp.NewToolResultError("get_file_skeleton: 'file_path' parameter is required and must not be empty"), nil
		}

		symbols, err := store.GetSymbolsByFile(ctx, db, repoName, filePath)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("get_file_skeleton: failed to get symbols for %q: %v", filePath, err)), nil
		}

		if len(symbols) == 0 {
			return mcp.NewToolResultError(fmt.Sprintf(
				"get_file_skeleton: no symbols found for %q in repository %q. Check that the file path is relative to the project root and has been indexed.",
				filePath, repoName,
			)), nil
		}

		entries := make([]skeletonEntry, len(symbols))
		language := ""
		var responseBytes int
		for i, s := range symbols {
			sig := extractSignature(s.CodeBlock)
			entries[i] = skeletonEntry{
				Name:          s.Name,
				QualifiedName: s.QualifiedName,
				Kind:          s.Kind,
				Signature:     sig,
				StartLine:     s.StartLine,
				EndLine:       s.EndLine,
			}
			responseBytes += len(sig)
			if language == "" {
				language = s.Language
			}
		}

		// Token savings: skeleton returns signatures only vs. reading the entire file.
		var fileBytes int
		if f, err := store.GetFileByPath(ctx, db, repoName, filePath); err == nil {
			fileBytes = int(f.SizeBytes)
		}
		savings := ComputeSavings(fileBytes, responseBytes)
		sessionTotal := tracker.Record(savings.Saved)
		timingMs := time.Since(start).Milliseconds()

		result := map[string]any{
			"file_path":    filePath,
			"language":     language,
			"symbols":      entries,
			"symbol_count": len(entries),
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

// extractSignature returns the first line of a code block (the signature).
func extractSignature(codeBlock string) string {
	if idx := strings.IndexByte(codeBlock, '\n'); idx >= 0 {
		return codeBlock[:idx]
	}
	return codeBlock
}
