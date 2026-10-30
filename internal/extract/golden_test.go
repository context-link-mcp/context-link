package extract

import (
	"context"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/context-link-mcp/context-link/internal/indexer"
	"github.com/context-link-mcp/context-link/internal/indexer/adapters"
	"github.com/context-link-mcp/context-link/internal/store"
	"github.com/context-link-mcp/context-link/internal/vectorstore"
)

var updateGolden = flag.Bool("update-golden", false, "update golden extract output files")

// goldenResult indexes testdata/extract and runs extract with opts.
func goldenResult(t *testing.T, opts Options) *Result {
	t.Helper()
	ctx := context.Background()

	db, err := store.Open(filepath.Join(t.TempDir(), "golden.db"))
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })
	require.NoError(t, store.Migrate(db))

	registry := indexer.NewLanguageRegistry()
	require.NoError(t, registry.Register(adapters.NewPythonAdapter()))
	require.NoError(t, registry.Register(adapters.NewJavaScriptAdapter()))
	embedder := vectorstore.NewModel2VecEmbedder()

	root := filepath.Join("..", "..", "testdata", "extract")
	stats, err := indexer.NewIndexer(registry, db, 1, embedder).IndexRepo(ctx, "extract", root)
	require.NoError(t, err)
	require.Equal(t, 0, stats.Errors)

	opts.RepoName = "extract"
	res, err := Run(ctx, db, embedder, nil, opts, IndexInfo{
		FilesDiscovered: stats.FilesDiscovered,
		ParseFailures:   stats.FailedFiles,
	})
	require.NoError(t, err)
	assert.Equal(t, res.Budget.UsedBytes, usedBytes(res.Files), "used_bytes must equal the summed content size")
	return res
}

// compareGolden marshals res and compares it with testdata/golden/<name>.
// Line endings are normalised so a CRLF checkout does not change contents.
func compareGolden(t *testing.T, name string, res *Result) {
	t.Helper()
	data, err := json.MarshalIndent(res, "", "  ")
	require.NoError(t, err)
	got := strings.ReplaceAll(string(data), `\r\n`, `\n`)

	path := filepath.Join("..", "..", "testdata", "golden", name)
	if *updateGolden {
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(got+"\n"), 0o644))
		t.Logf("updated golden file %s", path)
		return
	}

	want, err := os.ReadFile(path)
	require.NoError(t, err, "golden file missing — run with -update-golden to create it")
	assert.JSONEq(t, strings.ReplaceAll(string(want), "\r\n", "\n"), got,
		"extract output changed — if intended, run: go test ./internal/extract/ -args -update-golden")
}

// TestGoldenExtractThreatModel pins the full JSON contract for a typical call.
func TestGoldenExtractThreatModel(t *testing.T) {
	res := goldenResult(t, Options{
		Queries: []string{"user authentication token"},
		Profile: ProfileThreatModel,
	})
	compareGolden(t, "extract_threat_model.json", res)
}

// TestGoldenExtractTightBudget pins budget packing: code tier, then skeletons.
func TestGoldenExtractTightBudget(t *testing.T) {
	res := goldenResult(t, Options{
		Queries:     []string{"user authentication token"},
		BudgetBytes: 450,
	})
	compareGolden(t, "extract_tight_budget.json", res)
}
