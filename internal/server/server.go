// Package server implements the MCP server for context-link.
// It wraps the mcp-go library and wires up all tool handlers and prompts.
package server

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/context-link-mcp/context-link/internal/config"
	"github.com/context-link-mcp/context-link/internal/indexer"
	"github.com/context-link-mcp/context-link/internal/store"
	"github.com/context-link-mcp/context-link/internal/tools"
	"github.com/context-link-mcp/context-link/internal/vectorstore"
)

const serverName = "context-link"

// Server wraps the mcp-go MCPServer with context-link wiring.
type Server struct {
	mcp      *server.MCPServer
	cfg      *config.Config
	db       *store.DB
	embedder vectorstore.Embedder
	indexer  *indexer.Indexer
	version  string

	// vecCache holds the repo's embeddings for semantic_search_symbols. It
	// must be dropped after every index run (see NotifyIndexed).
	vecCache *vectorstore.VectorCache
}

// New creates and configures a new Server with all registered tools and prompts.
// embedder may be nil — semantic_search_symbols will return a "not available" error.
// indexer may be nil — reindex_project will not be registered.
func New(cfg *config.Config, db *store.DB, embedder vectorstore.Embedder, idx *indexer.Indexer, version string) *Server {
	mcpServer := server.NewMCPServer(
		serverName,
		version,
		server.WithLogging(),
	)

	s := &Server{
		mcp:      mcpServer,
		cfg:      cfg,
		db:       db,
		embedder: embedder,
		indexer:  idx,
		version:  version,
	}
	s.vecCache = vectorstore.NewVectorCache(s.repoName())

	s.registerTools()
	s.registerPrompts()

	return s
}

// repoName is the namespace every tool queries: cfg.RepoName when set (the
// CLI sets it from --repo-name, as index does), else the root's directory name.
func (s *Server) repoName() string {
	if s.cfg.RepoName != "" {
		return s.cfg.RepoName
	}
	return filepath.Base(filepath.Clean(s.cfg.ProjectRoot))
}

// NotifyIndexed drops the caches derived from the index so the next query
// reloads them. Call it after every index run of the served repo, whether it
// succeeded or not (a failed run may still have committed some files): the
// reindex_project tool calls it itself, and the --watch watcher is wired to
// it. Safe to call concurrently with in-flight searches.
func (s *Server) NotifyIndexed() {
	s.vecCache.Invalidate()
}

// toolRegistration pairs a tool name with its registration function.
type toolRegistration struct {
	name     string
	register func()
}

// registerTools wires up MCP tool handlers.
// If cfg.Tools is non-empty, only the listed tools are registered.
// Every handler is wrapped with a 30-second timeout.
func (s *Server) registerTools() {
	repoName := s.repoName()
	timeout := tools.DefaultToolTimeout
	vecCache := s.vecCache
	tracker := tools.NewSessionTokenTracker()

	registry := []toolRegistration{
		{"ping", func() { tools.RegisterPingTool(s.mcp, s.version) }},
		{"reindex_project", func() {
			if s.indexer != nil {
				tools.RegisterReindexTool(s.mcp, s.indexer, repoName, s.cfg.ProjectRoot, tracker, s.NotifyIndexed)
			}
		}},
		{"get_modified_symbols", func() { tools.RegisterModifiedSymbolsTool(s.mcp, s.db, repoName, s.cfg.ProjectRoot, timeout, tracker) }},
		{"get_tests_for_symbol", func() { tools.RegisterTestDiscoveryTool(s.mcp, s.db, repoName, timeout, tracker) }},
		{"read_architecture_rules", func() { tools.RegisterArchitectureTool(s.mcp, s.cfg.ProjectRoot, timeout, tracker) }},
		{"get_code_by_symbol", func() { tools.RegisterGetCodeTool(s.mcp, s.db, repoName, timeout, tracker) }},
		{"semantic_search_symbols", func() {
			tools.RegisterSemanticSearchTool(s.mcp, s.db, s.embedder, repoName, timeout, tracker, vecCache)
		}},
		{"get_file_skeleton", func() { tools.RegisterSkeletonTool(s.mcp, s.db, repoName, s.cfg.ProjectRoot, timeout, tracker) }},
		{"get_symbol_usages", func() { tools.RegisterUsagesTool(s.mcp, s.db, repoName, timeout, tracker) }},
		{"get_call_tree", func() { tools.RegisterCallTreeTool(s.mcp, s.db, repoName, timeout, tracker) }},
		{"find_dead_code", func() { tools.RegisterDeadCodeTool(s.mcp, s.db, repoName, s.cfg.ProjectRoot, timeout, tracker) }},
		{"get_blast_radius", func() { tools.RegisterBlastRadiusTool(s.mcp, s.db, repoName, timeout, tracker) }},
		{"find_http_routes", func() { tools.RegisterRoutesTool(s.mcp, s.db, repoName, s.cfg.ProjectRoot, timeout, tracker) }},
		{"search_code_patterns", func() { tools.RegisterSearchCodePatternsTool(s.mcp, s.db, repoName, timeout, tracker) }},
		{"save_symbol_memory", func() { tools.RegisterSaveMemoryTool(s.mcp, s.db, repoName, timeout) }},
		{"get_symbol_memories", func() { tools.RegisterGetMemoriesTool(s.mcp, s.db, repoName, timeout) }},
		{"purge_stale_memories", func() { tools.RegisterPurgeMemoriesTool(s.mcp, s.db, repoName, timeout) }},
	}

	enabled := s.cfg.Tools
	for _, reg := range registry {
		if isToolEnabled(reg.name, enabled) {
			reg.register()
			slog.Debug("registered tool", "name", reg.name)
		} else {
			slog.Debug("skipped tool (not in config)", "name", reg.name)
		}
	}
}

// memoryToolGroup is the config name that enables all three memory tools.
const memoryToolGroup = "memory"

// isToolEnabled returns true if the tool should be registered.
// If the enabled list is empty, all tools are enabled. Each tool is enabled
// by its own name; the "memory" group name enables save_symbol_memory,
// get_symbol_memories and purge_stale_memories together. Naming one memory
// tool never enables the others (in particular not the destructive purge).
func isToolEnabled(name string, enabled []string) bool {
	if len(enabled) == 0 {
		return true
	}
	for _, e := range enabled {
		if e == name {
			return true
		}
		if e == memoryToolGroup && isMemoryTool(name) {
			return true
		}
	}
	return false
}

// isMemoryTool reports whether name is one of the memory tools.
func isMemoryTool(name string) bool {
	return name == "save_symbol_memory" || name == "get_symbol_memories" || name == "purge_stale_memories"
}

// registerPrompts registers the explore_codebase prompt that instructs the
// agent to use context-link tools before reading files directly.
func (s *Server) registerPrompts() {
	// Build the protocol steps dynamically based on which tools are enabled.
	steps := []string{
		"Use semantic_search_symbols to discover relevant symbols by intent.",
		"Use get_file_skeleton to understand a file's structure (signatures only, no code bodies).",
		"Use get_code_by_symbol to extract only the needed code and dependencies.",
		"Use get_symbol_usages to find all callers of a symbol.",
		"Use get_call_tree to explore call hierarchies (callees or callers).",
		"Use get_blast_radius to see everything affected by changing a symbol.",
		"Use find_dead_code to discover unused symbols in the codebase.",
		"Check returned memories for prior insights about the code.",
		"ONLY use direct file read as a last resort if the above tools fail.",
		"After completing a task, use save_symbol_memory to record findings.",
	}

	var numbered []string
	for i, step := range steps {
		numbered = append(numbered, fmt.Sprintf("%d. %s", i+1, step))
	}
	protocol := strings.Join(numbered, "\n")

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
					mcp.NewTextContent(fmt.Sprintf("CONTEXT PROTOCOL: Before reading any file, you MUST:\n%s\n\nThis protocol reduces token consumption by >80%% compared to reading files directly.", protocol)),
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
		"version", s.version,
		"project_root", s.cfg.ProjectRoot,
		"db_path", s.cfg.DBPath,
	)

	stdioServer := server.NewStdioServer(s.mcp)

	if err := stdioServer.Listen(ctx, os.Stdin, os.Stdout); err != nil {
		return fmt.Errorf("server: stdio transport error: %w", err)
	}

	return nil
}
