package vectorstore

import (
	"encoding/json"
	"fmt"
)

// hfTokenizerJSON represents the relevant subset of a HuggingFace tokenizer.json file.
type hfTokenizerJSON struct {
	Model hfModelConfig `json:"model"`
}

type hfModelConfig struct {
	Type                    string           `json:"type"`
	Vocab                   map[string]int64 `json:"vocab"`
	UnkToken                string           `json:"unk_token"`
	ContinuingSubwordPrefix string           `json:"continuing_subword_prefix"`
	MaxInputCharsPerWord    int              `json:"max_input_chars_per_word"`
}

// NewBERTTokenizerFromHFJSON parses a HuggingFace tokenizer.json byte slice
// and returns a BERTTokenizer configured with its vocabulary and settings.
// Returns an error if the model type is not "WordPiece".
func NewBERTTokenizerFromHFJSON(data []byte) (*BERTTokenizer, error) {
	var cfg hfTokenizerJSON
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("vectorstore: failed to parse tokenizer.json: %w", err)
	}

	if cfg.Model.Type != "WordPiece" {
		return nil, fmt.Errorf("vectorstore: unsupported tokenizer model type %q, expected WordPiece", cfg.Model.Type)
	}

	if len(cfg.Model.Vocab) == 0 {
		return nil, fmt.Errorf("vectorstore: tokenizer.json has empty vocabulary")
	}

	maxChars := cfg.Model.MaxInputCharsPerWord
	if maxChars <= 0 {
		maxChars = 100
	}

	return newBERTTokenizer(cfg.Model.Vocab, maxChars), nil
}
