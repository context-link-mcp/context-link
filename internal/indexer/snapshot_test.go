package indexer

import (
	"context"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"testing"

	"github.com/context-link-mcp/context-link/internal/indexer/adapters"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var updateGolden = flag.Bool("update-golden", false, "update golden snapshot files")

// snapshotSymbol is a simplified symbol representation for golden file comparison.
// Excludes volatile fields (ID, IndexedAt, ContentHash).
type snapshotSymbol struct {
	Name          string `json:"name"`
	QualifiedName string `json:"qualified_name"`
	Kind          string `json:"kind"`
	Language      string `json:"language"`
	StartLine     int    `json:"start_line"`
	EndLine       int    `json:"end_line"`
}

func TestSnapshotGoSymbols(t *testing.T) {
	t.Parallel()
	runSnapshotTest(t, adapters.NewGoAdapter(), "go", "sample.go")
}

func TestSnapshotTypeScriptSymbols(t *testing.T) {
	t.Parallel()
	runSnapshotTest(t, adapters.NewTypeScriptAdapter(), "ts", "auth.ts")
}

func runSnapshotTest(t *testing.T, adapter LanguageAdapter, langDir, fixture string) {
	t.Helper()

	// Locate testdata relative to project root.
	projectRoot := findProjectRoot(t)
	fixturePath := filepath.Join(projectRoot, "testdata", "langs", langDir, fixture)

	source, err := os.ReadFile(fixturePath)
	require.NoError(t, err, "failed to read fixture %s", fixturePath)

	// Parse with the adapter's grammar.
	poolMgr := NewParserPoolManager()
	pool := poolMgr.GetPool(adapter)
	tree, err := pool.Parse(context.Background(), source)
	require.NoError(t, err, "failed to parse fixture %s", fixture)

	// Extract symbols.
	extractor := NewExtractor()
	symbols, err := extractor.ExtractSymbols(
		context.Background(), tree, source, adapter,
		"test-repo", fixture,
	)
	require.NoError(t, err, "failed to extract symbols from %s", fixture)

	// Convert to snapshot format (excluding volatile fields).
	var snapshots []snapshotSymbol
	for _, s := range symbols {
		snapshots = append(snapshots, snapshotSymbol{
			Name:          s.Name,
			QualifiedName: s.QualifiedName,
			Kind:          s.Kind,
			Language:      s.Language,
			StartLine:     s.StartLine,
			EndLine:       s.EndLine,
		})
	}

	// Sort by start_line for stable comparison.
	sort.Slice(snapshots, func(i, j int) bool {
		return snapshots[i].StartLine < snapshots[j].StartLine
	})

	goldenPath := filepath.Join(projectRoot, "testdata", "golden", langDir+"_"+fixture+".json")

	if *updateGolden {
		// Write golden file.
		data, err := json.MarshalIndent(snapshots, "", "  ")
		require.NoError(t, err)
		require.NoError(t, os.MkdirAll(filepath.Dir(goldenPath), 0755))
		require.NoError(t, os.WriteFile(goldenPath, data, 0644))
		t.Logf("Updated golden file: %s", goldenPath)
		return
	}

	// Read and compare against golden file.
	goldenData, err := os.ReadFile(goldenPath)
	require.NoError(t, err, "golden file not found at %s — run with -update-golden to create", goldenPath)

	var expected []snapshotSymbol
	require.NoError(t, json.Unmarshal(goldenData, &expected))

	assert.Equal(t, expected, snapshots, "snapshot mismatch for %s — run with -update-golden to update", fixture)
}

// findProjectRoot walks up from the test file to find the project root (where go.mod lives).
func findProjectRoot(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	require.True(t, ok)

	dir := filepath.Dir(filename)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not find project root (go.mod)")
		}
		dir = parent
	}
}
