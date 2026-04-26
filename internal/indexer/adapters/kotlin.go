package adapters

import (
	_ "embed"

	sitter "github.com/smacker/go-tree-sitter"
	kotlin "github.com/smacker/go-tree-sitter/kotlin"
)

//go:embed queries/kotlin_symbols.scm
var kotlinSymbolsQuery []byte

//go:embed queries/kotlin_deps.scm
var kotlinDepsQuery []byte

// KotlinAdapter implements the LanguageAdapter interface for Kotlin source files (.kt, .kts).
// It uses the Tree-sitter Kotlin grammar for parsing.
type KotlinAdapter struct{}

// NewKotlinAdapter creates a new KotlinAdapter.
func NewKotlinAdapter() *KotlinAdapter {
	return &KotlinAdapter{}
}

// Name returns "kotlin".
func (a *KotlinAdapter) Name() string {
	return "kotlin"
}

// Extensions returns [".kt", ".kts"].
func (a *KotlinAdapter) Extensions() []string {
	return []string{".kt", ".kts"}
}

// GetLanguage returns the Tree-sitter Kotlin grammar.
func (a *KotlinAdapter) GetLanguage() *sitter.Language {
	return kotlin.GetLanguage()
}

// GetSymbolQuery returns the Scheme query for extracting Kotlin symbols.
func (a *KotlinAdapter) GetSymbolQuery() []byte {
	return kotlinSymbolsQuery
}

// GetDependencyQuery returns the Scheme query for extracting Kotlin dependencies.
func (a *KotlinAdapter) GetDependencyQuery() []byte {
	return kotlinDepsQuery
}
