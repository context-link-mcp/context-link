package vectorstore

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func truncationTestTokenizer() *BERTTokenizer {
	return NewBERTTokenizerFromMap(map[string]int64{
		"[PAD]": 0, "[UNK]": 100, "[CLS]": 101, "[SEP]": 102,
		"a": 1, "b": 2, "##b": 3,
	})
}

// TestTokenize_TruncatedFlag checks that dropping tokens at the length cap is
// reported (it was silent on master, which had no Truncated field).
func TestTokenize_TruncatedFlag(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		text          string
		maxLen        int
		wantTruncated bool
		wantTokens    int // attention-mask length incl. [CLS]/[SEP]
	}{
		{"empty", "", 8, false, 2},
		{"fits", "a a a", 8, false, 5},
		{"exactly fills", strings.Repeat("a ", 6), 8, false, 8},
		{"one word over", strings.Repeat("a ", 7), 8, true, 8},
		{"subword split at the cap", strings.Repeat("a ", 5) + "bb", 8, true, 8},
		{"subwords fit exactly", strings.Repeat("a ", 4) + "bb", 8, false, 8},
	}
	tok := truncationTestTokenizer()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			out := tok.Tokenize(tt.text, tt.maxLen)
			assert.Equal(t, tt.wantTruncated, out.Truncated)
			n := 0
			for _, m := range out.AttentionMask {
				n += int(m)
			}
			assert.Equal(t, tt.wantTokens, n)
			assert.Equal(t, TokenIDSEP, out.InputIDs[n-1], "sequence still ends with [SEP]")
		})
	}
}

// TestWordpiece_CapCountsRunes guards the WordPiece length cap: it counts
// characters, not bytes, so a 60-rune word of 3-byte runes (180 bytes) is
// still split rather than mapped to [UNK].
func TestWordpiece_CapCountsRunes(t *testing.T) {
	t.Parallel()

	tok := NewBERTTokenizerFromMap(map[string]int64{
		"[PAD]": 0, "[UNK]": 100, "[CLS]": 101, "[SEP]": 102,
		"ⰰ": 8, "##ⰰ": 9,
	})
	word := strings.Repeat("ⰰ", 60) // Glagolitic small letter: 3 bytes per rune, not CJK or punctuation
	got := tok.wordpieceTokenize(word)
	assert.NotEqual(t, []string{"[UNK]"}, got)
	assert.Len(t, got, 60)
}
