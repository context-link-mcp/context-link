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
	// LogLevel controls verbosity: "debug", "info", "warn", "error".
	LogLevel string `mapstructure:"log_level"`
}

// Load reads the configuration from the config file and environment variables.
// CLI flag values can be applied after loading by directly setting fields.
func Load(cfgFile string) (*Config, error) {
	v := viper.New()

	// Set defaults.
	v.SetDefault("db_path", ".context-link.db")
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
	v.SetEnvPrefix("CONTEXT_LINK")
	v.AutomaticEnv()

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

	// Resolve DB path relative to working dir if it is not absolute.
	if !filepath.IsAbs(cfg.DBPath) {
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
