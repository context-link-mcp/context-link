package vectorstore

import (
	"container/heap"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"strconv"
	"sync"

	"github.com/context-link/context-link/internal/store"
)

// candidateHeap is a min-heap of candidates for top-k selection.
// The smallest similarity is at the top, so we can efficiently evict it
// when a better candidate arrives.
type candidateHeap []candidate

type candidate struct {
	symbolID int64
	sim      float32
}

func (h candidateHeap) Len() int            { return len(h) }
func (h candidateHeap) Less(i, j int) bool  { return h[i].sim < h[j].sim } // min-heap
func (h candidateHeap) Swap(i, j int)       { h[i], h[j] = h[j], h[i] }
func (h *candidateHeap) Push(x any)         { *h = append(*h, x.(candidate)) }
func (h *candidateHeap) Pop() any           { old := *h; n := len(old); x := old[n-1]; *h = old[:n-1]; return x }

// topKFromHeap extracts sorted results (descending similarity) from a min-heap.
func topKFromHeap(h *candidateHeap) []SearchResult {
	results := make([]SearchResult, h.Len())
	for i := len(results) - 1; i >= 0; i-- {
		c := heap.Pop(h).(candidate)
		results[i] = SearchResult{SymbolID: c.symbolID, Similarity: c.sim}
	}
	return results
}

// VectorCache holds pre-loaded embeddings in memory for fast KNN search.
// Thread-safe for concurrent reads; invalidated on writes.
type VectorCache struct {
	mu        sync.RWMutex
	symbolIDs []int64     // parallel arrays
	vectors   [][]float32 // each is dim-length
	repoName  string
	loaded    bool
}

// NewVectorCache creates an empty cache for the given repo.
func NewVectorCache(repoName string) *VectorCache {
	return &VectorCache{repoName: repoName}
}

// Invalidate clears the cache, forcing a reload on next search.
func (vc *VectorCache) Invalidate() {
	vc.mu.Lock()
	defer vc.mu.Unlock()
	vc.loaded = false
	vc.symbolIDs = nil
	vc.vectors = nil
}

// load populates the cache from the database if not already loaded.
func (vc *VectorCache) load(ctx context.Context, db *store.DB) error {
	vc.mu.Lock()
	defer vc.mu.Unlock()

	if vc.loaded {
		return nil
	}

	rows, err := db.QueryContext(ctx,
		`SELECT symbol_id, embedding FROM vec_symbols WHERE repo_name = ?`,
		vc.repoName,
	)
	if err != nil {
		return fmt.Errorf("vectorstore: cache load for repo %q: %w", vc.repoName, err)
	}
	defer rows.Close() //nolint:errcheck

	var ids []int64
	var vecs [][]float32
	for rows.Next() {
		var symbolID int64
		var blob []byte
		if err := rows.Scan(&symbolID, &blob); err != nil {
			return fmt.Errorf("vectorstore: cache scan row: %w", err)
		}
		ids = append(ids, symbolID)
		vecs = append(vecs, decodeFloat32s(blob))
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("vectorstore: cache iterate: %w", err)
	}

	vc.symbolIDs = ids
	vc.vectors = vecs
	vc.loaded = true
	return nil
}

// KNNSearchCached performs KNN search using the in-memory cache.
func KNNSearchCached(
	ctx context.Context,
	db *store.DB,
	cache *VectorCache,
	query []float32,
	topK int,
	minSimilarity float32,
) ([]SearchResult, error) {
	if err := cache.load(ctx, db); err != nil {
		return nil, err
	}

	cache.mu.RLock()
	defer cache.mu.RUnlock()

	h := &candidateHeap{}
	heap.Init(h)

	for i, vec := range cache.vectors {
		sim := dotProduct(query, vec)
		if sim >= minSimilarity {
			heap.Push(h, candidate{cache.symbolIDs[i], sim})
			if topK > 0 && h.Len() > topK {
				heap.Pop(h)
			}
		}
	}

	results := topKFromHeap(h)
	for i := range results {
		results[i].RepoName = cache.repoName
	}
	return results, nil
}

// ErrDimensionMismatch is returned when stored embeddings have a different
// dimension than the current embedder. The user must re-index with --force.
var ErrDimensionMismatch = errors.New("vectorstore: embedding dimension mismatch")

// SearchResult is a symbol returned by KNNSearch with its similarity score.
type SearchResult struct {
	SymbolID   int64
	RepoName   string
	Similarity float32
}

// UpsertEmbedding stores or replaces the embedding for a symbol.
// The vector must be L2-normalized (unit length).
func UpsertEmbedding(ctx context.Context, db *store.DB, symbolID int64, repoName string, vec []float32) error {
	blob := encodeFloat32s(vec)
	_, err := db.ExecContext(ctx, `
		INSERT INTO vec_symbols (symbol_id, repo_name, embedding)
		VALUES (?, ?, ?)
		ON CONFLICT(symbol_id) DO UPDATE SET
			repo_name = excluded.repo_name,
			embedding = excluded.embedding
	`, symbolID, repoName, blob)
	if err != nil {
		return fmt.Errorf("vectorstore: upsert embedding for symbol %d: %w", symbolID, err)
	}
	return nil
}

// BatchUpsertEmbeddings stores or replaces embeddings for multiple symbols in a
// single transaction. Each entry pairs a symbol ID with its L2-normalized vector.
func BatchUpsertEmbeddings(ctx context.Context, db *store.DB, repoName string, symbolIDs []int64, vecs [][]float32) error {
	if len(symbolIDs) != len(vecs) {
		return fmt.Errorf("vectorstore: symbolIDs length %d != vecs length %d", len(symbolIDs), len(vecs))
	}
	if len(symbolIDs) == 0 {
		return nil
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("vectorstore: begin batch upsert tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO vec_symbols (symbol_id, repo_name, embedding)
		VALUES (?, ?, ?)
		ON CONFLICT(symbol_id) DO UPDATE SET
			repo_name = excluded.repo_name,
			embedding = excluded.embedding
	`)
	if err != nil {
		return fmt.Errorf("vectorstore: prepare batch upsert: %w", err)
	}
	defer stmt.Close()

	for i, id := range symbolIDs {
		blob := encodeFloat32s(vecs[i])
		if _, err := stmt.ExecContext(ctx, id, repoName, blob); err != nil {
			return fmt.Errorf("vectorstore: batch upsert embedding for symbol %d: %w", id, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("vectorstore: commit batch upsert: %w", err)
	}
	return nil
}

// DeleteEmbeddingsByRepo removes all embeddings for a repository.
// Used during full re-index to clear stale vectors.
func DeleteEmbeddingsByRepo(ctx context.Context, db *store.DB, repoName string) error {
	_, err := db.ExecContext(ctx, `DELETE FROM vec_symbols WHERE repo_name = ?`, repoName)
	if err != nil {
		return fmt.Errorf("vectorstore: delete embeddings for repo %q: %w", repoName, err)
	}
	return nil
}

// KNNSearch performs Go-side K-nearest-neighbor search over stored embeddings.
//
// It loads all embeddings for the repo into memory, computes dot products with
// the query vector (equivalent to cosine similarity for L2-normalized vectors),
// and returns the top-k results above minSimilarity.
func KNNSearch(
	ctx context.Context,
	db *store.DB,
	repoName string,
	query []float32,
	topK int,
	minSimilarity float32,
) ([]SearchResult, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT symbol_id, embedding FROM vec_symbols WHERE repo_name = ?
	`, repoName)
	if err != nil {
		return nil, fmt.Errorf("vectorstore: query embeddings for repo %q: %w", repoName, err)
	}
	defer rows.Close() //nolint:errcheck

	h := &candidateHeap{}
	heap.Init(h)

	for rows.Next() {
		var symbolID int64
		var blob []byte
		if err := rows.Scan(&symbolID, &blob); err != nil {
			return nil, fmt.Errorf("vectorstore: scan embedding row: %w", err)
		}
		vec := decodeFloat32s(blob)
		sim := dotProduct(query, vec)
		if sim >= minSimilarity {
			heap.Push(h, candidate{symbolID, sim})
			if topK > 0 && h.Len() > topK {
				heap.Pop(h)
			}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("vectorstore: iterate embeddings: %w", err)
	}

	results := topKFromHeap(h)
	for i := range results {
		results[i].RepoName = repoName
	}
	return results, nil
}

// EnsureEmbeddingDimension checks that the stored embedding dimension matches
// the expected dimension. If no dimension is recorded yet, it sets it.
// Returns ErrDimensionMismatch if there is a mismatch (user must re-index).
func EnsureEmbeddingDimension(ctx context.Context, db *store.DB, expectedDim int) error {
	// Read current stored dimension.
	var value string
	err := db.QueryRowContext(ctx,
		`SELECT value FROM vec_meta WHERE key = 'embedding_dim'`,
	).Scan(&value)

	if err != nil {
		// Table might not exist yet (pre-migration) or no row — set it.
		return SetEmbeddingDimension(ctx, db, expectedDim)
	}

	storedDim, convErr := strconv.Atoi(value)
	if convErr != nil {
		// Corrupt value — overwrite.
		return SetEmbeddingDimension(ctx, db, expectedDim)
	}

	if storedDim != expectedDim {
		return fmt.Errorf("%w: stored=%d, expected=%d — re-index with --force",
			ErrDimensionMismatch, storedDim, expectedDim)
	}
	return nil
}

// SetEmbeddingDimension records the embedding dimension in vec_meta.
func SetEmbeddingDimension(ctx context.Context, db *store.DB, dim int) error {
	_, err := db.ExecContext(ctx, `
		INSERT INTO vec_meta (key, value) VALUES ('embedding_dim', ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value
	`, strconv.Itoa(dim))
	if err != nil {
		return fmt.Errorf("vectorstore: set embedding dimension: %w", err)
	}
	return nil
}

// encodeFloat32s encodes a float32 slice as little-endian IEEE 754 bytes.
func encodeFloat32s(vals []float32) []byte {
	buf := make([]byte, len(vals)*4)
	for i, v := range vals {
		binary.LittleEndian.PutUint32(buf[i*4:], math.Float32bits(v))
	}
	return buf
}

// decodeFloat32s decodes little-endian IEEE 754 bytes into a float32 slice.
func decodeFloat32s(buf []byte) []float32 {
	vals := make([]float32, len(buf)/4)
	for i := range vals {
		bits := binary.LittleEndian.Uint32(buf[i*4:])
		vals[i] = math.Float32frombits(bits)
	}
	return vals
}

// dotProduct computes the dot product of two equal-length vectors.
// For L2-normalized vectors, dot product == cosine similarity.
func dotProduct(a, b []float32) float32 {
	if len(a) != len(b) {
		return 0
	}
	var sum float32
	for i := range a {
		sum += a[i] * b[i]
	}
	return sum
}

// l2Normalize normalizes v to unit length in-place.
// No-op if the norm is near zero.
func l2Normalize(v []float32) {
	var norm float64
	for _, x := range v {
		norm += float64(x) * float64(x)
	}
	norm = math.Sqrt(norm)
	if norm < 1e-12 {
		return
	}
	f := float32(1.0 / norm)
	for i := range v {
		v[i] *= f
	}
}
