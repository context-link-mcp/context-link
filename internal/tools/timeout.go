package tools

import (
	"context"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// DefaultToolTimeout is the maximum duration any tool call is allowed to run.
const DefaultToolTimeout = 30 * time.Second

// WithTimeout wraps a ToolHandlerFunc so that it runs within a deadline.
// If the handler exceeds the deadline the context is cancelled and an error
// result is returned to the caller.
func WithTimeout(d time.Duration, h server.ToolHandlerFunc) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		ctx, cancel := context.WithTimeout(ctx, d)
		defer cancel()
		return h(ctx, req)
	}
}
