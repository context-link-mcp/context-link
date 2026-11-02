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

// TestResolveProject verifies that the project root is made absolute and
// cleaned, and that the repo name defaults to the root's directory name.
func TestResolveProject(t *testing.T) {
	t.Parallel()

	cwd, err := os.Getwd()
	require.NoError(t, err)
	abs := filepath.Join(t.TempDir(), "myrepo")

	tests := []struct {
		name     string
		root     string
		repoName string
		wantRoot string
		wantRepo string
	}{
		{"dot", ".", "", cwd, filepath.Base(cwd)},
		{"empty means cwd", "", "", cwd, filepath.Base(cwd)},
		{"relative child", "sub", "", filepath.Join(cwd, "sub"), "sub"},
		{"trailing separator", abs + string(filepath.Separator), "", abs, "myrepo"},
		{"unclean absolute", filepath.Join(abs, "x", ".."), "", abs, "myrepo"},
		{"explicit repo name wins", ".", "custom", cwd, "custom"},
		{"absolute with repo name", abs, "other", abs, "other"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			root, repo, err := ResolveProject(tc.root, tc.repoName)
			require.NoError(t, err)
			assert.True(t, filepath.IsAbs(root), "root must be absolute: %q", root)
			assert.Equal(t, tc.wantRoot, root)
			assert.Equal(t, tc.wantRepo, repo)
			assert.NotEqual(t, ".", repo)
		})
	}
}
