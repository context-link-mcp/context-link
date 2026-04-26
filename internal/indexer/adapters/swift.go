package adapters

import (
	_ "embed"

	sitter "github.com/smacker/go-tree-sitter"
	swift "github.com/smacker/go-tree-sitter/swift"
)

//go:embed queries/swift_symbols.scm
var swiftSymbolsQuery []byte

//go:embed queries/swift_deps.scm
var swiftDepsQuery []byte

// SwiftAdapter implements the LanguageAdapter interface for Swift source files (.swift).
// It uses the Tree-sitter Swift grammar for parsing.
type SwiftAdapter struct{}

// NewSwiftAdapter creates a new SwiftAdapter.
func NewSwiftAdapter() *SwiftAdapter {
	return &SwiftAdapter{}
}

// Name returns "swift".
func (a *SwiftAdapter) Name() string {
	return "swift"
}

// Extensions returns [".swift"].
func (a *SwiftAdapter) Extensions() []string {
	return []string{".swift"}
}

// GetLanguage returns the Tree-sitter Swift grammar.
func (a *SwiftAdapter) GetLanguage() *sitter.Language {
	return swift.GetLanguage()
}

// GetSymbolQuery returns the Scheme query for extracting Swift symbols.
func (a *SwiftAdapter) GetSymbolQuery() []byte {
	return swiftSymbolsQuery
}

// GetDependencyQuery returns the Scheme query for extracting Swift dependencies.
func (a *SwiftAdapter) GetDependencyQuery() []byte {
	return swiftDepsQuery
}
