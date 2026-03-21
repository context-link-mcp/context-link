package vectorstore

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"math"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// buildTestModel2Vec creates a tiny Model2Vec model for testing.
// vocab has 6 tokens: [PAD]=0, [UNK]=1, [CLS]=2, [SEP]=3, hello=4, world=5
// dim=4, each token has a known embedding vector.
func buildTestModel2Vec(t *testing.T) (safetensors, tokenizerJSON, configJSON []byte) {
	t.Helper()

	vocabSize := 6
	dim := 4

	// Embedding matrix: each row is distinct
	embData := make([]float32, vocabSize*dim)
	for i := 0; i < vocabSize; i++ {
		for d := 0; d < dim; d++ {
			embData[i*dim+d] = float32(i*dim+d) * 0.1
		}
	}

	// Build safetensors
	embBytes := make([]byte, len(embData)*4)
	for i, v := range embData {
		binary.LittleEndian.PutUint32(embBytes[i*4:], math.Float32bits(v))
	}
	header := map[string]TensorInfo{
		"embeddings": {
			Dtype:       "F32",
			Shape:       []int{vocabSize, dim},
			DataOffsets: [2]int{0, len(embBytes)},
		},
	}
	headerJSON, err := json.Marshal(header)
	require.NoError(t, err)
	sf := make([]byte, 8+len(headerJSON)+len(embBytes))
	binary.LittleEndian.PutUint64(sf[:8], uint64(len(headerJSON)))
	copy(sf[8:], headerJSON)
	copy(sf[8+len(headerJSON):], embBytes)

	// Build tokenizer.json
	tok := map[string]interface{}{
		"model": map[string]interface{}{
			"type":      "WordPiece",
			"unk_token": "[UNK]",
			"vocab": map[string]int64{
				"[PAD]": 0, "[UNK]": 1, "[CLS]": 2, "[SEP]": 3,
				"hello": 4, "world": 5,
			},
		},
	}
	tokJSON, err := json.Marshal(tok)
	require.NoError(t, err)

	// Build config.json
	cfg := Model2VecConfig{
		HiddenDim: dim,
		Normalize: true,
		ApplyZipf: false,
	}
	cfgJSON, err := json.Marshal(cfg)
	require.NoError(t, err)

	return sf, tokJSON, cfgJSON
}

func TestModel2VecEmbedder_Dim(t *testing.T) {
	t.Parallel()
	e := NewModel2VecEmbedder()
	assert.Equal(t, Model2VecDim, e.Dim())
}

func TestModel2VecEmbedder_EmbedOne_Deterministic(t *testing.T) {
	t.Parallel()
	sf, tok, cfg := buildTestModel2Vec(t)
	e := NewModel2VecEmbedderFromBytes(sf, tok, cfg)

	ctx := context.Background()
	v1, err := e.EmbedOne(ctx, "hello world")
	require.NoError(t, err)

	v2, err := e.EmbedOne(ctx, "hello world")
	require.NoError(t, err)

	assert.Equal(t, v1, v2, "same text should produce same embedding")
}

func TestModel2VecEmbedder_EmbedOne_Unique(t *testing.T) {
	t.Parallel()
	sf, tok, cfg := buildTestModel2Vec(t)
	e := NewModel2VecEmbedderFromBytes(sf, tok, cfg)

	ctx := context.Background()
	v1, err := e.EmbedOne(ctx, "hello")
	require.NoError(t, err)

	v2, err := e.EmbedOne(ctx, "world")
	require.NoError(t, err)

	assert.NotEqual(t, v1, v2, "different texts should produce different embeddings")
}

func TestModel2VecEmbedder_UnitNorm(t *testing.T) {
	t.Parallel()
	sf, tok, cfg := buildTestModel2Vec(t)
	e := NewModel2VecEmbedderFromBytes(sf, tok, cfg)

	ctx := context.Background()
	v, err := e.EmbedOne(ctx, "hello world")
	require.NoError(t, err)

	var norm float64
	for _, x := range v {
		norm += float64(x) * float64(x)
	}
	norm = math.Sqrt(norm)
	assert.InDelta(t, 1.0, norm, 0.001, "embedding should be L2-normalized")
}

func TestModel2VecEmbedder_EmbedBatch(t *testing.T) {
	t.Parallel()
	sf, tok, cfg := buildTestModel2Vec(t)
	e := NewModel2VecEmbedderFromBytes(sf, tok, cfg)

	ctx := context.Background()
	texts := []string{"hello", "world", "hello world"}
	batch, err := e.EmbedBatch(ctx, texts)
	require.NoError(t, err)
	require.Len(t, batch, 3)

	// Each batch result should match individual calls.
	for i, text := range texts {
		individual, err := e.EmbedOne(ctx, text)
		require.NoError(t, err)
		assert.Equal(t, individual, batch[i], "batch[%d] should match individual for %q", i, text)
	}
}

func TestModel2VecEmbedder_EmptyText(t *testing.T) {
	t.Parallel()
	sf, tok, cfg := buildTestModel2Vec(t)
	e := NewModel2VecEmbedderFromBytes(sf, tok, cfg)

	ctx := context.Background()
	v, err := e.EmbedOne(ctx, "")
	require.NoError(t, err)
	require.NotNil(t, v)
	// Empty text produces zero tokens after CLS/SEP skip, so all zeros
	// which l2Normalize leaves as zeros.
	assert.Len(t, v, 4)
}

func TestModel2VecEmbedder_Close(t *testing.T) {
	t.Parallel()
	e := NewModel2VecEmbedder()
	assert.NoError(t, e.Close())
}

func TestModel2VecEmbedder_ConcurrentSafety(t *testing.T) {
	t.Parallel()
	sf, tok, cfg := buildTestModel2Vec(t)
	e := NewModel2VecEmbedderFromBytes(sf, tok, cfg)

	ctx := context.Background()
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := e.EmbedOne(ctx, "hello world")
			assert.NoError(t, err)
		}()
	}
	wg.Wait()
}

func TestModel2VecEmbedder_BuiltinModel(t *testing.T) {
	t.Parallel()
	// Test with the actual embedded model.
	e := NewModel2VecEmbedder()

	ctx := context.Background()
	v, err := e.EmbedOne(ctx, "function validateToken")
	require.NoError(t, err)
	assert.Len(t, v, Model2VecDim, "should produce %d-dim embedding", Model2VecDim)

	var norm float64
	for _, x := range v {
		norm += float64(x) * float64(x)
	}
	norm = math.Sqrt(norm)
	assert.InDelta(t, 1.0, norm, 0.001)
}
