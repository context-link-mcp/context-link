package vectorstore

import (
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"sort"

	"github.com/context-link/context-link/internal/store"
)

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

	type candidate struct {
		symbolID int64
		sim      float32
	}

	var candidates []candidate
	for rows.Next() {
		var symbolID int64
		var blob []byte
		if err := rows.Scan(&symbolID, &blob); err != nil {
			return nil, fmt.Errorf("vectorstore: scan embedding row: %w", err)
		}
		vec := decodeFloat32s(blob)
		sim := dotProduct(query, vec)
		if sim >= minSimilarity {
			candidates = append(candidates, candidate{symbolID, sim})
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("vectorstore: iterate embeddings: %w", err)
	}

	// Sort by similarity descending.
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].sim > candidates[j].sim
	})

	if topK > 0 && len(candidates) > topK {
		candidates = candidates[:topK]
	}

	results := make([]SearchResult, len(candidates))
	for i, c := range candidates {
		results[i] = SearchResult{
			SymbolID:   c.symbolID,
			RepoName:   repoName,
			Similarity: c.sim,
		}
	}
	return results, nil
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
