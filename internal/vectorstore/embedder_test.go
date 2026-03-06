package vectorstore_test

import (
	"context"
	"fmt"
	"math"
	"os"
	"testing"

	"github.com/context-link/context-link/internal/vectorstore"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMockEmbedder_Dim verifies the embedding dimension matches what was requested.
func TestMockEmbedder_Dim(t *testing.T) {
	t.Parallel()
	e := vectorstore.NewMockEmbedder(64)
	assert.Equal(t, 64, e.Dim())
}

// TestMockEmbedder_DefaultDim uses the default ModelDim when 0 is passed.
func TestMockEmbedder_DefaultDim(t *testing.T) {
	t.Parallel()
	e := vectorstore.NewMockEmbedder(0)
	assert.Equal(t, vectorstore.ModelDim, e.Dim())
}

// TestMockEmbedder_Deterministic verifies same input always produces the same embedding.
func TestMockEmbedder_Deterministic(t *testing.T) {
	t.Parallel()
	e := vectorstore.NewMockEmbedder(0)
	ctx := context.Background()

	a, err := e.EmbedOne(ctx, "hello world")
	require.NoError(t, err)

	b, err := e.EmbedOne(ctx, "hello world")
	require.NoError(t, err)

	assert.Equal(t, a, b, "same text must produce identical embeddings")
}

// TestMockEmbedder_Unique verifies different inputs produce different embeddings.
func TestMockEmbedder_Unique(t *testing.T) {
	t.Parallel()
	e := vectorstore.NewMockEmbedder(0)
	ctx := context.Background()

	a, err := e.EmbedOne(ctx, "function validateToken")
	require.NoError(t, err)

	b, err := e.EmbedOne(ctx, "class UserAuth")
	require.NoError(t, err)

	assert.NotEqual(t, a, b, "different texts must produce different embeddings")
}

// TestMockEmbedder_UnitNorm verifies embeddings are L2-normalized.
func TestMockEmbedder_UnitNorm(t *testing.T) {
	t.Parallel()
	e := vectorstore.NewMockEmbedder(0)
	ctx := context.Background()

	tests := []string{
		"function validateToken",
		"class UserAuth",
		"interface Repository",
		"",
	}
	for _, text := range tests {
		emb, err := e.EmbedOne(ctx, text)
		require.NoError(t, err)
		require.Len(t, emb, vectorstore.ModelDim)

		var norm float64
		for _, x := range emb {
			norm += float64(x) * float64(x)
		}
		norm = math.Sqrt(norm)
		assert.InDelta(t, 1.0, norm, 1e-5, "embedding for %q should be unit length", text)
	}
}

// TestMockEmbedder_Batch verifies EmbedBatch returns same results as individual EmbedOne calls.
func TestMockEmbedder_Batch(t *testing.T) {
	t.Parallel()
	e := vectorstore.NewMockEmbedder(0)
	ctx := context.Background()

	texts := []string{"foo", "bar", "baz"}
	batch, err := e.EmbedBatch(ctx, texts)
	require.NoError(t, err)
	require.Len(t, batch, len(texts))

	for i, text := range texts {
		single, err := e.EmbedOne(ctx, text)
		require.NoError(t, err)
		assert.Equal(t, single, batch[i], "batch[%d] should match EmbedOne(%q)", i, text)
	}
}

// TestSymbolEmbedText verifies the embedding input format for symbol kinds.
func TestSymbolEmbedText(t *testing.T) {
	t.Parallel()

	tests := []struct {
		kind          string
		qualifiedName string
		codeBlock     string
		want          string
	}{
		{
			kind:          "function",
			qualifiedName: "validateToken",
			codeBlock:     "function validateToken(token: string): boolean {\n  return token.length > 0;\n}",
			want:          "function validateToken: function validateToken(token: string): boolean {",
		},
		{
			kind:          "class",
			qualifiedName: "UserAuth",
			codeBlock:     "class UserAuth {",
			want:          "class UserAuth: class UserAuth {",
		},
		{
			kind:          "function",
			qualifiedName: "noNewline",
			codeBlock:     "const x = 1",
			want:          "function noNewline: const x = 1",
		},
		{
			kind:          "interface",
			qualifiedName: "Repo",
			codeBlock:     "  interface Repo {  ",
			want:          "interface Repo: interface Repo {",
		},
	}
	for _, tc := range tests {
		got := vectorstore.SymbolEmbedText(tc.kind, tc.qualifiedName, tc.codeBlock)
		assert.Equal(t, tc.want, got, "SymbolEmbedText(%q, %q)", tc.kind, tc.qualifiedName)
	}
}

// TestBERTTokenizer_BasicTokenize verifies CLS/SEP injection and padding.
func TestBERTTokenizer_BasicTokenize(t *testing.T) {
	t.Parallel()

	// Minimal vocab with known tokens.
	vocab := map[string]int64{
		"[PAD]": 0, "[UNK]": 100, "[CLS]": 101, "[SEP]": 102,
		"hello": 7592, "world": 2088,
	}
	tok := vectorstore.NewBERTTokenizerFromMap(vocab)

	out := tok.Tokenize("hello world", 8)

	assert.Equal(t, int64(101), out.InputIDs[0], "first token must be [CLS]")
	assert.Equal(t, int64(7592), out.InputIDs[1], "second token must be 'hello'")
	assert.Equal(t, int64(2088), out.InputIDs[2], "third token must be 'world'")
	assert.Equal(t, int64(102), out.InputIDs[3], "fourth token must be [SEP]")
	assert.Equal(t, int64(0), out.InputIDs[4], "fifth token must be [PAD]")

	assert.Len(t, out.InputIDs, 8)
	assert.Len(t, out.AttentionMask, 8)
	assert.Len(t, out.TokenTypeIDs, 8)
}

// TestBERTTokenizer_AttentionMask verifies real tokens are masked, padding is not.
func TestBERTTokenizer_AttentionMask(t *testing.T) {
	t.Parallel()

	vocab := map[string]int64{
		"[PAD]": 0, "[UNK]": 100, "[CLS]": 101, "[SEP]": 102,
		"hi": 2299,
	}
	tok := vectorstore.NewBERTTokenizerFromMap(vocab)
	out := tok.Tokenize("hi", 8)

	// [CLS] hi [SEP] [PAD] [PAD] [PAD] [PAD] [PAD]
	assert.Equal(t, []int64{1, 1, 1, 0, 0, 0, 0, 0}, out.AttentionMask)
}

// TestBERTTokenizer_Truncation verifies long sequences are truncated to maxLen.
func TestBERTTokenizer_Truncation(t *testing.T) {
	t.Parallel()

	vocab := map[string]int64{
		"[PAD]": 0, "[UNK]": 100, "[CLS]": 101, "[SEP]": 102,
		"a": 1, "b": 2, "c": 3, "d": 4, "e": 5,
	}
	tok := vectorstore.NewBERTTokenizerFromMap(vocab)
	// Provide 10 words but maxLen=6: [CLS] a b c d [SEP]
	out := tok.Tokenize("a b c d e a b c d e", 6)

	assert.Len(t, out.InputIDs, 6)
	assert.Equal(t, int64(101), out.InputIDs[0], "[CLS]")
	assert.Equal(t, int64(102), out.InputIDs[5], "[SEP]")
}

// TestBERTTokenizer_UnknownToken verifies unknown tokens map to [UNK].
func TestBERTTokenizer_UnknownToken(t *testing.T) {
	t.Parallel()

	vocab := map[string]int64{
		"[PAD]": 0, "[UNK]": 100, "[CLS]": 101, "[SEP]": 102,
	}
	tok := vectorstore.NewBERTTokenizerFromMap(vocab)
	out := tok.Tokenize("unknownword", 8)

	assert.Equal(t, int64(100), out.InputIDs[1], "unknown word should map to [UNK]")
}

// TestBERTTokenizer_WordPiece verifies WordPiece subword segmentation with ## prefix.
func TestBERTTokenizer_WordPiece(t *testing.T) {
	t.Parallel()

	// Vocab contains whole word and a suffix fragment.
	vocab := map[string]int64{
		"[PAD]": 0, "[UNK]": 100, "[CLS]": 101, "[SEP]": 102,
		"un": 200, "##known": 201,
	}
	tok := vectorstore.NewBERTTokenizerFromMap(vocab)
	out := tok.Tokenize("unknown", 8)

	// Should segment into "un" + "##known"
	assert.Equal(t, int64(200), out.InputIDs[1], "first subword should be 'un'")
	assert.Equal(t, int64(201), out.InputIDs[2], "second subword should be '##known'")
}

// TestBERTTokenizer_MaxCharsExceeded verifies overlong words map to [UNK].
func TestBERTTokenizer_MaxCharsExceeded(t *testing.T) {
	t.Parallel()

	vocab := map[string]int64{
		"[PAD]": 0, "[UNK]": 100, "[CLS]": 101, "[SEP]": 102,
	}
	tok := vectorstore.NewBERTTokenizerFromMap(vocab)
	longWord := string(make([]byte, 200)) // 200 chars > maxChars (100)
	out := tok.Tokenize(longWord, 8)

	assert.Equal(t, int64(100), out.InputIDs[1], "word longer than maxChars should map to [UNK]")
}

// TestBERTTokenizer_TokenTypeIDsAllZero verifies token type IDs are always zero for single sequences.
func TestBERTTokenizer_TokenTypeIDsAllZero(t *testing.T) {
	t.Parallel()

	vocab := map[string]int64{
		"[PAD]": 0, "[UNK]": 100, "[CLS]": 101, "[SEP]": 102,
		"hello": 7592,
	}
	tok := vectorstore.NewBERTTokenizerFromMap(vocab)
	out := tok.Tokenize("hello", 8)

	for i, v := range out.TokenTypeIDs {
		assert.Equal(t, int64(0), v, "token_type_ids[%d] must be 0 for single-sequence input", i)
	}
}

// TestBERTTokenizerFromFile verifies loading vocab from a real file.
func TestBERTTokenizerFromFile(t *testing.T) {
	t.Parallel()

	// Write a minimal vocab.txt matching standard BERT ordering:
	// [PAD]=0, … [CLS]=101, [SEP]=102 so the hardcoded constants match.
	dir := t.TempDir()
	vocabPath := dir + "/vocab.txt"

	// Build 103-line vocab: 0–99 = unused, 100 = [UNK], 101 = [CLS], 102 = [SEP]
	// plus "hello" at id=200, "world" at id=201.
	vocabLines := ""
	for i := 0; i < 100; i++ {
		vocabLines += "[unused" + fmt.Sprintf("%d", i) + "]\n"
	}
	vocabLines += "[UNK]\n[CLS]\n[SEP]\n" // 100, 101, 102
	for i := 103; i < 200; i++ {
		vocabLines += "[unused" + fmt.Sprintf("%d", i) + "]\n"
	}
	vocabLines += "hello\nworld\n" // 200, 201

	require.NoError(t, os.WriteFile(vocabPath, []byte(vocabLines), 0o600))

	tok, err := vectorstore.NewBERTTokenizerFromFile(vocabPath)
	require.NoError(t, err)
	require.NotNil(t, tok)

	out := tok.Tokenize("hello world", 8)
	// Tokenize uses hardcoded TokenIDCLS=101 and TokenIDSEP=102.
	assert.Equal(t, vectorstore.TokenIDCLS, out.InputIDs[0], "[CLS] should be id=101")
	assert.Equal(t, int64(200), out.InputIDs[1], "'hello' should be id=200")
	assert.Equal(t, int64(201), out.InputIDs[2], "'world' should be id=201")
	assert.Equal(t, vectorstore.TokenIDSEP, out.InputIDs[3], "[SEP] should be id=102")
}

// TestBERTTokenizerFromFile_NotFound verifies an error is returned for missing files.
func TestBERTTokenizerFromFile_NotFound(t *testing.T) {
	t.Parallel()

	_, err := vectorstore.NewBERTTokenizerFromFile("/nonexistent/path/vocab.txt")
	assert.Error(t, err, "should error on missing vocab file")
}

// TestMockEmbedder_Close verifies Close is a no-op.
func TestMockEmbedder_Close(t *testing.T) {
	t.Parallel()
	e := vectorstore.NewMockEmbedder(0)
	assert.NoError(t, e.Close())
}
