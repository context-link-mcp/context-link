package vectorstore

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
)

// Model2VecDim is the output dimension of the built-in potion-base-4M model.
const Model2VecDim = 128

// Model2VecConfig holds parsed config.json for a Model2Vec model.
type Model2VecConfig struct {
	HiddenDim  int  `json:"hidden_dim"`
	Normalize  bool `json:"normalize"`
	ApplyZipf  bool `json:"apply_zipf"`
	SeqLength  int  `json:"seq_length"`
}

// Model2VecEmbedder generates embeddings using static Model2Vec word embeddings.
// Inference: tokenize → lookup embedding rows → mean pool → L2 normalize.
// No neural network forward pass required. Thread-safe without locking after init.
type Model2VecEmbedder struct {
	once sync.Once
	err  error

	// Raw data for lazy init.
	rawSafetensors  []byte
	rawTokenizerJSON []byte
	rawConfigJSON   []byte

	// Initialized on first use.
	tokenizer  *BERTTokenizer
	embeddings []float32 // flattened [vocab_size * hidden_dim]
	vocabSize  int
	config     Model2VecConfig
}

// NewModel2VecEmbedder creates a Model2VecEmbedder using the embedded model files.
// Initialization is lazy — the model is loaded on first EmbedOne/EmbedBatch call.
func NewModel2VecEmbedder() *Model2VecEmbedder {
	return &Model2VecEmbedder{
		rawSafetensors:   model2vecSafetensors,
		rawTokenizerJSON: model2vecTokenizerJSON,
		rawConfigJSON:    model2vecConfigJSON,
	}
}

// NewModel2VecEmbedderFromBytes creates a Model2VecEmbedder from explicit byte slices.
// Useful for testing with custom model data.
func NewModel2VecEmbedderFromBytes(safetensors, tokenizerJSON, configJSON []byte) *Model2VecEmbedder {
	return &Model2VecEmbedder{
		rawSafetensors:   safetensors,
		rawTokenizerJSON: tokenizerJSON,
		rawConfigJSON:    configJSON,
	}
}

// Dim implements Embedder.
func (e *Model2VecEmbedder) Dim() int {
	return Model2VecDim
}

// Close implements Embedder (no-op — memory is GC'd).
func (e *Model2VecEmbedder) Close() error {
	return nil
}

// EmbedOne implements Embedder.
func (e *Model2VecEmbedder) EmbedOne(_ context.Context, text string) ([]float32, error) {
	if err := e.init(); err != nil {
		return nil, err
	}
	return e.embedOne(text), nil
}

// EmbedBatch implements Embedder.
func (e *Model2VecEmbedder) EmbedBatch(_ context.Context, texts []string) ([][]float32, error) {
	if err := e.init(); err != nil {
		return nil, err
	}
	out := make([][]float32, len(texts))
	for i, t := range texts {
		out[i] = e.embedOne(t)
	}
	return out, nil
}

// init loads the model lazily on first use.
func (e *Model2VecEmbedder) init() error {
	e.once.Do(func() {
		e.err = e.initialize()
	})
	return e.err
}

func (e *Model2VecEmbedder) initialize() error {
	// Parse config.
	if err := json.Unmarshal(e.rawConfigJSON, &e.config); err != nil {
		return fmt.Errorf("model2vec: failed to parse config.json: %w", err)
	}

	// Parse tokenizer.
	tok, err := NewBERTTokenizerFromHFJSON(e.rawTokenizerJSON)
	if err != nil {
		return fmt.Errorf("model2vec: failed to parse tokenizer: %w", err)
	}
	e.tokenizer = tok

	// Parse safetensors.
	sf, err := ParseSafetensors(e.rawSafetensors)
	if err != nil {
		return fmt.Errorf("model2vec: failed to parse safetensors: %w", err)
	}

	embeddings, shape, err := sf.GetFloat32Tensor("embeddings")
	if err != nil {
		return fmt.Errorf("model2vec: failed to load embeddings tensor: %w", err)
	}

	if len(shape) != 2 || shape[1] != e.config.HiddenDim {
		return fmt.Errorf("model2vec: unexpected embeddings shape %v, expected [vocab, %d]", shape, e.config.HiddenDim)
	}

	e.embeddings = embeddings
	e.vocabSize = shape[0]

	// Release raw data references to allow GC.
	e.rawSafetensors = nil
	e.rawTokenizerJSON = nil
	e.rawConfigJSON = nil

	return nil
}

// embedOne computes a single embedding. Must be called after init().
// Thread-safe: all shared state is read-only after initialization.
func (e *Model2VecEmbedder) embedOne(text string) []float32 {
	dim := e.config.HiddenDim
	out := e.tokenizer.Tokenize(text, MaxSeqLen)

	result := make([]float32, dim)
	var count float32

	for i, mask := range out.AttentionMask {
		if mask == 0 {
			break
		}
		tokenID := out.InputIDs[i]

		// Skip [CLS] and [SEP] special tokens — they don't contribute
		// meaningful content to the embedding.
		if tokenID == e.tokenizer.clsID || tokenID == e.tokenizer.sepID {
			continue
		}

		// Bounds check against embedding matrix.
		if tokenID < 0 || int(tokenID) >= e.vocabSize {
			continue
		}

		// Look up embedding row and accumulate.
		offset := int(tokenID) * dim
		for d := 0; d < dim; d++ {
			result[d] += e.embeddings[offset+d]
		}
		count++
	}

	// Mean pool.
	if count > 0 {
		for d := range result {
			result[d] /= count
		}
	}

	// L2 normalize.
	if e.config.Normalize {
		l2Normalize(result)
	}

	return result
}
