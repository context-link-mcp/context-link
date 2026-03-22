package adapters

import (
	_ "embed"

	sitter "github.com/smacker/go-tree-sitter"
	cpp "github.com/smacker/go-tree-sitter/cpp"
)

//go:embed queries/cpp_symbols.scm
var cppSymbolsQuery []byte

//go:embed queries/cpp_deps.scm
var cppDepsQuery []byte

// CppAdapter implements the LanguageAdapter interface for C++ source files
// (.cpp, .hpp, .cc, .cxx, .hxx, .hh).
// It uses the Tree-sitter C++ grammar for parsing.
type CppAdapter struct{}

// NewCppAdapter creates a new CppAdapter.
func NewCppAdapter() *CppAdapter {
	return &CppAdapter{}
}

// Name returns "cpp".
func (a *CppAdapter) Name() string {
	return "cpp"
}

// Extensions returns C++ file extensions.
func (a *CppAdapter) Extensions() []string {
	return []string{".cpp", ".hpp", ".cc", ".cxx", ".hxx", ".hh"}
}

// GetLanguage returns the Tree-sitter C++ grammar.
func (a *CppAdapter) GetLanguage() *sitter.Language {
	return cpp.GetLanguage()
}

// GetSymbolQuery returns the Scheme query for extracting C++ symbols.
func (a *CppAdapter) GetSymbolQuery() []byte {
	return cppSymbolsQuery
}

// GetDependencyQuery returns the Scheme query for extracting C++ dependencies.
func (a *CppAdapter) GetDependencyQuery() []byte {
	return cppDepsQuery
}
