package adapters

import (
	_ "embed"

	sitter "github.com/smacker/go-tree-sitter"
	clang "github.com/smacker/go-tree-sitter/c"
)

//go:embed queries/c_symbols.scm
var cSymbolsQuery []byte

//go:embed queries/c_deps.scm
var cDepsQuery []byte

// CAdapter implements the LanguageAdapter interface for C source files (.c, .h).
// It uses the Tree-sitter C grammar for parsing.
type CAdapter struct{}

// NewCAdapter creates a new CAdapter.
func NewCAdapter() *CAdapter {
	return &CAdapter{}
}

// Name returns "c".
func (a *CAdapter) Name() string {
	return "c"
}

// Extensions returns [".c", ".h"].
func (a *CAdapter) Extensions() []string {
	return []string{".c", ".h"}
}

// GetLanguage returns the Tree-sitter C grammar.
func (a *CAdapter) GetLanguage() *sitter.Language {
	return clang.GetLanguage()
}

// GetSymbolQuery returns the Scheme query for extracting C symbols.
func (a *CAdapter) GetSymbolQuery() []byte {
	return cSymbolsQuery
}

// GetDependencyQuery returns the Scheme query for extracting C dependencies.
func (a *CAdapter) GetDependencyQuery() []byte {
	return cDepsQuery
}
