package adapters

import (
	_ "embed"

	sitter "github.com/smacker/go-tree-sitter"
	csharp "github.com/smacker/go-tree-sitter/csharp"
)

//go:embed queries/csharp_symbols.scm
var csharpSymbolsQuery []byte

//go:embed queries/csharp_deps.scm
var csharpDepsQuery []byte

// CSharpAdapter implements the LanguageAdapter interface for C# source files (.cs).
// It uses the Tree-sitter C# grammar for parsing.
type CSharpAdapter struct{}

// NewCSharpAdapter creates a new CSharpAdapter.
func NewCSharpAdapter() *CSharpAdapter {
	return &CSharpAdapter{}
}

// Name returns "csharp".
func (a *CSharpAdapter) Name() string {
	return "csharp"
}

// Extensions returns [".cs"].
func (a *CSharpAdapter) Extensions() []string {
	return []string{".cs"}
}

// GetLanguage returns the Tree-sitter C# grammar.
func (a *CSharpAdapter) GetLanguage() *sitter.Language {
	return csharp.GetLanguage()
}

// GetSymbolQuery returns the Scheme query for extracting C# symbols.
func (a *CSharpAdapter) GetSymbolQuery() []byte {
	return csharpSymbolsQuery
}

// GetDependencyQuery returns the Scheme query for extracting C# dependencies.
func (a *CSharpAdapter) GetDependencyQuery() []byte {
	return csharpDepsQuery
}
