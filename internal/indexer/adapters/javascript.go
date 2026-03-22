package adapters

import (
	_ "embed"

	sitter "github.com/smacker/go-tree-sitter"
	javascript "github.com/smacker/go-tree-sitter/javascript"
)

//go:embed queries/js_symbols.scm
var jsSymbolsQuery []byte

//go:embed queries/js_deps.scm
var jsDepsQuery []byte

// JavaScriptAdapter implements the LanguageAdapter interface for JavaScript source files (.js, .mjs).
// It uses the Tree-sitter JavaScript grammar for parsing.
type JavaScriptAdapter struct{}

// NewJavaScriptAdapter creates a new JavaScriptAdapter.
func NewJavaScriptAdapter() *JavaScriptAdapter {
	return &JavaScriptAdapter{}
}

// Name returns "javascript".
func (a *JavaScriptAdapter) Name() string {
	return "javascript"
}

// Extensions returns [".js", ".mjs"].
func (a *JavaScriptAdapter) Extensions() []string {
	return []string{".js", ".mjs"}
}

// GetLanguage returns the Tree-sitter JavaScript grammar.
func (a *JavaScriptAdapter) GetLanguage() *sitter.Language {
	return javascript.GetLanguage()
}

// GetSymbolQuery returns the Scheme query for extracting JavaScript symbols.
func (a *JavaScriptAdapter) GetSymbolQuery() []byte {
	return jsSymbolsQuery
}

// GetDependencyQuery returns the Scheme query for extracting JavaScript dependencies.
func (a *JavaScriptAdapter) GetDependencyQuery() []byte {
	return jsDepsQuery
}
