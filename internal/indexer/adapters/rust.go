package adapters

import (
	_ "embed"

	sitter "github.com/smacker/go-tree-sitter"
	rust "github.com/smacker/go-tree-sitter/rust"
)

//go:embed queries/rust_symbols.scm
var rustSymbolsQuery []byte

//go:embed queries/rust_deps.scm
var rustDepsQuery []byte

// RustAdapter implements the LanguageAdapter interface for Rust source files (.rs).
// It uses the Tree-sitter Rust grammar for parsing.
type RustAdapter struct{}

// NewRustAdapter creates a new RustAdapter.
func NewRustAdapter() *RustAdapter {
	return &RustAdapter{}
}

// Name returns "rust".
func (a *RustAdapter) Name() string {
	return "rust"
}

// Extensions returns [".rs"].
func (a *RustAdapter) Extensions() []string {
	return []string{".rs"}
}

// GetLanguage returns the Tree-sitter Rust grammar.
func (a *RustAdapter) GetLanguage() *sitter.Language {
	return rust.GetLanguage()
}

// GetSymbolQuery returns the Scheme query for extracting Rust symbols.
func (a *RustAdapter) GetSymbolQuery() []byte {
	return rustSymbolsQuery
}

// GetDependencyQuery returns the Scheme query for extracting Rust dependencies.
func (a *RustAdapter) GetDependencyQuery() []byte {
	return rustDepsQuery
}
