package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/context-link-mcp/context-link/internal/store"
	"github.com/context-link-mcp/context-link/pkg/models"
)

// RegisterSkeletonTool registers the get_file_skeleton MCP tool with batch support.
// Accepts either a single file path (string) or multiple file paths (array of strings).
func RegisterSkeletonTool(s *server.MCPServer, db *store.DB, repoName, projectRoot string, timeout time.Duration, tracker *SessionTokenTracker) {
	tool := mcp.NewTool("get_file_skeleton",
		mcp.WithDescription(
			"Returns a structural outline of one or more files — symbol signatures only, no code bodies. "+
				"Use this to understand file structure at a fraction of the token cost of reading files. "+
				"\n\n"+
				"**Accepts either a single file path (string) or multiple file paths (array of strings).** "+
				"For batch operations, returns an array of results with per-item error handling. "+
				"\n\n"+
				"Examples:\n"+
				"- Single: file_path='internal/store/symbols.go'\n"+
				"- Batch: file_path=['internal/store/symbols.go', 'internal/store/files.go']",
		),
		// Note: mcp-go doesn't natively support oneOf, but we handle both string and array via parseStringOrArray()
		mcp.WithString("file_path",
			mcp.Required(),
			mcp.Description("Relative file path(s). Accepts either a single string or an array of strings (max 50 files)."),
		),
	)
	s.AddTool(tool, WithTimeout(timeout, fileSkeletonHandlerBatch(db, repoName, projectRoot, tracker)))
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

// fileSkeletonHandlerBatch returns the MCP tool handler for get_file_skeleton with batch support.
// Accepts either a single file path (string) or multiple file paths (array of strings).
func fileSkeletonHandlerBatch(db *store.DB, repoName, projectRoot string, tracker *SessionTokenTracker) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		start := time.Now()

		// Parse polymorphic file_path parameter (string or array).
		args, ok := req.Params.Arguments.(map[string]any)
		if !ok {
			return mcp.NewToolResultError("get_file_skeleton: invalid arguments"), nil
		}
		filePathParam, ok := args["file_path"]
		if !ok {
			return mcp.NewToolResultError("get_file_skeleton: 'file_path' parameter is required"), nil
		}

		filePaths, err := parseStringOrArray(filePathParam)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf(
				"get_file_skeleton: invalid file_path parameter: %v", err,
			)), nil
		}

		// Enforce batch size limit.
		const maxBatchSize = 50
		if len(filePaths) > maxBatchSize {
			return mcp.NewToolResultError(fmt.Sprintf(
				"get_file_skeleton: batch size limit exceeded (max: %d, requested: %d)",
				maxBatchSize, len(filePaths),
			)), nil
		}

		// Process each file path.
		results := make([]batchItemResult, 0, len(filePaths))
		fileSet := map[string]struct{}{}
		totalResponseBytes := 0

		for _, filePath := range filePaths {
			filePath = strings.TrimSpace(filePath)
			if filePath == "" {
				results = append(results, newBatchError(filePath, "file_path is empty"))
				continue
			}

			// Check if file exists on disk.
			if projectRoot != "" {
				absPath := filepath.Join(projectRoot, filePath)
				if _, err := os.Stat(absPath); os.IsNotExist(err) {
					results = append(results, newBatchError(filePath, fmt.Sprintf(
						"file does not exist: %s", filePath,
					)))
					continue
				}
			}

			// Get symbols for this file.
			symbols, err := store.GetSymbolsByFile(ctx, db, repoName, filePath)
			if err != nil {
				results = append(results, newBatchError(filePath, fmt.Sprintf(
					"failed to get symbols: %v", err,
				)))
				continue
			}

			if len(symbols) == 0 {
				results = append(results, newBatchError(filePath, fmt.Sprintf(
					"no symbols found for %q. Check that the file is indexed.", filePath,
				)))
				continue
			}

			// Build skeleton entries.
			entries := make([]skeletonEntry, len(symbols))
			language := ""
			responseBytes := 0
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

			fileSet[filePath] = struct{}{}
			totalResponseBytes += responseBytes

			results = append(results, newBatchSuccess(filePath, map[string]any{
				"file_path":    filePath,
				"language":     language,
				"symbols":      entries,
				"symbol_count": len(entries),
			}))
		}

		// Token savings: skeleton returns signatures only vs. reading entire files.
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
			"total_files":   len(filePaths),
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

// extractSignature returns the first line of a code block (the signature).
func extractSignature(codeBlock string) string {
	if idx := strings.IndexByte(codeBlock, '\n'); idx >= 0 {
		return codeBlock[:idx]
	}
	return codeBlock
}
