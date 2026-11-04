package vectorstore

import (
	"container/heap"
	"context"
	"database/sql"
	"encoding/binary"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"strconv"
	"strings"
	"sync"

	"github.com/context-link-mcp/context-link/internal/store"
)

// candidateHeap is a min-heap of candidates for top-k selection.
// The smallest similarity is at the top, so we can efficiently evict it
// when a better candidate arrives.
type candidateHeap []candidate

type candidate struct {
	symbolID int64
	sim      float32
}

func (h candidateHeap) Len() int           { return len(h) }
func (h candidateHeap) Less(i, j int) bool { return h[i].sim < h[j].sim } // min-heap
func (h candidateHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *candidateHeap) Push(x any)        { *h = append(*h, x.(candidate)) }
func (h *candidateHeap) Pop() any          { old := *h; n := len(old); x := old[n-1]; *h = old[:n-1]; return x }

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
// Safe for concurrent use. Whoever writes the repo's embeddings must call
// Invalidate afterwards, or searches keep returning the old IDs and vectors.
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

// Invalidate clears the cache, forcing a reload on next search. Searches
// already running finish on the snapshot they started with.
func (vc *VectorCache) Invalidate() {
	vc.mu.Lock()
	defer vc.mu.Unlock()
	vc.loaded = false
	vc.symbolIDs = nil
	vc.vectors = nil
}

// snapshot returns the cached IDs and vectors, loading them from the database
// first if needed. The returned slices are never mutated afterwards
// (Invalidate and loadLocked replace them), so callers may read them without
// holding the lock; a concurrent Invalidate cannot empty a search midway.
func (vc *VectorCache) snapshot(ctx context.Context, db *store.DB) ([]int64, [][]float32, error) {
	vc.mu.RLock()
	if vc.loaded {
		ids, vecs := vc.symbolIDs, vc.vectors
		vc.mu.RUnlock()
		return ids, vecs, nil
	}
	vc.mu.RUnlock()

	vc.mu.Lock()
	defer vc.mu.Unlock()
	if err := vc.loadLocked(ctx, db); err != nil {
		return nil, nil, err
	}
	return vc.symbolIDs, vc.vectors, nil
}

// loadLocked populates the cache from the database if not already loaded.
// The caller must hold vc.mu for writing.
func (vc *VectorCache) loadLocked(ctx context.Context, db *store.DB) error {
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
		vec, ok := decodeRow(symbolID, blob)
		if !ok {
			continue
		}
		ids = append(ids, symbolID)
		vecs = append(vecs, vec)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("vectorstore: cache iterate: %w", err)
	}

	vc.symbolIDs = ids
	vc.vectors = vecs
	vc.loaded = true
	return nil
}

// AllowFunc reports whether a symbol may appear in search results. A nil
// AllowFunc allows every symbol.
type AllowFunc func(symbolID int64) bool

// knnScanner accumulates the top-k candidates for one query. Vectors that
// cannot be compared with the query (a different dimension, left over from
// another embedder, or the zero vector of an input with no known tokens) are
// skipped, never scored 0: with min_similarity=0 a 0 score would still be
// returned as a hit.
type knnScanner struct {
	query   []float32
	topK    int
	minSim  float32
	allow   AllowFunc
	h       candidateHeap
	skipped int // vectors with a different dimension than the query
}

func newKNNScanner(query []float32, topK int, minSim float32, allow AllowFunc) *knnScanner {
	return &knnScanner{query: query, topK: topK, minSim: minSim, allow: allow}
}

func (s *knnScanner) add(id int64, vec []float32) {
	if len(vec) != len(s.query) {
		s.skipped++
		return
	}
	if s.allow != nil && !s.allow(id) {
		return
	}
	if isZeroVector(vec) {
		return
	}
	sim := dotProduct(s.query, vec)
	if sim < s.minSim {
		return
	}
	heap.Push(&s.h, candidate{id, sim})
	if s.topK > 0 && s.h.Len() > s.topK {
		heap.Pop(&s.h)
	}
}

func (s *knnScanner) results(repoName string) []SearchResult {
	if s.skipped > 0 {
		slog.Debug("vectorstore: skipped embeddings with a different dimension than the query — re-index with --force",
			"repo", repoName, "skipped", s.skipped, "query_dim", len(s.query))
	}
	results := topKFromHeap(&s.h)
	for i := range results {
		results[i].RepoName = repoName
	}
	return results
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
	return KNNSearchCachedFiltered(ctx, db, cache, query, topK, minSimilarity, nil)
}

// KNNSearchCachedFiltered is KNNSearchCached restricted to symbols allow
// accepts. Filtering happens before the top-k cut, so a filtered search
// still returns up to topK matches.
func KNNSearchCachedFiltered(
	ctx context.Context,
	db *store.DB,
	cache *VectorCache,
	query []float32,
	topK int,
	minSimilarity float32,
	allow AllowFunc,
) ([]SearchResult, error) {
	if isZeroVector(query) {
		return nil, nil
	}
	ids, vecs, err := cache.snapshot(ctx, db)
	if err != nil {
		return nil, err
	}
	s := newKNNScanner(query, topK, minSimilarity, allow)
	for i, vec := range vecs {
		s.add(ids[i], vec)
	}
	return s.results(cache.repoName), nil
}

// Similarities returns the cosine similarity between query and the stored
// vector of each of ids. Symbols without a comparable vector (none stored,
// a different dimension, or the zero vector) are absent from the map.
// cache may be nil, in which case the vectors are read from the database.
func Similarities(ctx context.Context, db *store.DB, cache *VectorCache, repoName string, query []float32, ids []int64) (map[int64]float32, error) {
	out := make(map[int64]float32, len(ids))
	if len(ids) == 0 || isZeroVector(query) {
		return out, nil
	}
	want := make(map[int64]struct{}, len(ids))
	for _, id := range ids {
		want[id] = struct{}{}
	}
	score := func(id int64, vec []float32) {
		if _, ok := want[id]; !ok || len(vec) != len(query) || isZeroVector(vec) {
			return
		}
		out[id] = dotProduct(query, vec)
	}

	if cache != nil {
		cids, vecs, err := cache.snapshot(ctx, db)
		if err != nil {
			return nil, err
		}
		for i, vec := range vecs {
			score(cids[i], vec)
		}
		return out, nil
	}

	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	args := make([]any, 0, len(ids)+1)
	args = append(args, repoName)
	for _, id := range ids {
		args = append(args, id)
	}
	rows, err := db.QueryContext(ctx,
		`SELECT symbol_id, embedding FROM vec_symbols WHERE repo_name = ? AND symbol_id IN (`+placeholders+`)`,
		args...)
	if err != nil {
		return nil, fmt.Errorf("vectorstore: load embeddings for similarity: %w", err)
	}
	defer rows.Close() //nolint:errcheck
	for rows.Next() {
		var id int64
		var blob []byte
		if err := rows.Scan(&id, &blob); err != nil {
			return nil, fmt.Errorf("vectorstore: scan embedding row: %w", err)
		}
		if vec, ok := decodeRow(id, blob); ok {
			score(id, vec)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("vectorstore: iterate embeddings: %w", err)
	}
	return out, nil
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
	return KNNSearchFiltered(ctx, db, repoName, query, topK, minSimilarity, nil)
}

// KNNSearchFiltered is KNNSearch restricted to symbols allow accepts, applied
// before the top-k cut.
func KNNSearchFiltered(
	ctx context.Context,
	db *store.DB,
	repoName string,
	query []float32,
	topK int,
	minSimilarity float32,
	allow AllowFunc,
) ([]SearchResult, error) {
	if isZeroVector(query) {
		return nil, nil
	}
	rows, err := db.QueryContext(ctx, `
		SELECT symbol_id, embedding FROM vec_symbols WHERE repo_name = ?
	`, repoName)
	if err != nil {
		return nil, fmt.Errorf("vectorstore: query embeddings for repo %q: %w", repoName, err)
	}
	defer rows.Close() //nolint:errcheck

	s := newKNNScanner(query, topK, minSimilarity, allow)
	for rows.Next() {
		var symbolID int64
		var blob []byte
		if err := rows.Scan(&symbolID, &blob); err != nil {
			return nil, fmt.Errorf("vectorstore: scan embedding row: %w", err)
		}
		if vec, ok := decodeRow(symbolID, blob); ok {
			s.add(symbolID, vec)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("vectorstore: iterate embeddings: %w", err)
	}
	return s.results(repoName), nil
}

// EnsureEmbeddingDimension reconciles the recorded embedding dimension with
// the current embedder's and with the vectors actually stored:
//
//   - recorded dimension equals expectedDim: nothing to do.
//   - otherwise, if every stored vector already has expectedDim (none stored
//     yet, or a --force re-index replaced them all), the record is updated to
//     expectedDim and the mismatch clears.
//   - otherwise ErrDimensionMismatch: vectors of another dimension remain
//     and the user must re-index with --force. Searches skip them meanwhile.
//
// A failure to read the record or the vectors (e.g. SQLITE_BUSY) is returned
// as an error and never rewrites the recorded dimension.
func EnsureEmbeddingDimension(ctx context.Context, db *store.DB, expectedDim int) error {
	return ensureEmbeddingDimension(ctx, db, expectedDim)
}

// metaDB is the subset of *store.DB the dimension bookkeeping uses; tests
// substitute a failing reader.
type metaDB interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

func ensureEmbeddingDimension(ctx context.Context, db metaDB, expectedDim int) error {
	storedDim, known, err := storedEmbeddingDimension(ctx, db)
	if err != nil {
		return err
	}
	if known && storedDim == expectedDim {
		return nil
	}

	var other int
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM vec_symbols WHERE length(embedding) != ?`, expectedDim*4,
	).Scan(&other); err != nil {
		return fmt.Errorf("vectorstore: count embeddings by dimension: %w", err)
	}
	if other == 0 {
		return setEmbeddingDimension(ctx, db, expectedDim)
	}

	stored := "unknown"
	if known {
		stored = strconv.Itoa(storedDim)
	}
	return fmt.Errorf("%w: stored=%s, expected=%d, %d embeddings of another dimension — re-index with --force",
		ErrDimensionMismatch, stored, expectedDim, other)
}

// storedEmbeddingDimension reads the recorded embedding dimension. known is
// false when no dimension is recorded or the record is not a number.
func storedEmbeddingDimension(ctx context.Context, db metaDB) (dim int, known bool, err error) {
	var value string
	err = db.QueryRowContext(ctx,
		`SELECT value FROM vec_meta WHERE key = 'embedding_dim'`,
	).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("vectorstore: read embedding dimension: %w", err)
	}
	dim, convErr := strconv.Atoi(value)
	if convErr != nil {
		return 0, false, nil
	}
	return dim, true, nil
}

// SetEmbeddingDimension records the embedding dimension in vec_meta.
func SetEmbeddingDimension(ctx context.Context, db *store.DB, dim int) error {
	return setEmbeddingDimension(ctx, db, dim)
}

func setEmbeddingDimension(ctx context.Context, db metaDB, dim int) error {
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

// ErrCorruptEmbedding is returned for a stored embedding BLOB whose length
// is not a whole number of float32 values.
var ErrCorruptEmbedding = errors.New("vectorstore: corrupt embedding blob")

// decodeFloat32s decodes little-endian IEEE 754 bytes into a float32 slice.
// A length that is not a multiple of 4 is rejected rather than silently
// dropping the trailing bytes.
func decodeFloat32s(buf []byte) ([]float32, error) {
	if len(buf)%4 != 0 {
		return nil, fmt.Errorf("%w: %d bytes is not a multiple of 4", ErrCorruptEmbedding, len(buf))
	}
	vals := make([]float32, len(buf)/4)
	for i := range vals {
		bits := binary.LittleEndian.Uint32(buf[i*4:])
		vals[i] = math.Float32frombits(bits)
	}
	return vals, nil
}

// decodeRow decodes a scanned embedding BLOB, logging and reporting false for
// a corrupt one so that callers skip that row instead of scoring garbage.
func decodeRow(symbolID int64, blob []byte) ([]float32, bool) {
	vec, err := decodeFloat32s(blob)
	if err != nil {
		slog.Warn("vectorstore: skipping corrupt embedding; re-index with --force", "symbol_id", symbolID, "error", err)
		return nil, false
	}
	return vec, true
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

// isZeroVector reports whether every component of v is zero (or v is empty).
// Embedders return the zero vector for inputs with no known tokens.
func isZeroVector(v []float32) bool {
	for _, x := range v {
		if x != 0 {
			return false
		}
	}
	return true
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
