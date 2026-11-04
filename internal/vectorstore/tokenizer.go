package vectorstore

import (
	"bufio"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

// Standard BERT special token IDs (consistent with HuggingFace vocab.txt).
const (
	TokenIDPad = int64(0)
	TokenIDUnk = int64(100)
	TokenIDCLS = int64(101)
	TokenIDSEP = int64(102)
)

// TokenizerOutput holds the three int64 tensors required by BERT-family models.
type TokenizerOutput struct {
	InputIDs      []int64
	AttentionMask []int64
	TokenTypeIDs  []int64
	// Truncated reports that the text had more WordPiece tokens than fit in
	// maxLen ([CLS] and [SEP] included) and the rest were dropped.
	Truncated bool
}

// BERTTokenizer implements BERT WordPiece tokenization.
// It lowercases input, splits on whitespace/punctuation, and applies
// WordPiece subword segmentation using the provided vocabulary.
type BERTTokenizer struct {
	vocab    map[string]int64
	maxChars int
	padID    int64
	unkID    int64
	clsID    int64
	sepID    int64
	maskID   int64
	hasMask  bool
}

// NewBERTTokenizerFromFile loads vocabulary from a vocab.txt file.
// Each line in vocab.txt is a token; the line number (0-indexed) is the token ID.
func NewBERTTokenizerFromFile(path string) (*BERTTokenizer, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("vectorstore: failed to open vocab file %s: %w", path, err)
	}
	defer f.Close() //nolint:errcheck

	vocab := make(map[string]int64)
	scanner := bufio.NewScanner(f)
	var id int64
	for scanner.Scan() {
		token := scanner.Text()
		vocab[token] = id
		id++
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("vectorstore: failed to read vocab file: %w", err)
	}
	return newBERTTokenizer(vocab, 100), nil
}

// NewBERTTokenizerFromMap creates a tokenizer with a pre-built vocab (for testing).
func NewBERTTokenizerFromMap(vocab map[string]int64) *BERTTokenizer {
	return newBERTTokenizer(vocab, 100)
}

// newBERTTokenizer creates a BERTTokenizer with special token IDs read from
// the vocab map. Falls back to standard BERT constants if tokens are missing.
func newBERTTokenizer(vocab map[string]int64, maxChars int) *BERTTokenizer {
	t := &BERTTokenizer{
		vocab:    vocab,
		maxChars: maxChars,
		padID:    TokenIDPad,
		unkID:    TokenIDUnk,
		clsID:    TokenIDCLS,
		sepID:    TokenIDSEP,
	}
	if id, ok := vocab["[PAD]"]; ok {
		t.padID = id
	}
	if id, ok := vocab["[UNK]"]; ok {
		t.unkID = id
	}
	if id, ok := vocab["[CLS]"]; ok {
		t.clsID = id
	}
	if id, ok := vocab["[SEP]"]; ok {
		t.sepID = id
	}
	if id, ok := vocab["[MASK]"]; ok {
		t.maskID, t.hasMask = id, true
	}
	return t
}

// Tokenize tokenizes text and returns padded/truncated tensors of length maxLen.
// The sequence is: [CLS] token1 token2 ... tokenN [SEP] [PAD] ... [PAD]
func (t *BERTTokenizer) Tokenize(text string, maxLen int) TokenizerOutput {
	words := t.basicTokenize(text)

	// Reserve 2 slots for [CLS] and [SEP].
	maxTokens := maxLen - 2

	tokenIDs := make([]int64, 0, maxLen)
	tokenIDs = append(tokenIDs, t.clsID)

	truncated := false
words:
	for _, word := range words {
		for _, sw := range t.wordpieceTokenize(word) {
			if len(tokenIDs)-1 >= maxTokens {
				truncated = true
				break words
			}
			id, ok := t.vocab[sw]
			if !ok {
				tokenIDs = append(tokenIDs, t.unkID)
			} else {
				tokenIDs = append(tokenIDs, id)
			}
		}
	}
	tokenIDs = append(tokenIDs, t.sepID)

	// Build attention mask: 1 for real tokens, 0 for padding.
	seqLen := len(tokenIDs)
	attnMask := make([]int64, maxLen)
	for i := 0; i < seqLen && i < maxLen; i++ {
		attnMask[i] = 1
	}

	// Pad input IDs to maxLen with TokenIDPad.
	padded := make([]int64, maxLen)
	copy(padded, tokenIDs)

	// Token type IDs are all 0 for single-sequence classification/embedding.
	tokenTypeIDs := make([]int64, maxLen)

	return TokenizerOutput{
		InputIDs:      padded,
		AttentionMask: attnMask,
		TokenTypeIDs:  tokenTypeIDs,
		Truncated:     truncated,
	}
}

// logTruncated logs at debug level when an embedding input lost tokens to
// the MaxSeqLen cap, so the truncation is visible instead of silent.
func logTruncated(out TokenizerOutput, text string) {
	if !out.Truncated {
		return
	}
	preview := text
	if i := strings.IndexByte(preview, '\n'); i >= 0 {
		preview = preview[:i]
	}
	if utf8.RuneCountInString(preview) > 80 {
		preview = string([]rune(preview)[:80])
	}
	slog.Debug("vectorstore: embedding input truncated", "max_tokens", MaxSeqLen, "bytes", len(text), "text", preview)
}

// IsSpecial reports whether id is one of the tokenizer's special tokens
// ([PAD], [UNK], [CLS], [SEP], [MASK]). Special tokens carry no content, so
// pooling over them makes unrelated inputs look alike.
func (t *BERTTokenizer) IsSpecial(id int64) bool {
	return id == t.padID || id == t.unkID || id == t.clsID || id == t.sepID || (t.hasMask && id == t.maskID)
}

// ContentTokens returns how many tokens of out are real vocabulary tokens,
// i.e. attended and not special. Zero means the input had nothing the model
// knows (empty, or every word mapped to [UNK]).
func (t *BERTTokenizer) ContentTokens(out TokenizerOutput) int {
	n := 0
	for i, mask := range out.AttentionMask {
		if mask == 0 {
			break
		}
		if !t.IsSpecial(out.InputIDs[i]) {
			n++
		}
	}
	return n
}

// normalize applies the BERT BasicTokenizer normalisation used by uncased
// models (HF BertNormalizer with clean_text, handle_chinese_chars and
// lowercase, strip_accents following lowercase):
//   - drops NUL, U+FFFD and control/format characters, maps whitespace to ' '
//   - puts spaces around CJK ideographs so each becomes its own word
//   - lowercases, decomposes to NFD and strips combining marks ("café" → "cafe")
func normalize(text string) string {
	var b strings.Builder
	b.Grow(len(text))
	for _, r := range text {
		switch {
		case r == 0 || r == utf8.RuneError:
			continue
		case isBERTWhitespace(r):
			b.WriteByte(' ')
		case isBERTControl(r):
			continue
		case isCJK(r):
			b.WriteByte(' ')
			b.WriteRune(r)
			b.WriteByte(' ')
		default:
			b.WriteRune(r)
		}
	}
	lowered := strings.ToLower(b.String())

	var out strings.Builder
	out.Grow(len(lowered))
	for _, r := range norm.NFD.String(lowered) {
		if unicode.Is(unicode.Mn, r) {
			continue
		}
		out.WriteRune(r)
	}
	return out.String()
}

// isBERTWhitespace mirrors BERT's _is_whitespace: \t, \n, \r and Unicode Zs.
func isBERTWhitespace(r rune) bool {
	if r == ' ' || r == '\t' || r == '\n' || r == '\r' {
		return true
	}
	return unicode.Is(unicode.Zs, r)
}

// isBERTControl mirrors BERT's _is_control: Cc and Cf, except the whitespace
// characters handled above.
func isBERTControl(r rune) bool {
	return unicode.Is(unicode.Cc, r) || unicode.Is(unicode.Cf, r)
}

// isCJK reports whether r is in the CJK Unified Ideographs blocks BERT splits
// on (Hangul, Hiragana and Katakana are not included, as in BERT).
func isCJK(r rune) bool {
	return (r >= 0x4E00 && r <= 0x9FFF) ||
		(r >= 0x3400 && r <= 0x4DBF) ||
		(r >= 0x20000 && r <= 0x2A6DF) ||
		(r >= 0x2A700 && r <= 0x2B73F) ||
		(r >= 0x2B740 && r <= 0x2B81F) ||
		(r >= 0x2B820 && r <= 0x2CEAF) ||
		(r >= 0xF900 && r <= 0xFAFF) ||
		(r >= 0x2F800 && r <= 0x2FA1F)
}

// basicTokenize normalises text (see normalize) and splits it on whitespace
// and punctuation.
func (t *BERTTokenizer) basicTokenize(text string) []string {
	text = normalize(text)
	var words []string
	var cur strings.Builder

	for _, r := range text {
		if unicode.IsSpace(r) {
			if cur.Len() > 0 {
				words = append(words, cur.String())
				cur.Reset()
			}
		} else if isPunct(r) {
			if cur.Len() > 0 {
				words = append(words, cur.String())
				cur.Reset()
			}
			words = append(words, string(r))
		} else {
			cur.WriteRune(r)
		}
	}
	if cur.Len() > 0 {
		words = append(words, cur.String())
	}
	return words
}

// isPunct returns true for Unicode punctuation and symbol characters.
func isPunct(r rune) bool {
	return unicode.IsPunct(r) || unicode.IsSymbol(r)
}

// wordpieceTokenize applies WordPiece subword tokenization to a single word.
// Returns ["[UNK]"] if the word cannot be segmented.
func (t *BERTTokenizer) wordpieceTokenize(word string) []string {
	if utf8.RuneCountInString(word) > t.maxChars {
		return []string{"[UNK]"}
	}
	// Fast path: whole word is in vocab.
	if _, ok := t.vocab[word]; ok {
		return []string{word}
	}

	runes := []rune(word)
	var subwords []string
	start := 0

	for start < len(runes) {
		end := len(runes)
		found := false

		for end > start {
			substr := string(runes[start:end])
			if start > 0 {
				substr = "##" + substr
			}
			if _, ok := t.vocab[substr]; ok {
				subwords = append(subwords, substr)
				start = end
				found = true
				break
			}
			end--
		}
		if !found {
			return []string{"[UNK]"}
		}
	}
	return subwords
}
