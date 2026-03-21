// Package vectorstore provides vector embedding generation and similarity
// search for the context-link semantic search pipeline (Phase 3).
package vectorstore

import (
	"context"
	"hash/fnv"
	"math"
	"strings"
)

const (
	// ONNXModelDim is the output dimension of all-MiniLM-L6-v2 (ONNX backend).
	ONNXModelDim = 384
	// ModelDim is the default embedding dimension (Model2Vec built-in backend).
	ModelDim = Model2VecDim
	// MaxSeqLen is the maximum token sequence length fed to the model.
	MaxSeqLen = 128
	// DefaultBatchSize is the number of texts to embed per ONNX inference call.
	DefaultBatchSize = 32
)

// Embedder generates fixed-size float32 embeddings from text.
type Embedder interface {
	// EmbedBatch generates embeddings for a batch of texts.
	EmbedBatch(ctx context.Context, texts []string) ([][]float32, error)
	// EmbedOne generates an embedding for a single text.
	EmbedOne(ctx context.Context, text string) ([]float32, error)
	// Dim returns the embedding vector dimension.
	Dim() int
	// Close releases any resources held by the embedder.
	Close() error
}

// SymbolEmbedText builds the embedding input string for a symbol.
// Format: "{kind} {qualified_name}: {first_line_of_code_block}"
func SymbolEmbedText(kind, qualifiedName, codeBlock string) string {
	firstLine := codeBlock
	if idx := strings.IndexByte(codeBlock, '\n'); idx >= 0 {
		firstLine = codeBlock[:idx]
	}
	return kind + " " + qualifiedName + ": " + strings.TrimSpace(firstLine)
}

// MockEmbedder returns deterministic, L2-normalized embeddings for testing.
// Two different texts always produce different (but stable) embeddings.
type MockEmbedder struct {
	dim int
}

// NewMockEmbedder creates a MockEmbedder with the given vector dimension.
// Pass 0 to use ModelDim (128).
func NewMockEmbedder(dim int) *MockEmbedder {
	if dim <= 0 {
		dim = ModelDim
	}
	return &MockEmbedder{dim: dim}
}

// Dim implements Embedder.
func (m *MockEmbedder) Dim() int { return m.dim }

// Close implements Embedder (no-op for mock).
func (m *MockEmbedder) Close() error { return nil }

// EmbedOne implements Embedder.
func (m *MockEmbedder) EmbedOne(_ context.Context, text string) ([]float32, error) {
	return mockEmbed(text, m.dim), nil
}

// EmbedBatch implements Embedder.
func (m *MockEmbedder) EmbedBatch(_ context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i, t := range texts {
		out[i] = mockEmbed(t, m.dim)
	}
	return out, nil
}

// mockEmbed generates a deterministic, L2-normalized embedding from a string
// using FNV-64a hashing with a linear congruential generator for spreading.
func mockEmbed(text string, dim int) []float32 {
	h := fnv.New64a()
	h.Write([]byte(text)) //nolint:errcheck
	seed := h.Sum64()

	v := make([]float32, dim)
	for i := range v {
		// LCG step to spread hash across dimensions.
		seed = seed*6364136223846793005 + 1442695040888963407
		v[i] = float32(int64(seed)>>32) / float32(math.MaxInt32)
	}
	l2Normalize(v)
	return v
}
