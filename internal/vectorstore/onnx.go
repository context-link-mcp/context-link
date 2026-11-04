package vectorstore

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync"

	ort "github.com/yalue/onnxruntime_go"
)

// ErrModelNotConfigured is returned when no model path has been configured.
var ErrModelNotConfigured = errors.New("vectorstore: ONNX model path not configured")

// ortGlobal guards one-time ORT environment initialization per process.
var (
	ortInitOnce sync.Once
	ortInitErr  error
)

func ensureORTEnvironment(libPath string) error {
	ortInitOnce.Do(func() {
		if libPath != "" {
			ort.SetSharedLibraryPath(libPath)
		}
		ortInitErr = ort.InitializeEnvironment()
	})
	return ortInitErr
}

// ONNXEmbedder generates embeddings using all-MiniLM-L6-v2 via OnnxRuntime.
//
// The model, tokenizer, and ORT session are initialized eagerly by
// NewONNXEmbedder, so a misconfiguration surfaces at startup instead of as
// an index run with zero embeddings. The session is reused across calls by
// updating pre-allocated input tensor data in-place before each Run().
type ONNXEmbedder struct {
	mu sync.Mutex

	modelPath string
	vocabPath string
	libPath   string

	tokenizer *BERTTokenizer

	// Pre-allocated input data slices (shapes [1, MaxSeqLen]).
	// Tensors wrap these slices; updating the slices updates tensor data.
	inputIDs []int64
	attMasks []int64
	typeIDs  []int64

	idTensor   *ort.Tensor[int64]
	maskTensor *ort.Tensor[int64]
	typeTensor *ort.Tensor[int64]
	outTensor  *ort.Tensor[float32]
	session    *ort.AdvancedSession
}

// NewONNXEmbedder creates an ONNXEmbedder and initializes it immediately:
// it checks that the model and vocab files exist, loads the vocabulary and
// the OnnxRuntime library, creates the session and runs one test embedding.
// Any failure is returned here, never deferred to the first EmbedOne call.
//
//   - modelPath: path to the all-MiniLM-L6-v2.onnx file.
//   - vocabPath: path to the accompanying vocab.txt file.
//   - libPath:   path to the OnnxRuntime shared library (leave "" to use the
//     system default or the path set via ONNXRUNTIME_LIB environment variable).
func NewONNXEmbedder(modelPath, vocabPath, libPath string) (*ONNXEmbedder, error) {
	if modelPath == "" || vocabPath == "" {
		return nil, ErrModelNotConfigured
	}
	for _, p := range []struct{ what, path string }{{"model", modelPath}, {"vocab", vocabPath}} {
		info, err := os.Stat(p.path)
		if err != nil {
			return nil, fmt.Errorf("vectorstore: ONNX %s file: %w", p.what, err)
		}
		if info.IsDir() {
			return nil, fmt.Errorf("vectorstore: ONNX %s path %s is a directory", p.what, p.path)
		}
	}
	e := &ONNXEmbedder{
		modelPath: modelPath,
		vocabPath: vocabPath,
		libPath:   libPath,
	}
	if err := e.initialize(); err != nil {
		e.Close() //nolint:errcheck
		return nil, err
	}
	// A test embedding catches models whose inputs/outputs or shape differ
	// from all-MiniLM-L6-v2, which session creation alone does not.
	if _, err := e.inferOne("context-link onnx self test"); err != nil {
		e.Close() //nolint:errcheck
		return nil, fmt.Errorf("vectorstore: ONNX test embedding: %w", err)
	}
	return e, nil
}

// NewConfiguredEmbedder returns the embedder the configuration asks for:
// ONNX when both modelPath and vocabPath are set and the model initializes,
// otherwise the built-in Model2Vec embedder. A configured ONNX model that
// fails to initialize logs a warning and falls back to Model2Vec, so indexing
// still produces (128-dim) embeddings instead of none.
func NewConfiguredEmbedder(modelPath, vocabPath, libPath string) Embedder {
	if modelPath == "" && vocabPath == "" {
		return NewModel2VecEmbedder()
	}
	if modelPath == "" || vocabPath == "" {
		slog.Warn("ONNX embedder needs both a model path and a vocab path; using built-in Model2Vec",
			"model_path", modelPath, "vocab_path", vocabPath)
		return NewModel2VecEmbedder()
	}
	e, err := NewONNXEmbedder(modelPath, vocabPath, libPath)
	if err != nil {
		slog.Warn("ONNX embedder failed to initialize, falling back to built-in Model2Vec",
			"model_path", modelPath, "error", err)
		return NewModel2VecEmbedder()
	}
	return e
}

// Dim implements Embedder.
func (e *ONNXEmbedder) Dim() int { return ONNXModelDim }

// Close implements Embedder — destroys the ORT session and tensors.
func (e *ONNXEmbedder) Close() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.session != nil {
		e.session.Destroy() //nolint:errcheck
	}
	for _, t := range []*ort.Tensor[int64]{e.idTensor, e.maskTensor, e.typeTensor} {
		if t != nil {
			t.Destroy() //nolint:errcheck
		}
	}
	if e.outTensor != nil {
		e.outTensor.Destroy() //nolint:errcheck
	}
	e.session, e.idTensor, e.maskTensor, e.typeTensor, e.outTensor = nil, nil, nil, nil, nil
	return nil
}

// EmbedOne implements Embedder.
func (e *ONNXEmbedder) EmbedOne(ctx context.Context, text string) ([]float32, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.inferOne(text)
}

// EmbedBatch implements Embedder. It processes each text individually and
// returns embeddings in the same order as the input slice.
func (e *ONNXEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	results := make([][]float32, len(texts))
	for i, text := range texts {
		emb, err := e.inferOne(text)
		if err != nil {
			return nil, fmt.Errorf("vectorstore: embedding text[%d]: %w", i, err)
		}
		results[i] = emb
	}
	return results, nil
}

// initialize loads the tokenizer, ORT environment, tensors, and session.
func (e *ONNXEmbedder) initialize() error {
	// Initialize ORT environment (once per process).
	if err := ensureORTEnvironment(e.libPath); err != nil {
		return fmt.Errorf("vectorstore: ORT initialization: %w", err)
	}

	// Load vocabulary.
	tok, err := NewBERTTokenizerFromFile(e.vocabPath)
	if err != nil {
		return err
	}
	e.tokenizer = tok

	// Pre-allocate input data slices (batch=1, seqLen=MaxSeqLen).
	e.inputIDs = make([]int64, MaxSeqLen)
	e.attMasks = make([]int64, MaxSeqLen)
	e.typeIDs = make([]int64, MaxSeqLen)

	shape1D := ort.NewShape(1, int64(MaxSeqLen))

	e.idTensor, err = ort.NewTensor(shape1D, e.inputIDs)
	if err != nil {
		return fmt.Errorf("vectorstore: create input_ids tensor: %w", err)
	}
	e.maskTensor, err = ort.NewTensor(shape1D, e.attMasks)
	if err != nil {
		return fmt.Errorf("vectorstore: create attention_mask tensor: %w", err)
	}
	e.typeTensor, err = ort.NewTensor(shape1D, e.typeIDs)
	if err != nil {
		return fmt.Errorf("vectorstore: create token_type_ids tensor: %w", err)
	}

	// Pre-allocate output tensor: [1, MaxSeqLen, ONNXModelDim].
	outShape := ort.NewShape(1, int64(MaxSeqLen), int64(ONNXModelDim))
	e.outTensor, err = ort.NewEmptyTensor[float32](outShape)
	if err != nil {
		return fmt.Errorf("vectorstore: create output tensor: %w", err)
	}

	// Create the session once; it will be reused across all inference calls.
	e.session, err = ort.NewAdvancedSession(
		e.modelPath,
		[]string{"input_ids", "attention_mask", "token_type_ids"},
		[]string{"last_hidden_state"},
		[]ort.Value{e.idTensor, e.maskTensor, e.typeTensor},
		[]ort.Value{e.outTensor},
		nil,
	)
	if err != nil {
		return fmt.Errorf("vectorstore: create ORT session: %w", err)
	}

	return nil
}

// inferOne tokenizes text, runs inference, and returns a mean-pooled,
// L2-normalized embedding. Must be called with e.mu held.
func (e *ONNXEmbedder) inferOne(text string) ([]float32, error) {
	out := e.tokenizer.Tokenize(text, MaxSeqLen)

	// Nothing the model knows (empty, or every word [UNK]): return the zero
	// vector, as Model2Vec does, instead of a [CLS]/[UNK] embedding that
	// would look similar for any unknown input. Normal inputs keep the
	// standard sentence-transformers mean pooling the model was trained with.
	if e.tokenizer.ContentTokens(out) == 0 {
		return make([]float32, ONNXModelDim), nil
	}

	// Update pre-allocated input slices in-place.
	// The tensors share the underlying array, so ORT sees the new data.
	copy(e.inputIDs, out.InputIDs)
	copy(e.attMasks, out.AttentionMask)
	copy(e.typeIDs, out.TokenTypeIDs)

	if err := e.session.Run(); err != nil {
		return nil, fmt.Errorf("vectorstore: ORT Run: %w", err)
	}

	// Mean pool over non-padding token positions.
	hidden := e.outTensor.GetData() // []float32, shape [1, MaxSeqLen, ONNXModelDim]
	embedding := make([]float32, ONNXModelDim)
	var count float32

	for s := 0; s < MaxSeqLen; s++ {
		if e.attMasks[s] == 0 {
			continue
		}
		offset := s * ONNXModelDim
		for d := 0; d < ONNXModelDim; d++ {
			embedding[d] += hidden[offset+d]
		}
		count++
	}
	if count > 0 {
		for d := range embedding {
			embedding[d] /= count
		}
	}

	l2Normalize(embedding)
	return embedding, nil
}
