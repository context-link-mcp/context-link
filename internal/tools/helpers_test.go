// Package tools — test helpers shared across tool tests.
package tools

import (
	"context"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// callHandler invokes a ToolHandlerFunc with an empty request and returns the result.
func callHandler(t *testing.T, h server.ToolHandlerFunc) (*mcp.CallToolResult, error) {
	t.Helper()
	req := mcp.CallToolRequest{}
	return h(context.Background(), req)
}
