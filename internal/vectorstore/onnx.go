package vectorstore

import (
	"context"
	"errors"
	"fmt"
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
// The model, tokenizer, and ORT session are initialized lazily on the first
// call to EmbedOne or EmbedBatch. The session is reused across calls by
// updating pre-allocated input tensor data in-place before each Run().
type ONNXEmbedder struct {
	mu   sync.Mutex
	once sync.Once
	err  error

	modelPath string
	vocabPath string
	libPath   string

	// Lazily initialized fields below.
	tokenizer *BERTTokenizer

	// Pre-allocated input data slices (shapes [1, MaxSeqLen]).
	// Tensors wrap these slices; updating the slices updates tensor data.
	inputIDs  []int64
	attMasks  []int64
	typeIDs   []int64

	idTensor   *ort.Tensor[int64]
	maskTensor *ort.Tensor[int64]
	typeTensor *ort.Tensor[int64]
	outTensor  *ort.Tensor[float32]
	session    *ort.AdvancedSession
}

// NewONNXEmbedder creates an ONNXEmbedder that will lazily initialize the
// ORT session on first use.
//
//   - modelPath: path to the all-MiniLM-L6-v2.onnx file.
//   - vocabPath: path to the accompanying vocab.txt file.
//   - libPath:   path to the OnnxRuntime shared library (leave "" to use the
//     system default or the path set via ONNXRUNTIME_LIB environment variable).
func NewONNXEmbedder(modelPath, vocabPath, libPath string) (*ONNXEmbedder, error) {
	if modelPath == "" || vocabPath == "" {
		return nil, ErrModelNotConfigured
	}
	return &ONNXEmbedder{
		modelPath: modelPath,
		vocabPath: vocabPath,
		libPath:   libPath,
	}, nil
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
	return nil
}

// EmbedOne implements Embedder.
func (e *ONNXEmbedder) EmbedOne(ctx context.Context, text string) ([]float32, error) {
	if err := e.ensureInitialized(); err != nil {
		return nil, err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.inferOne(text)
}

// EmbedBatch implements Embedder. It processes each text individually and
// returns embeddings in the same order as the input slice.
func (e *ONNXEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	if err := e.ensureInitialized(); err != nil {
		return nil, err
	}
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

// ensureInitialized runs initialization exactly once; returns any init error.
func (e *ONNXEmbedder) ensureInitialized() error {
	e.once.Do(func() {
		e.err = e.initialize()
	})
	return e.err
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
