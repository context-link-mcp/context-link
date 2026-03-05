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

	"github.com/spf13/cobra"

	"github.com/context-link/context-link/internal/config"
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

var indexCmd = &cobra.Command{
	Use:   "index [path]",
	Short: "Index a project directory (Phase 2)",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Fprintln(os.Stderr, "index command will be available in Phase 2")
		return nil
	},
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

	rootCmd.AddCommand(serveCmd, indexCmd, versionCmd)
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
