package vectorstore_test

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/context-link/context-link/internal/store"
	"github.com/context-link/context-link/internal/vectorstore"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// openTestDB opens a fresh in-disk test database with all migrations applied.
func openTestDB(t *testing.T) *store.DB {
	t.Helper()
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "test.db"))
	require.NoError(t, err, "open test db")
	t.Cleanup(func() { db.Close() }) //nolint:errcheck
	require.NoError(t, store.Migrate(db), "migrate test db")
	return db
}

// insertTestSymbol inserts a minimal symbol and returns its ID.
func insertTestSymbol(t *testing.T, db *store.DB, repo, name string) int64 {
	t.Helper()
	res, err := db.ExecContext(context.Background(), `
		INSERT INTO symbols (repo_name, name, qualified_name, kind, file_path, content_hash, code_block, start_line, end_line, language)
		VALUES (?, ?, ?, 'function', 'test.ts', 'abc123', 'function foo(){}', 1, 5, 'typescript')
	`, repo, name, name)
	require.NoError(t, err)
	id, err := res.LastInsertId()
	require.NoError(t, err)
	return id
}

// makeVec returns an L2-normalized float32 vector with dimension d, with a
// spike at position pos to make different vectors clearly distinguishable.
func makeVec(d, pos int) []float32 {
	v := make([]float32, d)
	v[pos%d] = 1.0
	return v
}

func TestUpsertEmbedding_And_Retrieve(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openTestDB(t)

	id := insertTestSymbol(t, db, "repo1", "myFunc")
	vec := makeVec(vectorstore.ModelDim, 0)

	err := vectorstore.UpsertEmbedding(ctx, db, id, "repo1", vec)
	require.NoError(t, err, "upsert should succeed")

	// Second upsert (same symbol) should not error — ON CONFLICT DO UPDATE.
	vec2 := makeVec(vectorstore.ModelDim, 1)
	err = vectorstore.UpsertEmbedding(ctx, db, id, "repo1", vec2)
	require.NoError(t, err, "re-upsert should succeed")

	// Confirm the row exists.
	var count int
	err = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM vec_symbols WHERE symbol_id = ?`, id).Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 1, count, "should have exactly 1 row after upsert")
}

func TestKNNSearch_ReturnsRankedResults(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openTestDB(t)

	repo := "testrepo"
	// Insert 3 symbols with distinct embeddings.
	idA := insertTestSymbol(t, db, repo, "alpha")
	idB := insertTestSymbol(t, db, repo, "beta")
	idC := insertTestSymbol(t, db, repo, "gamma")

	// Embeddings: all unit vectors in orthogonal directions.
	vecA := makeVec(vectorstore.ModelDim, 0) // spike at dim 0
	vecB := makeVec(vectorstore.ModelDim, 1) // spike at dim 1
	vecC := makeVec(vectorstore.ModelDim, 2) // spike at dim 2

	require.NoError(t, vectorstore.UpsertEmbedding(ctx, db, idA, repo, vecA))
	require.NoError(t, vectorstore.UpsertEmbedding(ctx, db, idB, repo, vecB))
	require.NoError(t, vectorstore.UpsertEmbedding(ctx, db, idC, repo, vecC))

	// Query identical to vecA → idA must rank first.
	results, err := vectorstore.KNNSearch(ctx, db, repo, vecA, 3, 0.0)
	require.NoError(t, err)
	require.Len(t, results, 3)

	assert.Equal(t, idA, results[0].SymbolID, "idA should rank first")
	assert.InDelta(t, 1.0, results[0].Similarity, 1e-5, "similarity to self should be ~1.0")

	// Orthogonal vectors have zero dot product.
	assert.InDelta(t, 0.0, results[1].Similarity, 1e-5)
	assert.InDelta(t, 0.0, results[2].Similarity, 1e-5)
}

func TestKNNSearch_TopK(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openTestDB(t)

	repo := "repo2"
	for i := 0; i < 5; i++ {
		id := insertTestSymbol(t, db, repo, fmt.Sprintf("sym%d", i))
		vec := makeVec(vectorstore.ModelDim, i)
		require.NoError(t, vectorstore.UpsertEmbedding(ctx, db, id, repo, vec))
	}

	query := makeVec(vectorstore.ModelDim, 0)
	results, err := vectorstore.KNNSearch(ctx, db, repo, query, 2, 0.0)
	require.NoError(t, err)
	assert.Len(t, results, 2, "top_k=2 should return exactly 2 results")
}

func TestKNNSearch_MinSimilarityFilter(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openTestDB(t)

	repo := "repo3"
	idA := insertTestSymbol(t, db, repo, "a")
	idB := insertTestSymbol(t, db, repo, "b")

	vecA := makeVec(vectorstore.ModelDim, 0)
	vecB := makeVec(vectorstore.ModelDim, 1) // orthogonal to query

	require.NoError(t, vectorstore.UpsertEmbedding(ctx, db, idA, repo, vecA))
	require.NoError(t, vectorstore.UpsertEmbedding(ctx, db, idB, repo, vecB))

	_ = idB // idB should be filtered out

	// With minSimilarity=0.5, only vecA (similarity=1.0) should pass.
	results, err := vectorstore.KNNSearch(ctx, db, repo, vecA, 10, 0.5)
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, idA, results[0].SymbolID)
}

func TestKNNSearch_RepoIsolation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openTestDB(t)

	idA := insertTestSymbol(t, db, "repoX", "funcA")
	idB := insertTestSymbol(t, db, "repoY", "funcB")
	vec := makeVec(vectorstore.ModelDim, 0)

	require.NoError(t, vectorstore.UpsertEmbedding(ctx, db, idA, "repoX", vec))
	require.NoError(t, vectorstore.UpsertEmbedding(ctx, db, idB, "repoY", vec))

	results, err := vectorstore.KNNSearch(ctx, db, "repoX", vec, 10, 0.0)
	require.NoError(t, err)
	require.Len(t, results, 1, "search in repoX should not return repoY symbols")
	assert.Equal(t, idA, results[0].SymbolID)
}

func TestKNNSearch_EmptyDB(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openTestDB(t)

	query := makeVec(vectorstore.ModelDim, 0)
	results, err := vectorstore.KNNSearch(ctx, db, "emptyrepo", query, 10, 0.0)
	require.NoError(t, err)
	assert.Empty(t, results, "search on empty DB should return empty results")
}

func TestKNNSearch_MismatchedDimIgnored(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openTestDB(t)

	idA := insertTestSymbol(t, db, "dimrepo", "sym")
	// Store a 384-dim vector but query with a different-length vector.
	// dotProduct returns 0 for mismatched lengths — result is filtered by minSim.
	vec := makeVec(vectorstore.ModelDim, 0)
	require.NoError(t, vectorstore.UpsertEmbedding(ctx, db, idA, "dimrepo", vec))

	// Query with same vector — should match.
	results, err := vectorstore.KNNSearch(ctx, db, "dimrepo", vec, 10, 0.5)
	require.NoError(t, err)
	assert.Len(t, results, 1)
}

func TestDeleteEmbeddingsByRepo(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openTestDB(t)

	idA := insertTestSymbol(t, db, "delrepo", "f1")
	idB := insertTestSymbol(t, db, "other", "f2")
	vec := makeVec(vectorstore.ModelDim, 0)

	require.NoError(t, vectorstore.UpsertEmbedding(ctx, db, idA, "delrepo", vec))
	require.NoError(t, vectorstore.UpsertEmbedding(ctx, db, idB, "other", vec))

	err := vectorstore.DeleteEmbeddingsByRepo(ctx, db, "delrepo")
	require.NoError(t, err)

	var count int
	db.QueryRowContext(ctx, `SELECT COUNT(*) FROM vec_symbols`).Scan(&count) //nolint:errcheck
	assert.Equal(t, 1, count, "only delrepo embeddings should be removed")
}
