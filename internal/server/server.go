// Package server implements the MCP server for context-link.
// It wraps the mcp-go library and wires up all tool handlers and prompts.
package server

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/context-link/context-link/internal/config"
	"github.com/context-link/context-link/internal/store"
	"github.com/context-link/context-link/internal/tools"
	"github.com/context-link/context-link/internal/vectorstore"
)

const (
	serverName    = "context-link"
	serverVersion = "0.1.0"
)

// Server wraps the mcp-go MCPServer with context-link wiring.
type Server struct {
	mcp      *server.MCPServer
	cfg      *config.Config
	db       *store.DB
	embedder vectorstore.Embedder
}

// New creates and configures a new Server with all registered tools and prompts.
// embedder may be nil — semantic_search_symbols will return a "not available" error.
func New(cfg *config.Config, db *store.DB, embedder vectorstore.Embedder) *Server {
	mcpServer := server.NewMCPServer(
		serverName,
		serverVersion,
		server.WithLogging(),
	)

	s := &Server{
		mcp:      mcpServer,
		cfg:      cfg,
		db:       db,
		embedder: embedder,
	}

	s.registerTools()
	s.registerPrompts()

	return s
}

// registerTools wires up all MCP tool handlers.
func (s *Server) registerTools() {
	tools.RegisterPingTool(s.mcp)
	slog.Debug("registered tool", "name", "ping")

	tools.RegisterArchitectureTool(s.mcp, s.cfg.ProjectRoot)
	slog.Debug("registered tool", "name", "read_architecture_rules")

	// Phase 2: Structural extraction tool.
	repoName := filepath.Base(s.cfg.ProjectRoot)
	tools.RegisterGetCodeTool(s.mcp, s.db, repoName)
	slog.Debug("registered tool", "name", "get_code_by_symbol")

	// Phase 3: Semantic search tool (embedder may be nil if model not configured).
	tools.RegisterSemanticSearchTool(s.mcp, s.db, s.embedder, repoName)
	slog.Debug("registered tool", "name", "semantic_search_symbols")
}

// registerPrompts registers the explore_codebase prompt that instructs the
// agent to use context-link tools before reading files directly.
func (s *Server) registerPrompts() {
	prompt := mcp.NewPrompt("explore_codebase",
		mcp.WithPromptDescription(
			"Context Funnel protocol — instructs the agent to use context-link tools "+
				"before reading any file. This reduces token consumption by >80%.",
		),
	)

	s.mcp.AddPrompt(prompt, func(ctx context.Context, req mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
		return mcp.NewGetPromptResult(
			"Context Funnel: Efficient Codebase Navigation Protocol",
			[]mcp.PromptMessage{
				mcp.NewPromptMessage(
					mcp.RoleUser,
					mcp.NewTextContent(`CONTEXT PROTOCOL: Before reading any file, you MUST:
1. Use semantic_search_symbols to discover relevant symbols by intent.
2. Use get_code_by_symbol to extract only the needed code and dependencies.
3. Check returned memories for prior insights about the code.
4. ONLY use direct file read as a last resort if the above tools fail.
5. After completing a task, use save_symbol_memory to record findings.

This protocol reduces token consumption by >80% compared to reading files directly.`),
				),
			},
		), nil
	})

	slog.Debug("registered prompt", "name", "explore_codebase")
}

// Run starts the MCP server, reading from stdin and writing to stdout.
// It blocks until ctx is cancelled or an error occurs.
func (s *Server) Run(ctx context.Context) error {
	slog.Info("starting context-link MCP server",
		"version", serverVersion,
		"project_root", s.cfg.ProjectRoot,
		"db_path", s.cfg.DBPath,
	)

	stdioServer := server.NewStdioServer(s.mcp)

	if err := stdioServer.Listen(ctx, os.Stdin, os.Stdout); err != nil {
		return fmt.Errorf("server: stdio transport error: %w", err)
	}

	return nil
}
