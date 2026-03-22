package adapters

import (
	_ "embed"

	sitter "github.com/smacker/go-tree-sitter"
	java "github.com/smacker/go-tree-sitter/java"
)

//go:embed queries/java_symbols.scm
var javaSymbolsQuery []byte

//go:embed queries/java_deps.scm
var javaDepsQuery []byte

// JavaAdapter implements the LanguageAdapter interface for Java source files (.java).
// It uses the Tree-sitter Java grammar for parsing.
type JavaAdapter struct{}

// NewJavaAdapter creates a new JavaAdapter.
func NewJavaAdapter() *JavaAdapter {
	return &JavaAdapter{}
}

// Name returns "java".
func (a *JavaAdapter) Name() string {
	return "java"
}

// Extensions returns [".java"].
func (a *JavaAdapter) Extensions() []string {
	return []string{".java"}
}

// GetLanguage returns the Tree-sitter Java grammar.
func (a *JavaAdapter) GetLanguage() *sitter.Language {
	return java.GetLanguage()
}

// GetSymbolQuery returns the Scheme query for extracting Java symbols.
func (a *JavaAdapter) GetSymbolQuery() []byte {
	return javaSymbolsQuery
}

// GetDependencyQuery returns the Scheme query for extracting Java dependencies.
func (a *JavaAdapter) GetDependencyQuery() []byte {
	return javaDepsQuery
}
