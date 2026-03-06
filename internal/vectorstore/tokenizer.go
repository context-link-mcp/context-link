package vectorstore

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"unicode"
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
}

// BERTTokenizer implements BERT WordPiece tokenization.
// It lowercases input, splits on whitespace/punctuation, and applies
// WordPiece subword segmentation using the provided vocabulary.
type BERTTokenizer struct {
	vocab    map[string]int64
	maxChars int
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
	return &BERTTokenizer{vocab: vocab, maxChars: 100}, nil
}

// NewBERTTokenizerFromMap creates a tokenizer with a pre-built vocab (for testing).
func NewBERTTokenizerFromMap(vocab map[string]int64) *BERTTokenizer {
	return &BERTTokenizer{vocab: vocab, maxChars: 100}
}

// Tokenize tokenizes text and returns padded/truncated tensors of length maxLen.
// The sequence is: [CLS] token1 token2 ... tokenN [SEP] [PAD] ... [PAD]
func (t *BERTTokenizer) Tokenize(text string, maxLen int) TokenizerOutput {
	words := t.basicTokenize(text)

	// Reserve 2 slots for [CLS] and [SEP].
	maxTokens := maxLen - 2

	tokenIDs := make([]int64, 0, maxLen)
	tokenIDs = append(tokenIDs, TokenIDCLS)

	for _, word := range words {
		subwords := t.wordpieceTokenize(word)
		for _, sw := range subwords {
			if len(tokenIDs)-1 >= maxTokens {
				break
			}
			id, ok := t.vocab[sw]
			if !ok {
				tokenIDs = append(tokenIDs, TokenIDUnk)
			} else {
				tokenIDs = append(tokenIDs, id)
			}
		}
		if len(tokenIDs)-1 >= maxTokens {
			break
		}
	}
	tokenIDs = append(tokenIDs, TokenIDSEP)

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
	}
}

// basicTokenize lowercases text and splits on whitespace and punctuation.
func (t *BERTTokenizer) basicTokenize(text string) []string {
	text = strings.ToLower(text)
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
	if len(word) > t.maxChars {
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
