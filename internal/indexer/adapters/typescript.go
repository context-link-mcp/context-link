// Package adapters provides LanguageAdapter implementations for supported languages.
package adapters

import (
	_ "embed"

	sitter "github.com/smacker/go-tree-sitter"
	typescript "github.com/smacker/go-tree-sitter/typescript/typescript"
)

//go:embed queries/ts_symbols.scm
var tsSymbolQuery []byte

//go:embed queries/ts_deps.scm
var tsDepsQuery []byte

// TypeScriptAdapter implements the LanguageAdapter interface for pure TypeScript
// files (.ts). It uses the TypeScript grammar which correctly handles advanced
// generics but cannot parse JSX tags.
type TypeScriptAdapter struct{}

// NewTypeScriptAdapter creates a new TypeScriptAdapter.
func NewTypeScriptAdapter() *TypeScriptAdapter {
	return &TypeScriptAdapter{}
}

// Name returns "typescript".
func (a *TypeScriptAdapter) Name() string {
	return "typescript"
}

// Extensions returns [".ts"].
func (a *TypeScriptAdapter) Extensions() []string {
	return []string{".ts"}
}

// GetLanguage returns the Tree-sitter TypeScript grammar.
func (a *TypeScriptAdapter) GetLanguage() *sitter.Language {
	return typescript.GetLanguage()
}

// GetSymbolQuery returns the Scheme query for extracting TypeScript symbols.
func (a *TypeScriptAdapter) GetSymbolQuery() []byte {
	return tsSymbolQuery
}

// GetDependencyQuery returns the Scheme query for extracting TypeScript dependencies.
func (a *TypeScriptAdapter) GetDependencyQuery() []byte {
	return tsDepsQuery
}
