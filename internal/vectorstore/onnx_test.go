package vectorstore

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestONNXEmbedder_UseAfterClose checks that Close is idempotent and that the
// embedder refuses work afterwards instead of dereferencing destroyed ORT
// handles. Panics on master (nil session / tokenizer). No ONNX runtime is
// needed: the handles are already nil, which is the state Close leaves.
func TestONNXEmbedder_UseAfterClose(t *testing.T) {
	t.Parallel()

	tok := NewBERTTokenizerFromMap(map[string]int64{"[PAD]": 0, "[UNK]": 1, "[CLS]": 2, "[SEP]": 3, "hello": 4})
	e := &ONNXEmbedder{tokenizer: tok}

	require.NoError(t, e.Close())
	require.NoError(t, e.Close(), "second Close is a no-op")

	ctx := context.Background()
	_, err := e.EmbedOne(ctx, "hello")
	assert.ErrorIs(t, err, ErrEmbedderClosed)
	_, err = e.EmbedBatch(ctx, []string{"hello", "world"})
	assert.ErrorIs(t, err, ErrEmbedderClosed)
}
