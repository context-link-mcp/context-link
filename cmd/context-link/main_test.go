package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/context-link-mcp/context-link/internal/config"
)

// TestServeAndIndexResolveSameRepo is the H7 regression: `serve
// --project-root .` must query the same repo name and absolute root that
// `index --project-root .` wrote, not the literal ".".
// Not parallel: it changes the working directory.
func TestServeAndIndexResolveSameRepo(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "myrepo")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	t.Chdir(dir)
	// Derive the expected root from Getwd (via Abs), not from dir, so a
	// symlinked temp dir (macOS /var -> /private/var) still compares equal.
	wantRoot, err := filepath.Abs(".")
	require.NoError(t, err)

	tests := []struct {
		name         string
		projectRoot  string
		args         []string
		repoNameFlag string
		wantRepo     string
	}{
		{"dot", ".", nil, "", "myrepo"},
		{"dot with trailing separator", "." + string(filepath.Separator), nil, "", "myrepo"},
		{"dot via index path arg", "/elsewhere", []string{"."}, "", "myrepo"},
		{"explicit repo name", ".", nil, "custom", "custom"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			idxCfg := &config.Config{ProjectRoot: tc.projectRoot}
			idxRoot, idxRepo, err := resolveIndexTarget(idxCfg, tc.args, tc.repoNameFlag)
			require.NoError(t, err)

			serveRootFlag := tc.projectRoot
			if len(tc.args) > 0 {
				serveRootFlag = tc.args[0]
			}
			srvCfg := &config.Config{ProjectRoot: serveRootFlag}
			require.NoError(t, resolveServeProject(srvCfg, tc.repoNameFlag))

			assert.Equal(t, wantRoot, idxRoot)
			assert.Equal(t, tc.wantRepo, idxRepo)
			assert.Equal(t, idxRoot, srvCfg.ProjectRoot, "serve must use the same absolute root as index")
			assert.Equal(t, idxRepo, srvCfg.RepoName, "serve must query the repo name index wrote")
		})
	}
}

// TestServeHasRepoNameFlag checks serve exposes --repo-name like index does.
func TestServeHasRepoNameFlag(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"serve", "index", "extract"} {
		cmd, _, err := rootCmd.Find([]string{name})
		require.NoError(t, err)
		assert.NotNil(t, cmd.Flags().Lookup("repo-name"), "%s must have --repo-name", name)
	}
}
