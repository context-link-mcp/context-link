// Package tools implements all MCP tool handlers for context-link.
package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/context-link-mcp/context-link/pkg/models"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// RegisterArchitectureTool registers the read_architecture_rules tool with the
// MCP server. projectRoot is the directory where ARCHITECTURE.md is expected.
// timeout is applied to each tool call.
func RegisterArchitectureTool(s *server.MCPServer, projectRoot string, timeout time.Duration, tracker *SessionTokenTracker) {
	tool := mcp.NewTool("read_architecture_rules",
		mcp.WithDescription(
			"Reads the ARCHITECTURE.md file from the project root and returns its "+
				"sections as structured JSON. Use this at the start of a session to "+
				"understand coding standards, project structure, and design principles.",
		),
	)

	s.AddTool(tool, WithTimeout(timeout, readArchitectureRulesHandler(projectRoot, tracker)))
}

// readArchitectureRulesHandler returns the MCP tool handler function.
func readArchitectureRulesHandler(projectRoot string, tracker *SessionTokenTracker) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		start := time.Now()

		archPath := filepath.Join(projectRoot, "ARCHITECTURE.md")

		data, err := os.ReadFile(archPath)
		if err != nil {
			if os.IsNotExist(err) {
				return mcp.NewToolResultError(fmt.Sprintf(
					"ARCHITECTURE.md not found at %s. "+
						"Create this file to document your project's architectural rules.",
					archPath,
				)), nil
			}
			return mcp.NewToolResultError(fmt.Sprintf(
				"failed to read ARCHITECTURE.md: %v", err,
			)), nil
		}

		sections := parseMarkdownSections(string(data))
		timingMs := time.Since(start).Milliseconds()

		// Token savings: reading ARCHITECTURE.md via this tool returns structured
		// sections instead of the raw file, but the file is fully included so
		// savings come from the structured format being slightly smaller.
		savings := ComputeSavings(len(data), len(data))
		sessionTotal := tracker.Record(savings.Saved)

		result := map[string]any{
			"sections": sections,
			"metadata": models.ToolMetadata{
				TimingMs:           timingMs,
				Source:             archPath,
				TokensSavedEst:     savings.Saved,
				CostAvoidedEst:     FormatCost(savings.Saved),
				SessionTokensSaved: sessionTotal,
				SessionCostAvoided: FormatCost(sessionTotal),
			},
		}

		return mcp.NewToolResultJSON(result)
	}
}

// parseMarkdownSections splits a Markdown document into sections by ## headings.
// The content before the first ## heading is returned as a section with an
// empty title (preamble).
func parseMarkdownSections(content string) []models.Section {
	if strings.TrimSpace(content) == "" {
		return nil
	}

	var sections []models.Section
	var currentTitle string
	var currentLines []string

	for _, line := range strings.Split(content, "\n") {
		if strings.HasPrefix(line, "## ") {
			// Flush previous section.
			if currentTitle != "" || len(currentLines) > 0 {
				sections = append(sections, models.Section{
					Title:   currentTitle,
					Content: strings.TrimSpace(strings.Join(currentLines, "\n")),
				})
			}
			currentTitle = strings.TrimPrefix(line, "## ")
			currentLines = nil
		} else {
			currentLines = append(currentLines, line)
		}
	}

	// Flush final section.
	if currentTitle != "" || len(currentLines) > 0 {
		sections = append(sections, models.Section{
			Title:   currentTitle,
			Content: strings.TrimSpace(strings.Join(currentLines, "\n")),
		})
	}

	return sections
}
