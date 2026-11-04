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
	assert.Empty(t, cfg.DBPath, "no db_path: callers default it under the project root (DBPathFor)")
	assert.True(t, filepath.IsAbs(cfg.ProjectRoot), "ProjectRoot should be absolute")
}

// TestDBPathFor is the M18 regression for the default database location: it
// lives in the project root, not in the working directory, so index, serve
// and extract started from anywhere use the same file. An explicit path wins.
func TestDBPathFor(t *testing.T) {
	t.Parallel()
	root := filepath.Join(t.TempDir(), "proj")
	assert.Equal(t, filepath.Join(root, ".context-link.db"), DBPathFor("", root))
	assert.Equal(t, "custom.db", DBPathFor("custom.db", root))
	abs := filepath.Join(t.TempDir(), "x.db")
	assert.Equal(t, abs, DBPathFor(abs, root))
}

// TestLoad_EnvironmentVariables is the M18 regression for env overrides:
// keys without a default (model_path, vocab_path, ort_lib_path, tools,
// db_path) were ignored by viper's AutomaticEnv. Not parallel: t.Setenv.
func TestLoad_EnvironmentVariables(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CONTEXT_LINK_DB_PATH", filepath.Join(dir, "env.db"))
	t.Setenv("CONTEXT_LINK_PROJECT_ROOT", dir)
	t.Setenv("CONTEXT_LINK_LOG_LEVEL", "warn")
	t.Setenv("CONTEXT_LINK_MODEL_PATH", "/models/model.onnx")
	t.Setenv("CONTEXT_LINK_VOCAB_PATH", "/models/vocab.txt")
	t.Setenv("CONTEXT_LINK_ORT_LIB_PATH", "/lib/onnxruntime.so")
	t.Setenv("CONTEXT_LINK_TOOLS", "ping,get_code_by_symbol")

	cfg, err := Load("")
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(dir, "env.db"), cfg.DBPath)
	assert.Equal(t, dir, cfg.ProjectRoot)
	assert.Equal(t, "warn", cfg.LogLevel)
	assert.Equal(t, "/models/model.onnx", cfg.ModelPath)
	assert.Equal(t, "/models/vocab.txt", cfg.VocabPath)
	assert.Equal(t, "/lib/onnxruntime.so", cfg.ORTLibPath)
	assert.Equal(t, []string{"ping", "get_code_by_symbol"}, cfg.Tools)
}

// TestLoad_EnvironmentOverridesFile checks the precedence: environment over
// config file. Not parallel: t.Setenv.
func TestLoad_EnvironmentOverridesFile(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, ".context-link.yaml")
	require.NoError(t, os.WriteFile(cfgPath, []byte("model_path: /file/model.onnx\ntools:\n  - ping\n"), 0o600))
	t.Setenv("CONTEXT_LINK_MODEL_PATH", "/env/model.onnx")

	cfg, err := Load(cfgPath)
	require.NoError(t, err)
	assert.Equal(t, "/env/model.onnx", cfg.ModelPath)
	assert.Equal(t, []string{"ping"}, cfg.Tools, "keys without an env var keep the file value")
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
