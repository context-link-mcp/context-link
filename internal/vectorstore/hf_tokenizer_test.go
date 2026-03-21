package vectorstore

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewBERTTokenizerFromHFJSON_Valid(t *testing.T) {
	t.Parallel()

	data := []byte(`{
		"model": {
			"type": "WordPiece",
			"unk_token": "[UNK]",
			"continuing_subword_prefix": "##",
			"max_input_chars_per_word": 100,
			"vocab": {
				"[PAD]": 0, "[UNK]": 100, "[CLS]": 101, "[SEP]": 102,
				"hello": 7592, "world": 2088, "##s": 1055
			}
		}
	}`)

	tok, err := NewBERTTokenizerFromHFJSON(data)
	require.NoError(t, err)
	require.NotNil(t, tok)
	assert.Equal(t, int64(7592), tok.vocab["hello"])
	assert.Equal(t, 100, tok.maxChars)
}

func TestNewBERTTokenizerFromHFJSON_WrongModelType(t *testing.T) {
	t.Parallel()

	data := []byte(`{
		"model": {
			"type": "BPE",
			"vocab": {"a": 0}
		}
	}`)

	_, err := NewBERTTokenizerFromHFJSON(data)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported tokenizer model type")
}

func TestNewBERTTokenizerFromHFJSON_EmptyVocab(t *testing.T) {
	t.Parallel()

	data := []byte(`{
		"model": {
			"type": "WordPiece",
			"vocab": {}
		}
	}`)

	_, err := NewBERTTokenizerFromHFJSON(data)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "empty vocabulary")
}

func TestNewBERTTokenizerFromHFJSON_SpecialTokenIDs(t *testing.T) {
	t.Parallel()

	data := []byte(`{
		"model": {
			"type": "WordPiece",
			"vocab": {
				"[PAD]": 5, "[UNK]": 10, "[CLS]": 20, "[SEP]": 30,
				"test": 1
			}
		}
	}`)

	tok, err := NewBERTTokenizerFromHFJSON(data)
	require.NoError(t, err)
	assert.Equal(t, int64(5), tok.padID)
	assert.Equal(t, int64(10), tok.unkID)
	assert.Equal(t, int64(20), tok.clsID)
	assert.Equal(t, int64(30), tok.sepID)
}

func TestNewBERTTokenizerFromHFJSON_Tokenize(t *testing.T) {
	t.Parallel()

	data := []byte(`{
		"model": {
			"type": "WordPiece",
			"max_input_chars_per_word": 100,
			"vocab": {
				"[PAD]": 0, "[UNK]": 100, "[CLS]": 101, "[SEP]": 102,
				"hello": 7592, "world": 2088
			}
		}
	}`)

	tok, err := NewBERTTokenizerFromHFJSON(data)
	require.NoError(t, err)

	out := tok.Tokenize("Hello World", 8)
	// Should be: [CLS]=101, hello=7592, world=2088, [SEP]=102, [PAD]*4
	assert.Equal(t, int64(101), out.InputIDs[0])
	assert.Equal(t, int64(7592), out.InputIDs[1])
	assert.Equal(t, int64(2088), out.InputIDs[2])
	assert.Equal(t, int64(102), out.InputIDs[3])
	assert.Equal(t, int64(0), out.InputIDs[4])

	// Attention mask: 4 real tokens, rest padding
	assert.Equal(t, int64(1), out.AttentionMask[0])
	assert.Equal(t, int64(1), out.AttentionMask[3])
	assert.Equal(t, int64(0), out.AttentionMask[4])
}

func TestNewBERTTokenizerFromHFJSON_MalformedJSON(t *testing.T) {
	t.Parallel()

	_, err := NewBERTTokenizerFromHFJSON([]byte("not json"))
	assert.Error(t, err)
}

func TestNewBERTTokenizerFromHFJSON_DefaultMaxChars(t *testing.T) {
	t.Parallel()

	data := []byte(`{
		"model": {
			"type": "WordPiece",
			"vocab": {"[PAD]": 0, "a": 1}
		}
	}`)

	tok, err := NewBERTTokenizerFromHFJSON(data)
	require.NoError(t, err)
	assert.Equal(t, 100, tok.maxChars)
}
