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

// TestDefaultDBPathIsInProjectRoot is the M18 regression for the default
// database: index and serve run from another directory must both use
// <root>/.context-link.db; an explicit --db-path is kept as given.
func TestDefaultDBPathIsInProjectRoot(t *testing.T) {
	t.Parallel()
	root := filepath.Join(t.TempDir(), "proj")
	want := filepath.Join(root, ".context-link.db")

	srvCfg := &config.Config{ProjectRoot: root}
	require.NoError(t, resolveServeProject(srvCfg, ""))
	assert.Equal(t, want, srvCfg.DBPath)

	idxCfg := &config.Config{ProjectRoot: "/elsewhere"}
	idxRoot, _, err := resolveIndexTarget(idxCfg, []string{root}, "")
	require.NoError(t, err)
	assert.Equal(t, want, config.DBPathFor(idxCfg.DBPath, idxRoot), "the [path] argument picks the root and so the DB")

	explicit := &config.Config{ProjectRoot: root, DBPath: "custom.db"}
	require.NoError(t, resolveServeProject(explicit, ""))
	assert.Equal(t, "custom.db", explicit.DBPath)
}

// TestValidateExtractFlags checks extract's usage errors (exit code 1),
// including the non-positive --budget-bytes / --max-symbols-per-file that
// used to fall back to the defaults silently.
func TestValidateExtractFlags(t *testing.T) {
	t.Parallel()
	valid := extractFlagValues{format: "json", queries: []string{"auth"}, depDepth: 1, reserve: 25, budget: 50000, perFile: 3}
	require.NoError(t, validateExtractFlags(valid))

	tests := []struct {
		name   string
		modify func(*extractFlagValues)
		want   string
	}{
		{"format", func(f *extractFlagValues) { f.format = "yaml" }, "--format"},
		{"profile", func(f *extractFlagValues) { f.profile = "x" }, "--profile"},
		{"no query", func(f *extractFlagValues) { f.queries = nil }, "--query"},
		{"depth", func(f *extractFlagValues) { f.depDepth = 2 }, "--dependency-depth"},
		{"reserve", func(f *extractFlagValues) { f.reserve = 91 }, "--dependency-reserve"},
		{"negative budget", func(f *extractFlagValues) { f.budget = -1 }, "--budget-bytes"},
		{"zero budget", func(f *extractFlagValues) { f.budget = 0 }, "--budget-bytes"},
		{"negative per file", func(f *extractFlagValues) { f.perFile = -3 }, "--max-symbols-per-file"},
		{"zero per file", func(f *extractFlagValues) { f.perFile = 0 }, "--max-symbols-per-file"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := valid
			tc.modify(&f)
			err := validateExtractFlags(f)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
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
