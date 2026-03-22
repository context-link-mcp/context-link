package adapters_test

import (
	"testing"

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

// TestAdapterNamesAreUnique verifies no two adapters share a name.
func TestAdapterNamesAreUnique(t *testing.T) {
	t.Parallel()
	all := []interface{ Name() string }{
		adapters.NewTypeScriptAdapter(),
		adapters.NewTSXAdapter(),
		adapters.NewGoAdapter(),
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
