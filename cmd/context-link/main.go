// context-link: Local MCP Context Gateway for AI coding agents.
// Serves structural code context (symbols, dependencies, memories) over MCP/stdio.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/context-link-mcp/context-link/internal/config"
	"github.com/context-link-mcp/context-link/internal/indexer"
	"github.com/context-link-mcp/context-link/internal/indexer/adapters"
	"github.com/context-link-mcp/context-link/internal/server"
	"github.com/context-link-mcp/context-link/internal/store"
	"github.com/context-link-mcp/context-link/internal/vectorstore"
	"github.com/context-link-mcp/context-link/internal/watcher"
)

// version is set at build time via -ldflags "-X main.version=vX.Y.Z".
var version = "dev"

// CLI flag values — populated by cobra before command functions run.
var (
	flagDBPath      string
	flagProjectRoot string
	flagLogLevel    string
	flagConfigFile  string

	// Phase 3: semantic search model paths.
	flagModelPath  string
	flagVocabPath  string
	flagORTLibPath string

	// Tool registry: comma-separated list of tools to enable.
	flagTools []string

	// File watcher flag.
	flagWatch bool
)

func main() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

var rootCmd = &cobra.Command{
	Use:   "context-link",
	Short: "Local MCP Context Gateway for AI coding agents",
	Long: `context-link is a local MCP server that serves structured code context
to AI agents, dramatically reducing token consumption compared to reading
entire files. It indexes TypeScript/TSX codebases, builds a symbol graph,
and provides semantic search via local embeddings.`,
	SilenceUsage: true,
}

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start the MCP server (reads from stdin, writes to stdout)",
	RunE:  runServe,
}

var (
	flagRepoName string
	flagWorkers  int
	flagForce    bool
)

var indexCmd = &cobra.Command{
	Use:   "index [path]",
	Short: "Index a project directory into the symbol database",
	Long: `Scans the given directory (or project root) for source files, parses them
using Tree-sitter, and stores extracted symbols and dependencies in the
SQLite database. Supports incremental re-indexing — only changed files
are re-processed.`,
	Args: cobra.MaximumNArgs(1),
	RunE: runIndex,
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the version and exit",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("context-link %s\n", version)
	},
}

func init() {
	// Persistent flags available to all subcommands.
	rootCmd.PersistentFlags().StringVar(&flagDBPath, "db-path", "", "path to SQLite database (default: .context-link.db)")
	rootCmd.PersistentFlags().StringVar(&flagProjectRoot, "project-root", "", "root directory of the project to index (default: current dir)")
	rootCmd.PersistentFlags().StringVar(&flagLogLevel, "log-level", "info", "log level: debug, info, warn, error")
	rootCmd.PersistentFlags().StringVar(&flagConfigFile, "config", "", "config file (default: .context-link.yaml in current dir)")

	// Index-specific flags.
	indexCmd.Flags().StringVar(&flagRepoName, "repo-name", "", "repository name for multi-repo namespacing (default: directory name)")
	indexCmd.Flags().IntVar(&flagWorkers, "workers", 4, "number of parallel worker goroutines for indexing")
	indexCmd.Flags().BoolVar(&flagForce, "force", false, "force full re-index, bypassing incremental hash check")

	// Phase 3: semantic search flags (shared by serve and index).
	for _, cmd := range []*cobra.Command{serveCmd, indexCmd} {
		cmd.Flags().StringVar(&flagModelPath, "model-path", "", "path to all-MiniLM-L6-v2.onnx (enables semantic search)")
		cmd.Flags().StringVar(&flagVocabPath, "vocab-path", "", "path to vocab.txt for the ONNX model tokenizer")
		cmd.Flags().StringVar(&flagORTLibPath, "ort-lib-path", "", "path to OnnxRuntime shared library (default: system search path)")
	}

	// Tool registry flag (serve only).
	serveCmd.Flags().StringSliceVar(&flagTools, "tools", nil, "comma-separated list of tools to enable (default: all)")

	// File watcher flag (serve only).
	serveCmd.Flags().BoolVar(&flagWatch, "watch", false, "auto re-index on file changes")

	rootCmd.AddCommand(serveCmd, indexCmd, versionCmd)
}

func runIndex(cmd *cobra.Command, args []string) error {
	logLevel := parseLogLevel(flagLogLevel)
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{
		Level: logLevel,
	})))

	cfg, err := config.Load(flagConfigFile)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}
	if flagDBPath != "" {
		cfg.DBPath = flagDBPath
	}
	if flagProjectRoot != "" {
		cfg.ProjectRoot = flagProjectRoot
	}

	// Determine the path to index (always resolve to absolute so that
	// filepath.Base returns a real directory name, not ".").
	indexPath := cfg.ProjectRoot
	if len(args) > 0 {
		indexPath = args[0]
	}
	indexPath, err = filepath.Abs(indexPath)
	if err != nil {
		return fmt.Errorf("failed to resolve index path %s: %w", indexPath, err)
	}

	// Determine repo name.
	repoName := flagRepoName
	if repoName == "" {
		repoName = filepath.Base(indexPath)
	}

	// Open and migrate the database.
	db, err := store.Open(cfg.DBPath)
	if err != nil {
		return fmt.Errorf("failed to open database: %w", err)
	}
	defer db.Close()

	if err := store.Migrate(db); err != nil {
		return fmt.Errorf("failed to run migrations: %w", err)
	}

	// Apply CLI flag overrides for model paths.
	if flagModelPath != "" {
		cfg.ModelPath = flagModelPath
	}
	if flagVocabPath != "" {
		cfg.VocabPath = flagVocabPath
	}
	if flagORTLibPath != "" {
		cfg.ORTLibPath = flagORTLibPath
	}

	// Create embedder: use ONNX if explicitly configured, otherwise built-in Model2Vec.
	var embedder vectorstore.Embedder
	if cfg.ModelPath != "" && cfg.VocabPath != "" {
		e, err := vectorstore.NewONNXEmbedder(cfg.ModelPath, cfg.VocabPath, cfg.ORTLibPath)
		if err != nil {
			slog.Warn("ONNX embedder failed, falling back to built-in Model2Vec", "error", err)
			embedder = vectorstore.NewModel2VecEmbedder()
		} else {
			embedder = e
			defer e.Close() //nolint:errcheck
			slog.Info("embedding generation enabled (ONNX)", "model", cfg.ModelPath)
		}
	} else {
		embedder = vectorstore.NewModel2VecEmbedder()
		slog.Info("embedding generation enabled (built-in Model2Vec)")
	}

	// Validate embedding dimension matches stored data.
	if err := vectorstore.EnsureEmbeddingDimension(context.Background(), db, embedder.Dim()); err != nil {
		slog.Warn("embedding dimension mismatch — re-index with --force to use new embedder",
			"error", err)
	}

	// Build language registry with default adapters.
	registry := buildLanguageRegistry()

	// Run the indexer.
	idx := indexer.NewIndexer(registry, db, flagWorkers, embedder).WithForce(flagForce)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	stats, err := idx.IndexRepo(ctx, repoName, indexPath)
	if err != nil {
		return fmt.Errorf("indexing failed: %w", err)
	}

	fmt.Fprintf(os.Stderr, "\nIndexing complete:\n")
	fmt.Fprintf(os.Stderr, "  Files discovered: %d\n", stats.FilesDiscovered)
	fmt.Fprintf(os.Stderr, "  Files indexed:    %d\n", stats.FilesIndexed)
	fmt.Fprintf(os.Stderr, "  Files unchanged:  %d\n", stats.FilesUnchanged)
	fmt.Fprintf(os.Stderr, "  Files deleted:    %d\n", stats.FilesDeleted)
	fmt.Fprintf(os.Stderr, "  Files skipped:    %d\n", stats.FilesSkipped)
	fmt.Fprintf(os.Stderr, "  Symbols extracted:    %d\n", stats.SymbolsExtracted)
	fmt.Fprintf(os.Stderr, "  Dependencies:         %d\n", stats.DepsResolved)
	fmt.Fprintf(os.Stderr, "  Embeddings generated: %d\n", stats.EmbeddingsGenerated)
	fmt.Fprintf(os.Stderr, "  Memories orphaned:    %d\n", stats.MemoriesOrphaned)
	fmt.Fprintf(os.Stderr, "  Memories relinked:    %d\n", stats.MemoriesRelinked)
	fmt.Fprintf(os.Stderr, "  Errors:               %d\n", stats.Errors)
	fmt.Fprintf(os.Stderr, "  Duration:             %s\n", stats.Duration)

	return nil
}

// buildLanguageRegistry creates a LanguageRegistry with all built-in adapters.
func buildLanguageRegistry() *indexer.LanguageRegistry {
	registry := indexer.NewLanguageRegistry()

	// Register TypeScript adapter (.ts files).
	if err := registry.Register(adapters.NewTypeScriptAdapter()); err != nil {
		slog.Error("failed to register TypeScript adapter", "error", err)
	}

	// Register TSX adapter (.tsx, .jsx files).
	if err := registry.Register(adapters.NewTSXAdapter()); err != nil {
		slog.Error("failed to register TSX adapter", "error", err)
	}

	// Register Go adapter (.go files).
	if err := registry.Register(adapters.NewGoAdapter()); err != nil {
		slog.Error("failed to register Go adapter", "error", err)
	}

	// Register Python adapter (.py files).
	if err := registry.Register(adapters.NewPythonAdapter()); err != nil {
		slog.Error("failed to register Python adapter", "error", err)
	}

	// Register JavaScript adapter (.js, .mjs files).
	if err := registry.Register(adapters.NewJavaScriptAdapter()); err != nil {
		slog.Error("failed to register JavaScript adapter", "error", err)
	}

	// Register Rust adapter (.rs files).
	if err := registry.Register(adapters.NewRustAdapter()); err != nil {
		slog.Error("failed to register Rust adapter", "error", err)
	}

	// Register Java adapter (.java files).
	if err := registry.Register(adapters.NewJavaAdapter()); err != nil {
		slog.Error("failed to register Java adapter", "error", err)
	}

	// Register C adapter (.c, .h files).
	if err := registry.Register(adapters.NewCAdapter()); err != nil {
		slog.Error("failed to register C adapter", "error", err)
	}

	// Register C++ adapter (.cpp, .hpp, .cc, .cxx, .hxx, .hh files).
	if err := registry.Register(adapters.NewCppAdapter()); err != nil {
		slog.Error("failed to register C++ adapter", "error", err)
	}

	// Register C# adapter (.cs files).
	if err := registry.Register(adapters.NewCSharpAdapter()); err != nil {
		slog.Error("failed to register C# adapter", "error", err)
	}

	return registry
}

func runServe(cmd *cobra.Command, args []string) error {
	// Configure structured logging.
	logLevel := parseLogLevel(flagLogLevel)
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{
		Level: logLevel,
	})))

	// Load configuration, then apply CLI flag overrides.
	cfg, err := config.Load(flagConfigFile)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}
	if flagDBPath != "" {
		cfg.DBPath = flagDBPath
	}
	if flagProjectRoot != "" {
		cfg.ProjectRoot = flagProjectRoot
	}
	if flagLogLevel != "" {
		cfg.LogLevel = flagLogLevel
	}

	// Open and migrate the database.
	db, err := store.Open(cfg.DBPath)
	if err != nil {
		return fmt.Errorf("failed to open database: %w", err)
	}
	defer db.Close()

	if err := store.Migrate(db); err != nil {
		return fmt.Errorf("failed to run migrations: %w", err)
	}

	// Apply CLI flag overrides for model paths.
	if flagModelPath != "" {
		cfg.ModelPath = flagModelPath
	}
	if flagVocabPath != "" {
		cfg.VocabPath = flagVocabPath
	}
	if flagORTLibPath != "" {
		cfg.ORTLibPath = flagORTLibPath
	}

	// Create embedder: use ONNX if explicitly configured, otherwise built-in Model2Vec.
	var embedder vectorstore.Embedder
	if cfg.ModelPath != "" && cfg.VocabPath != "" {
		e, err := vectorstore.NewONNXEmbedder(cfg.ModelPath, cfg.VocabPath, cfg.ORTLibPath)
		if err != nil {
			slog.Warn("ONNX embedder failed, falling back to built-in Model2Vec", "error", err)
			embedder = vectorstore.NewModel2VecEmbedder()
		} else {
			embedder = e
			slog.Info("semantic search enabled (ONNX)", "model", cfg.ModelPath)
		}
	} else {
		embedder = vectorstore.NewModel2VecEmbedder()
		slog.Info("semantic search enabled (built-in Model2Vec)")
	}

	// Validate embedding dimension matches stored data.
	if err := vectorstore.EnsureEmbeddingDimension(context.Background(), db, embedder.Dim()); err != nil {
		slog.Warn("embedding dimension mismatch — re-index with --force to use new embedder",
			"error", err)
	}

	// Apply tool filter from CLI flag.
	if len(flagTools) > 0 {
		cfg.Tools = flagTools
	}

	// Build MCP server with all tools registered.
	srv := server.New(cfg, db, embedder, version)

	// Graceful shutdown on SIGINT/SIGTERM.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Start file watcher if --watch is enabled.
	if flagWatch {
		registry := buildLanguageRegistry()
		repoName := filepath.Base(cfg.ProjectRoot)
		w := watcher.New(registry, db, embedder, cfg.ProjectRoot, repoName, 4)
		go func() {
			if err := w.Watch(ctx); err != nil {
				slog.Error("file watcher failed", "error", err)
			}
		}()
	}

	return srv.Run(ctx)
}

// parseLogLevel converts a string level to slog.Level.
func parseLogLevel(level string) slog.Level {
	switch level {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
