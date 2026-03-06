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

	"github.com/context-link/context-link/internal/config"
	"github.com/context-link/context-link/internal/indexer"
	"github.com/context-link/context-link/internal/indexer/adapters"
	"github.com/context-link/context-link/internal/server"
	"github.com/context-link/context-link/internal/store"
)

const version = "0.1.0"

// CLI flag values — populated by cobra before command functions run.
var (
	flagDBPath      string
	flagProjectRoot string
	flagLogLevel    string
	flagConfigFile  string
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

	// Determine the path to index.
	indexPath := cfg.ProjectRoot
	if len(args) > 0 {
		indexPath, err = filepath.Abs(args[0])
		if err != nil {
			return fmt.Errorf("failed to resolve path %s: %w", args[0], err)
		}
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

	// Build language registry with default adapters.
	registry := buildLanguageRegistry()

	// Run the indexer.
	idx := indexer.NewIndexer(registry, db, flagWorkers)

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
	fmt.Fprintf(os.Stderr, "  Files skipped:    %d\n", stats.FilesSkipped)
	fmt.Fprintf(os.Stderr, "  Symbols extracted: %d\n", stats.SymbolsExtracted)
	fmt.Fprintf(os.Stderr, "  Dependencies:     %d\n", stats.DepsResolved)
	fmt.Fprintf(os.Stderr, "  Errors:           %d\n", stats.Errors)
	fmt.Fprintf(os.Stderr, "  Duration:         %s\n", stats.Duration)

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

	// Build MCP server with all tools registered.
	srv := server.New(cfg, db)

	// Graceful shutdown on SIGINT/SIGTERM.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

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
