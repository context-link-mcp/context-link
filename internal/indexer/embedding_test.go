package indexer

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/context-link-mcp/context-link/internal/indexer/adapters"
	"github.com/context-link-mcp/context-link/internal/store"
	"github.com/context-link-mcp/context-link/internal/vectorstore"
	"github.com/context-link-mcp/context-link/pkg/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGenerateEmbeddings_WithMockEmbedder verifies that generateEmbeddings:
//   - fetches symbols from the DB for the provided file paths,
//   - calls the embedder with SymbolEmbedText-formatted inputs,
//   - upserts the resulting vectors into vec_symbols,
//   - returns the correct count of embeddings generated.
func TestGenerateEmbeddings_WithMockEmbedder(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	db := setupTestDB(t)
	projectRoot := findProjectRoot(t)
	mockEmbedder := vectorstore.NewMockEmbedder(0)

	// Parse and store symbols from the Go fixture.
	goAdapter := adapters.NewGoAdapter()
	fixturePath := filepath.Join(projectRoot, "testdata", "langs", "go", "sample.go")
	source, err := os.ReadFile(fixturePath)
	require.NoError(t, err)

	poolMgr := NewParserPoolManager()
	tree, err := poolMgr.GetPool(goAdapter).Parse(ctx, source)
	require.NoError(t, err)

	extractor := NewExtractor()
	symbols, err := extractor.ExtractSymbols(ctx, tree, source, goAdapter, "embrepo", "sample.go")
	require.NoError(t, err)
	require.NotEmpty(t, symbols, "fixture must contain symbols")

	require.NoError(t, store.BatchInsertSymbols(ctx, db, symbols))

	// Build indexer with mock embedder and run generateEmbeddings.
	registry := NewLanguageRegistry()
	require.NoError(t, registry.Register(goAdapter))

	idx := NewIndexer(registry, db, 1, mockEmbedder)

	// Build symbol map (simulates what IndexRepo does after Phase 4).
	symsByFile, err := store.GetSymbolsByRepo(ctx, db, "embrepo")
	require.NoError(t, err)

	count := idx.generateEmbeddings(ctx, "embrepo", []string{"sample.go"}, symsByFile)
	assert.Equal(t, len(symbols), count, "should generate one embedding per symbol")

	// Verify embeddings were persisted in vec_symbols.
	var stored int
	err = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM vec_symbols WHERE repo_name = ?`, "embrepo").Scan(&stored)
	require.NoError(t, err)
	assert.Equal(t, len(symbols), stored, "all embeddings should be stored in vec_symbols")
}

// TestGenerateEmbeddings_UnknownFile verifies generateEmbeddings gracefully skips
// a file path that has no symbols in the DB (returns 0, no panic).
func TestGenerateEmbeddings_UnknownFile(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	db := setupTestDB(t)
	mockEmbedder := vectorstore.NewMockEmbedder(0)

	registry := NewLanguageRegistry()
	idx := NewIndexer(registry, db, 1, mockEmbedder)

	emptySymMap := make(map[string][]models.Symbol)
	count := idx.generateEmbeddings(ctx, "repo", []string{"no_such_file.go"}, emptySymMap)
	assert.Equal(t, 0, count, "no embeddings should be generated for an unknown file path")
}

// TestGenerateEmbeddings_NilEmbedder verifies that IndexRepo does not call
// generateEmbeddings when no embedder is configured (nil embedder field).
func TestGenerateEmbeddings_NilEmbedder(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	db := setupTestDB(t)
	registry := NewLanguageRegistry()
	idx := NewIndexer(registry, db, 1, nil) // nil embedder

	// Must not panic.
	stats, err := idx.IndexRepo(ctx, "nilrepo", t.TempDir())
	require.NoError(t, err)
	assert.Equal(t, 0, stats.EmbeddingsGenerated, "nil embedder should produce 0 embeddings")
}
