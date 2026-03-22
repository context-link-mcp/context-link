package adapters

import (
	_ "embed"

	sitter "github.com/smacker/go-tree-sitter"
	python "github.com/smacker/go-tree-sitter/python"
)

//go:embed queries/python_symbols.scm
var pythonSymbolsQuery []byte

//go:embed queries/python_deps.scm
var pythonDepsQuery []byte

// PythonAdapter implements the LanguageAdapter interface for Python source files (.py).
// It uses the Tree-sitter Python grammar for parsing.
type PythonAdapter struct{}

// NewPythonAdapter creates a new PythonAdapter.
func NewPythonAdapter() *PythonAdapter {
	return &PythonAdapter{}
}

// Name returns "python".
func (a *PythonAdapter) Name() string {
	return "python"
}

// Extensions returns [".py"].
func (a *PythonAdapter) Extensions() []string {
	return []string{".py"}
}

// GetLanguage returns the Tree-sitter Python grammar.
func (a *PythonAdapter) GetLanguage() *sitter.Language {
	return python.GetLanguage()
}

// GetSymbolQuery returns the Scheme query for extracting Python symbols.
func (a *PythonAdapter) GetSymbolQuery() []byte {
	return pythonSymbolsQuery
}

// GetDependencyQuery returns the Scheme query for extracting Python dependencies.
func (a *PythonAdapter) GetDependencyQuery() []byte {
	return pythonDepsQuery
}
