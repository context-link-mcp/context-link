// Package tools implements all MCP tool handlers for context-link.
package tools

import (
	"context"
	"runtime"
	"time"

	"github.com/context-link-mcp/context-link/pkg/models"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// RegisterPingTool registers the ping health-check tool with the MCP server.
// It validates connectivity and returns server status information.
func RegisterPingTool(s *server.MCPServer, version string) {
	tool := mcp.NewTool("ping",
		mcp.WithDescription(
			"Health-check tool that validates connectivity to the context-link MCP server. "+
				"Returns server status, version, and uptime information.",
		),
	)

	s.AddTool(tool, pingHandler(version))
}

// pingHandler returns the MCP tool handler for the ping health-check.
func pingHandler(version string) server.ToolHandlerFunc {
	startedAt := time.Now()

	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		start := time.Now()

		result := map[string]any{
			"status":  "ok",
			"version": version,
			"uptime":  time.Since(startedAt).String(),
			"runtime": map[string]any{
				"go_version": runtime.Version(),
				"os":         runtime.GOOS,
				"arch":       runtime.GOARCH,
			},
			"metadata": models.ToolMetadata{
				TimingMs: time.Since(start).Milliseconds(),
			},
		}

		return mcp.NewToolResultJSON(result)
	}
}
