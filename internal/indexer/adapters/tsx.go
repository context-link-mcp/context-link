package adapters

import (
	_ "embed"

	sitter "github.com/smacker/go-tree-sitter"
	tsx "github.com/smacker/go-tree-sitter/typescript/tsx"
)

// TSX symbol and dependency queries are the same as TypeScript since the
// AST node types are identical between TS and TSX grammars — only the
// parser behavior differs (TSX can parse JSX tags).
//
//go:embed queries/ts_symbols.scm
var tsxSymbolQuery []byte

//go:embed queries/ts_deps.scm
var tsxDepsQuery []byte

// TSXAdapter implements the LanguageAdapter interface for TSX/JSX files.
// It uses the TSX grammar which can parse JSX tags but may misparse
// advanced generics in pure .ts files — hence the separate adapter.
type TSXAdapter struct{}

// NewTSXAdapter creates a new TSXAdapter.
func NewTSXAdapter() *TSXAdapter {
	return &TSXAdapter{}
}

// Name returns "tsx".
func (a *TSXAdapter) Name() string {
	return "tsx"
}

// Extensions returns [".tsx", ".jsx"].
func (a *TSXAdapter) Extensions() []string {
	return []string{".tsx", ".jsx"}
}

// GetLanguage returns the Tree-sitter TSX grammar.
func (a *TSXAdapter) GetLanguage() *sitter.Language {
	return tsx.GetLanguage()
}

// GetSymbolQuery returns the Scheme query for extracting TSX symbols.
func (a *TSXAdapter) GetSymbolQuery() []byte {
	return tsxSymbolQuery
}

// GetDependencyQuery returns the Scheme query for extracting TSX dependencies.
func (a *TSXAdapter) GetDependencyQuery() []byte {
	return tsxDepsQuery
}
