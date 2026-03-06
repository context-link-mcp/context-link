package adapters

import (
	_ "embed"

	sitter "github.com/smacker/go-tree-sitter"
	golang "github.com/smacker/go-tree-sitter/golang"
)

//go:embed queries/go_symbols.scm
var goSymbolsQuery []byte

//go:embed queries/go_deps.scm
var goDepsQuery []byte

// GoAdapter implements the LanguageAdapter interface for Go source files (.go).
// It uses the Tree-sitter Go grammar for parsing.
type GoAdapter struct{}

// NewGoAdapter creates a new GoAdapter.
func NewGoAdapter() *GoAdapter {
	return &GoAdapter{}
}

// Name returns "go".
func (a *GoAdapter) Name() string {
	return "go"
}

// Extensions returns [".go"].
func (a *GoAdapter) Extensions() []string {
	return []string{".go"}
}

// GetLanguage returns the Tree-sitter Go grammar.
func (a *GoAdapter) GetLanguage() *sitter.Language {
	return golang.GetLanguage()
}

// GetSymbolQuery returns the Scheme query for extracting Go symbols.
func (a *GoAdapter) GetSymbolQuery() []byte {
	return goSymbolsQuery
}

// GetDependencyQuery returns the Scheme query for extracting Go dependencies.
func (a *GoAdapter) GetDependencyQuery() []byte {
	return goDepsQuery
}
