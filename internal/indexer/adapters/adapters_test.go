package adapters_test

import (
	"testing"

	sitter "github.com/smacker/go-tree-sitter"

	"github.com/context-link-mcp/context-link/internal/indexer/adapters"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTypeScriptAdapter(t *testing.T) {
	t.Parallel()
	a := adapters.NewTypeScriptAdapter()

	assert.Equal(t, "typescript", a.Name())
	assert.Equal(t, []string{".ts"}, a.Extensions())
	require.NotNil(t, a.GetLanguage(), "GetLanguage must return a non-nil grammar")
	assert.NotEmpty(t, a.GetSymbolQuery(), "symbol query must not be empty")
	assert.NotEmpty(t, a.GetDependencyQuery(), "dependency query must not be empty")
}

func TestTSXAdapter(t *testing.T) {
	t.Parallel()
	a := adapters.NewTSXAdapter()

	assert.Equal(t, "tsx", a.Name())
	assert.Equal(t, []string{".tsx", ".jsx"}, a.Extensions())
	require.NotNil(t, a.GetLanguage(), "GetLanguage must return a non-nil grammar")
	assert.NotEmpty(t, a.GetSymbolQuery(), "symbol query must not be empty")
	assert.NotEmpty(t, a.GetDependencyQuery(), "dependency query must not be empty")
}

func TestGoAdapter(t *testing.T) {
	t.Parallel()
	a := adapters.NewGoAdapter()

	assert.Equal(t, "go", a.Name())
	assert.Equal(t, []string{".go"}, a.Extensions())
	require.NotNil(t, a.GetLanguage(), "GetLanguage must return a non-nil grammar")
	assert.NotEmpty(t, a.GetSymbolQuery(), "symbol query must not be empty")
	assert.NotEmpty(t, a.GetDependencyQuery(), "dependency query must not be empty")
}

func TestPythonAdapter(t *testing.T) {
	t.Parallel()
	a := adapters.NewPythonAdapter()

	assert.Equal(t, "python", a.Name())
	assert.Equal(t, []string{".py"}, a.Extensions())
	require.NotNil(t, a.GetLanguage(), "GetLanguage must return a non-nil grammar")
	assert.NotEmpty(t, a.GetSymbolQuery(), "symbol query must not be empty")
	assert.NotEmpty(t, a.GetDependencyQuery(), "dependency query must not be empty")
}

func TestJavaScriptAdapter(t *testing.T) {
	t.Parallel()
	a := adapters.NewJavaScriptAdapter()

	assert.Equal(t, "javascript", a.Name())
	assert.Equal(t, []string{".js", ".mjs"}, a.Extensions())
	require.NotNil(t, a.GetLanguage(), "GetLanguage must return a non-nil grammar")
	assert.NotEmpty(t, a.GetSymbolQuery(), "symbol query must not be empty")
	assert.NotEmpty(t, a.GetDependencyQuery(), "dependency query must not be empty")
}

func TestRustAdapter(t *testing.T) {
	t.Parallel()
	a := adapters.NewRustAdapter()

	assert.Equal(t, "rust", a.Name())
	assert.Equal(t, []string{".rs"}, a.Extensions())
	require.NotNil(t, a.GetLanguage(), "GetLanguage must return a non-nil grammar")
	assert.NotEmpty(t, a.GetSymbolQuery(), "symbol query must not be empty")
	assert.NotEmpty(t, a.GetDependencyQuery(), "dependency query must not be empty")
}

func TestJavaAdapter(t *testing.T) {
	t.Parallel()
	a := adapters.NewJavaAdapter()

	assert.Equal(t, "java", a.Name())
	assert.Equal(t, []string{".java"}, a.Extensions())
	require.NotNil(t, a.GetLanguage(), "GetLanguage must return a non-nil grammar")
	assert.NotEmpty(t, a.GetSymbolQuery(), "symbol query must not be empty")
	assert.NotEmpty(t, a.GetDependencyQuery(), "dependency query must not be empty")
}

func TestCAdapter(t *testing.T) {
	t.Parallel()
	a := adapters.NewCAdapter()

	assert.Equal(t, "c", a.Name())
	assert.Equal(t, []string{".c", ".h"}, a.Extensions())
	require.NotNil(t, a.GetLanguage(), "GetLanguage must return a non-nil grammar")
	assert.NotEmpty(t, a.GetSymbolQuery(), "symbol query must not be empty")
	assert.NotEmpty(t, a.GetDependencyQuery(), "dependency query must not be empty")
}

func TestCppAdapter(t *testing.T) {
	t.Parallel()
	a := adapters.NewCppAdapter()

	assert.Equal(t, "cpp", a.Name())
	assert.Equal(t, []string{".cpp", ".hpp", ".cc", ".cxx", ".hxx", ".hh"}, a.Extensions())
	require.NotNil(t, a.GetLanguage(), "GetLanguage must return a non-nil grammar")
	assert.NotEmpty(t, a.GetSymbolQuery(), "symbol query must not be empty")
	assert.NotEmpty(t, a.GetDependencyQuery(), "dependency query must not be empty")
}

func TestCSharpAdapter(t *testing.T) {
	t.Parallel()
	a := adapters.NewCSharpAdapter()

	assert.Equal(t, "csharp", a.Name())
	assert.Equal(t, []string{".cs"}, a.Extensions())
	require.NotNil(t, a.GetLanguage(), "GetLanguage must return a non-nil grammar")
	assert.NotEmpty(t, a.GetSymbolQuery(), "symbol query must not be empty")
	assert.NotEmpty(t, a.GetDependencyQuery(), "dependency query must not be empty")
}

// TestAdapterQueriesCompile verifies all .scm queries compile against their grammars.
func TestAdapterQueriesCompile(t *testing.T) {
	t.Parallel()

	type adapter interface {
		Name() string
		GetLanguage() *sitter.Language
		GetSymbolQuery() []byte
		GetDependencyQuery() []byte
	}

	all := []adapter{
		adapters.NewTypeScriptAdapter(),
		adapters.NewTSXAdapter(),
		adapters.NewGoAdapter(),
		adapters.NewPythonAdapter(),
		adapters.NewJavaScriptAdapter(),
		adapters.NewRustAdapter(),
		adapters.NewJavaAdapter(),
		adapters.NewCAdapter(),
		adapters.NewCppAdapter(),
		adapters.NewCSharpAdapter(),
	}

	for _, a := range all {
		a := a
		t.Run(a.Name()+"_symbols", func(t *testing.T) {
			t.Parallel()
			q, err := sitter.NewQuery(a.GetSymbolQuery(), a.GetLanguage())
			require.NoError(t, err, "symbol query must compile for %s", a.Name())
			require.NotNil(t, q)
		})
		t.Run(a.Name()+"_deps", func(t *testing.T) {
			t.Parallel()
			q, err := sitter.NewQuery(a.GetDependencyQuery(), a.GetLanguage())
			require.NoError(t, err, "dependency query must compile for %s", a.Name())
			require.NotNil(t, q)
		})
	}
}

// TestAdapterNamesAreUnique verifies no two adapters share a name.
func TestAdapterNamesAreUnique(t *testing.T) {
	t.Parallel()
	all := []interface{ Name() string }{
		adapters.NewTypeScriptAdapter(),
		adapters.NewTSXAdapter(),
		adapters.NewGoAdapter(),
		adapters.NewPythonAdapter(),
		adapters.NewJavaScriptAdapter(),
		adapters.NewRustAdapter(),
		adapters.NewJavaAdapter(),
		adapters.NewCAdapter(),
		adapters.NewCppAdapter(),
		adapters.NewCSharpAdapter(),
	}
	seen := make(map[string]bool)
	for _, a := range all {
		name := a.Name()
		assert.False(t, seen[name], "duplicate adapter name: %s", name)
		seen[name] = true
	}
}

// TestAdapterExtensionsAreUnique verifies no two adapters share an extension.
func TestAdapterExtensionsAreUnique(t *testing.T) {
	t.Parallel()
	type extProvider interface {
		Extensions() []string
		Name() string
	}
	all := []extProvider{
		adapters.NewTypeScriptAdapter(),
		adapters.NewTSXAdapter(),
		adapters.NewGoAdapter(),
		adapters.NewPythonAdapter(),
		adapters.NewJavaScriptAdapter(),
		adapters.NewRustAdapter(),
		adapters.NewJavaAdapter(),
		adapters.NewCAdapter(),
		adapters.NewCppAdapter(),
		adapters.NewCSharpAdapter(),
	}
	seen := make(map[string]string)
	for _, a := range all {
		for _, ext := range a.Extensions() {
			owner, exists := seen[ext]
			assert.False(t, exists, "extension %s claimed by both %s and %s", ext, owner, a.Name())
			seen[ext] = a.Name()
		}
	}
}
