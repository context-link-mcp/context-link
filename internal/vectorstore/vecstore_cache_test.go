package vectorstore_test

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/context-link-mcp/context-link/internal/vectorstore"
)

// TestVectorCache_Invalidate verifies the cache serves stale rows until it is
// invalidated, and fresh rows after (H2).
func TestVectorCache_Invalidate(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openTestDB(t)
	const dim = 4

	a := insertTestSymbol(t, db, "repo", "a")
	require.NoError(t, vectorstore.UpsertEmbedding(ctx, db, a, "repo", makeVec(dim, 0)))
	cache := vectorstore.NewVectorCache("repo")

	ids := func() []int64 {
		res, err := vectorstore.KNNSearchCached(ctx, db, cache, makeVec(dim, 0), 10, 0.5)
		require.NoError(t, err)
		out := make([]int64, len(res))
		for i, r := range res {
			out[i] = r.SymbolID
		}
		return out
	}
	require.Equal(t, []int64{a}, ids())

	b := insertTestSymbol(t, db, "repo", "b")
	require.NoError(t, vectorstore.UpsertEmbedding(ctx, db, b, "repo", makeVec(dim, 0)))
	assert.Equal(t, []int64{a}, ids(), "cache is not refreshed until invalidated")

	cache.Invalidate()
	assert.ElementsMatch(t, []int64{a, b}, ids())
}

// TestVectorCache_InvalidateDuringSearch runs searches concurrently with
// Invalidate: every search must see the full row set, never a cache emptied
// between load and scan. Run with -race to check the locking.
func TestVectorCache_InvalidateDuringSearch(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openTestDB(t)
	const dim, n = 4, 20
	for i := 0; i < n; i++ {
		id := insertTestSymbol(t, db, "repo", fmt.Sprintf("s%d", i))
		require.NoError(t, vectorstore.UpsertEmbedding(ctx, db, id, "repo", makeVec(dim, 0)))
	}
	cache := vectorstore.NewVectorCache("repo")

	var wg sync.WaitGroup
	errs := make(chan error, 200)
	for g := 0; g < 4; g++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			for i := 0; i < 25; i++ {
				res, err := vectorstore.KNNSearchCached(ctx, db, cache, makeVec(dim, 0), 0, 0.5)
				if err == nil && len(res) != n {
					err = fmt.Errorf("search saw %d rows, want %d", len(res), n)
				}
				if err != nil {
					errs <- err
				}
			}
		}()
		go func() {
			defer wg.Done()
			for i := 0; i < 25; i++ {
				cache.Invalidate()
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		assert.NoError(t, err)
	}
}
