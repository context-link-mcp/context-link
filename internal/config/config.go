// Package config handles configuration loading for context-link.
// Configuration is loaded from a .context-link.yaml file in the working
// directory, with overrides from CLI flags.
package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/viper"
)

const configFileName = ".context-link"

// Config holds all runtime configuration for the context-link server.
type Config struct {
	// DBPath is the path to the SQLite database file.
	DBPath string `mapstructure:"db_path"`
	// ProjectRoot is the root directory of the project to index.
	ProjectRoot string `mapstructure:"project_root"`
	// RepoName namespaces the project's rows in the database. It is set from
	// the --repo-name flag (see ResolveProject); when empty, consumers derive
	// it from filepath.Base(ProjectRoot). Not read from the config file.
	RepoName string `mapstructure:"-"`
	// LogLevel controls verbosity: "debug", "info", "warn", "error".
	LogLevel string `mapstructure:"log_level"`

	// Phase 3: Semantic search — leave empty to disable semantic search.

	// ModelPath is the path to the all-MiniLM-L6-v2.onnx model file.
	ModelPath string `mapstructure:"model_path"`
	// VocabPath is the path to the accompanying vocab.txt tokenizer vocabulary.
	VocabPath string `mapstructure:"vocab_path"`
	// ORTLibPath is the path to the OnnxRuntime shared library.
	// Leave empty to use the system default library search path.
	ORTLibPath string `mapstructure:"ort_lib_path"`

	// Tools lists which MCP tools to expose. If empty, all tools are enabled.
	// Use this to control prompt token budget by disabling unused tools.
	// Example: ["ping", "semantic_search_symbols", "get_code_by_symbol"]
	Tools []string `mapstructure:"tools"`
}

// DefaultDBFile is the database file name used when no db_path is given; it
// is created in the project root (see DBPathFor).
const DefaultDBFile = ".context-link.db"

// envPrefix prefixes the environment variable of every config key, e.g.
// CONTEXT_LINK_MODEL_PATH for model_path.
const envPrefix = "CONTEXT_LINK"

// configKeys lists every key read from the config file and environment.
// Each is bound to its environment variable explicitly: viper's AutomaticEnv
// alone only applies to keys viper already knows from a default or the
// config file, so e.g. CONTEXT_LINK_MODEL_PATH was silently ignored.
var configKeys = []string{
	"db_path", "project_root", "log_level",
	"model_path", "vocab_path", "ort_lib_path",
	"tools",
}

// Load reads the configuration from the config file and environment variables
// (CONTEXT_LINK_<KEY>, e.g. CONTEXT_LINK_DB_PATH; CONTEXT_LINK_TOOLS is a
// comma-separated list). Environment variables override the config file.
// CLI flag values can be applied after loading by directly setting fields.
//
// DBPath is left empty unless db_path is set; callers resolve the default
// against the project root with DBPathFor. A relative db_path is resolved
// against the working directory.
func Load(cfgFile string) (*Config, error) {
	v := viper.New()

	// Set defaults.
	v.SetDefault("log_level", "info")

	// Determine project root default: current working directory.
	cwd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("config: failed to get working directory: %w", err)
	}
	v.SetDefault("project_root", cwd)

	// Config file location.
	if cfgFile != "" {
		v.SetConfigFile(cfgFile)
	} else {
		v.SetConfigName(configFileName)
		v.SetConfigType("yaml")
		v.AddConfigPath(".")
		v.AddConfigPath(cwd)
	}

	// Allow environment variable overrides prefixed with CONTEXT_LINK_.
	v.SetEnvPrefix(envPrefix)
	for _, key := range configKeys {
		if err := v.BindEnv(key); err != nil {
			return nil, fmt.Errorf("config: failed to bind environment variable for %s: %w", key, err)
		}
	}

	// Read config file (ignore not-found errors — it's optional).
	if err := v.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			return nil, fmt.Errorf("config: failed to read config file: %w", err)
		}
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("config: failed to unmarshal config: %w", err)
	}

	// An explicit relative DB path is resolved against the working dir.
	if cfg.DBPath != "" && !filepath.IsAbs(cfg.DBPath) {
		cfg.DBPath = filepath.Join(cwd, cfg.DBPath)
	}

	// Resolve project root to absolute path.
	if !filepath.IsAbs(cfg.ProjectRoot) {
		cfg.ProjectRoot, err = filepath.Abs(cfg.ProjectRoot)
		if err != nil {
			return nil, fmt.Errorf("config: failed to resolve project root: %w", err)
		}
	}

	return &cfg, nil
}

// DBPathFor returns dbPath if it is set (from --db-path, the config file or
// CONTEXT_LINK_DB_PATH), else DefaultDBFile in the resolved project root, so
// that index, serve and extract use the same database for a project whatever
// the working directory.
func DBPathFor(dbPath, projectRoot string) string {
	if dbPath != "" {
		return dbPath
	}
	return filepath.Join(projectRoot, DefaultDBFile)
}

// ResolveProject returns the absolute, cleaned project root and the repo name
// used to namespace it. An empty repoName defaults to the root's directory
// name. index, extract and serve all resolve through this function so that a
// relative root such as "." yields the same repo name in every command.
func ResolveProject(root, repoName string) (absRoot, name string, err error) {
	absRoot, err = filepath.Abs(root) // Abs also cleans the path.
	if err != nil {
		return "", "", fmt.Errorf("config: failed to resolve project root %q: %w", root, err)
	}
	name = repoName
	if name == "" {
		name = filepath.Base(absRoot)
	}
	return absRoot, name, nil
}
