package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/context-link-mcp/context-link/internal/config"
	"github.com/context-link-mcp/context-link/internal/extract"
	"github.com/context-link-mcp/context-link/internal/indexer"
	"github.com/context-link-mcp/context-link/internal/store"
	"github.com/context-link-mcp/context-link/internal/vectorstore"
)

// Exit codes of the extract command.
const (
	exitUsage         = 1 // bad flags
	exitIndexUnusable = 2 // no usable index; callers should continue without code context
)

// exitError makes main exit with a specific code.
type exitError struct {
	code int
	err  error
}

func (e *exitError) Error() string { return e.err.Error() }
func (e *exitError) Unwrap() error { return e.err }

// exitCode returns the process exit code for an error from a command.
func exitCode(err error) int {
	var ee *exitError
	if errors.As(err, &ee) {
		return ee.code
	}
	return 1
}

var (
	flagExtractQueries  []string
	flagExtractProfile  string
	flagExtractBudget   int
	flagExtractTests    bool
	flagExtractPerFile  int
	flagExtractFormat   string
	flagExtractNoIndex  bool
	flagExtractWorkers  int
	flagExtractRepoName string
	flagExtractDepDepth int
	flagExtractReserve  int
)

var extractCmd = &cobra.Command{
	Use:   "extract",
	Short: "Print a ranked, budgeted code-context bundle as JSON",
	Long: `Indexes the project incrementally, then ranks symbols against the given
queries and prints one JSON document: entry points (HTTP routes), the best
files packed into a size budget, and index health (parse failures).

Exit codes: 0 ok, 1 usage error, 2 index unusable.`,
	Args: cobra.NoArgs,
	RunE: runExtract,
}

func registerExtractFlags() {
	f := extractCmd.Flags()
	f.StringArrayVar(&flagExtractQueries, "query", nil, "natural-language query (repeatable)")
	f.StringVar(&flagExtractProfile, "profile", "", "query profile: threat-model")
	f.IntVar(&flagExtractBudget, "budget-bytes", extract.DefaultBudgetBytes, "limit for the summed size of files[].content")
	f.BoolVar(&flagExtractTests, "include-tests", false, "include tests, fixtures, vendored code and migrations")
	f.IntVar(&flagExtractPerFile, "max-symbols-per-file", extract.DefaultMaxSymbolsPerFile, "maximum symbols returned per file")
	f.IntVar(&flagExtractDepDepth, "dependency-depth", 1, "0: list direct dependencies as references only; 1: also pack their code")
	f.IntVar(&flagExtractReserve, "dependency-reserve", extract.DefaultDependencyReservePercent,
		"percent of the budget (0-90) kept for dependency code before lower-ranked matches; unused reserve goes back to matches")
	f.StringVar(&flagExtractFormat, "format", "json", "output format (only json)")
	f.BoolVar(&flagExtractNoIndex, "no-index", false, "skip the incremental index step and use the existing index")
	f.IntVar(&flagExtractWorkers, "workers", 4, "parallel workers for the index step")
	f.StringVar(&flagExtractRepoName, "repo-name", "", "repository name (default: directory name)")
	f.StringVar(&flagModelPath, "model-path", "", "path to all-MiniLM-L6-v2.onnx (enables ONNX embeddings)")
	f.StringVar(&flagVocabPath, "vocab-path", "", "path to vocab.txt for the ONNX model tokenizer")
	f.StringVar(&flagORTLibPath, "ort-lib-path", "", "path to OnnxRuntime shared library")
}

func runExtract(cmd *cobra.Command, args []string) error {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{
		Level: parseLogLevel(flagLogLevel),
	})))

	if err := validateExtractFlags(extractFlagValues{
		format: flagExtractFormat, profile: flagExtractProfile, queries: flagExtractQueries,
		depDepth: flagExtractDepDepth, reserve: flagExtractReserve,
		budget: flagExtractBudget, perFile: flagExtractPerFile,
	}); err != nil {
		return &exitError{exitUsage, err}
	}

	cfg, err := config.Load(flagConfigFile)
	if err != nil {
		return &exitError{exitUsage, fmt.Errorf("failed to load config: %w", err)}
	}
	if flagDBPath != "" {
		cfg.DBPath = flagDBPath
	}
	if flagProjectRoot != "" {
		cfg.ProjectRoot = flagProjectRoot
	}
	root, repoName, err := config.ResolveProject(cfg.ProjectRoot, flagExtractRepoName)
	if err != nil {
		return &exitError{exitUsage, err}
	}
	cfg.DBPath = config.DBPathFor(cfg.DBPath, root)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	db, err := store.Open(cfg.DBPath)
	if err != nil {
		return &exitError{exitIndexUnusable, fmt.Errorf("failed to open database: %w", err)}
	}
	defer db.Close()
	if err := store.Migrate(db); err != nil {
		return &exitError{exitIndexUnusable, fmt.Errorf("failed to run migrations: %w", err)}
	}

	if flagModelPath != "" {
		cfg.ModelPath = flagModelPath
	}
	if flagVocabPath != "" {
		cfg.VocabPath = flagVocabPath
	}
	if flagORTLibPath != "" {
		cfg.ORTLibPath = flagORTLibPath
	}
	embedder, closeEmbedder := newEmbedder(cfg)
	defer closeEmbedder()
	if err := vectorstore.EnsureEmbeddingDimension(ctx, db, embedder.Dim()); err != nil {
		slog.Warn("embedding dimension mismatch — re-index with `index --force` to use the new embedder", "error", err)
	}

	var info extract.IndexInfo
	if !flagExtractNoIndex {
		idx := indexer.NewIndexer(buildLanguageRegistry(), db, flagExtractWorkers, embedder)
		stats, err := idx.IndexRepo(ctx, repoName, root)
		if err != nil {
			return &exitError{exitIndexUnusable, fmt.Errorf("indexing failed: %w", err)}
		}
		info.FilesDiscovered = stats.FilesDiscovered
		info.ParseFailures = stats.FailedFiles
	}

	if n, err := store.CountSymbols(ctx, db, repoName); err != nil || n == 0 {
		return &exitError{exitIndexUnusable, fmt.Errorf("no indexed symbols for %q in %s", repoName, cfg.DBPath)}
	}

	res, err := extract.Run(ctx, db, embedder, vectorstore.NewVectorCache(repoName), extract.Options{
		RepoName:           repoName,
		Queries:            flagExtractQueries,
		Profile:            flagExtractProfile,
		BudgetBytes:        flagExtractBudget,
		IncludeTests:       flagExtractTests,
		MaxSymbolsPerFile:  flagExtractPerFile,
		SkipDependencyCode: flagExtractDepDepth == 0,

		DependencyReservePercent: flagExtractReserve,
	}, info)
	if err != nil {
		return &exitError{exitIndexUnusable, err}
	}

	return json.NewEncoder(os.Stdout).Encode(res)
}

// extractFlagValues are the extract flags checked by validateExtractFlags.
type extractFlagValues struct {
	format, profile   string
	queries           []string
	depDepth, reserve int
	budget, perFile   int
}

// validateExtractFlags rejects invalid extract flag values; runExtract exits
// with exitUsage on its error.
func validateExtractFlags(f extractFlagValues) error {
	if f.format != "json" {
		return fmt.Errorf("unsupported --format %q (only json)", f.format)
	}
	if f.profile != "" && f.profile != extract.ProfileThreatModel {
		return fmt.Errorf("unknown --profile %q (want %q)", f.profile, extract.ProfileThreatModel)
	}
	if len(f.queries) == 0 && f.profile == "" {
		return errors.New("at least one --query or a --profile is required")
	}
	if f.depDepth != 0 && f.depDepth != 1 {
		return fmt.Errorf("unsupported --dependency-depth %d (want 0 or 1)", f.depDepth)
	}
	if f.reserve < 0 || f.reserve > extract.MaxDependencyReservePercent {
		return fmt.Errorf("unsupported --dependency-reserve %d (want 0-%d)", f.reserve, extract.MaxDependencyReservePercent)
	}
	// extract.Run treats 0 as "use the default"; on the command line a
	// non-positive value is a mistake, not a request for the default.
	if f.budget <= 0 {
		return fmt.Errorf("unsupported --budget-bytes %d (want a positive number)", f.budget)
	}
	if f.perFile <= 0 {
		return fmt.Errorf("unsupported --max-symbols-per-file %d (want a positive number)", f.perFile)
	}
	return nil
}

// newEmbedder returns the ONNX embedder when configured and it initializes,
// else the built-in Model2Vec one (see vectorstore.NewConfiguredEmbedder),
// plus a cleanup func. Shared by index, serve and extract.
func newEmbedder(cfg *config.Config) (vectorstore.Embedder, func()) {
	e := vectorstore.NewConfiguredEmbedder(cfg.ModelPath, cfg.VocabPath, cfg.ORTLibPath)
	if _, ok := e.(*vectorstore.ONNXEmbedder); ok {
		slog.Info("embeddings: ONNX model", "model", cfg.ModelPath)
	} else {
		slog.Info("embeddings: built-in Model2Vec")
	}
	return e, func() { _ = e.Close() }
}
