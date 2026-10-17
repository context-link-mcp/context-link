package indexer

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/context-link-mcp/context-link/internal/indexer/adapters"
)

// TestIndexRepoNoParseFailuresSingleWorker guards against parser reuse after a
// cancelled context: a reused parser could carry a stale cancellation flag and
// abort a later file with "operation limit was hit", silently dropping its
// symbols. With one worker every file goes through the same parser.
func TestIndexRepoNoParseFailuresSingleWorker(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := setupTestDB(t)

	registry := NewLanguageRegistry()
	require.NoError(t, registry.Register(adapters.NewPythonAdapter()))
	idx := NewIndexer(registry, db, 1, nil)

	const numFiles = 80
	projectRoot := t.TempDir()
	for i := range numFiles {
		var sb strings.Builder
		for j := range 40 {
			fmt.Fprintf(&sb, "def func_%d_%d(a, b):\n    if a > b:\n        return a * %d\n    return b\n\n", i, j, j)
		}
		writeGoFile(t, projectRoot, fmt.Sprintf("mod%03d.py", i), sb.String())
	}

	stats, err := idx.IndexRepo(ctx, "repo", projectRoot)
	require.NoError(t, err)
	assert.Equal(t, 0, stats.Errors, "no file may fail to parse")
	assert.Equal(t, numFiles, stats.FilesIndexed)
	assert.GreaterOrEqual(t, stats.SymbolsExtracted, numFiles*40)
}
