package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoad_Defaults(t *testing.T) {
	t.Parallel()

	// Load with no config file — should use defaults.
	cfg, err := Load("")
	require.NoError(t, err)
	require.NotNil(t, cfg)

	assert.Equal(t, "info", cfg.LogLevel)
	assert.True(t, filepath.IsAbs(cfg.DBPath), "DBPath should be absolute")
	assert.True(t, filepath.IsAbs(cfg.ProjectRoot), "ProjectRoot should be absolute")
	assert.Contains(t, cfg.DBPath, ".context-link.db")
}

func TestLoad_FromFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, ".context-link.yaml")

	content := []byte("db_path: custom.db\nlog_level: debug\n")
	err := os.WriteFile(cfgPath, content, 0o600)
	require.NoError(t, err)

	cfg, err := Load(cfgPath)
	require.NoError(t, err)
	require.NotNil(t, cfg)

	assert.Equal(t, "debug", cfg.LogLevel)
	// db_path is relative, so it should be resolved to absolute.
	assert.True(t, filepath.IsAbs(cfg.DBPath))
	assert.Contains(t, cfg.DBPath, "custom.db")
}

func TestLoad_NonexistentExplicitFile(t *testing.T) {
	t.Parallel()

	// Explicitly specifying a config file that doesn't exist should error.
	_, err := Load(filepath.Join(t.TempDir(), "nonexistent.yaml"))
	assert.Error(t, err, "should fail when explicit config file is missing")
}

func TestLoad_ProjectRootAbsolute(t *testing.T) {
	t.Parallel()

	cfg, err := Load("")
	require.NoError(t, err)

	cwd, err := os.Getwd()
	require.NoError(t, err)

	assert.Equal(t, cwd, cfg.ProjectRoot)
}
