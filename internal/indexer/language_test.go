package indexer

import (
	"testing"

	sitter "github.com/smacker/go-tree-sitter"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockAdapter implements LanguageAdapter for testing without CGo.
type mockAdapter struct {
	name       string
	extensions []string
}

func (m *mockAdapter) Name() string                { return m.name }
func (m *mockAdapter) Extensions() []string        { return m.extensions }
func (m *mockAdapter) GetLanguage() *sitter.Language { return nil }
func (m *mockAdapter) GetSymbolQuery() []byte      { return []byte("") }
func (m *mockAdapter) GetDependencyQuery() []byte  { return []byte("") }

func TestNewLanguageRegistry(t *testing.T) {
	t.Parallel()
	reg := NewLanguageRegistry()
	require.NotNil(t, reg)
	assert.Empty(t, reg.Extensions())
}

func TestLanguageRegistry_Register(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		adapters  []*mockAdapter
		wantError bool
	}{
		{
			name: "register single adapter",
			adapters: []*mockAdapter{
				{name: "typescript", extensions: []string{".ts"}},
			},
			wantError: false,
		},
		{
			name: "register multiple adapters",
			adapters: []*mockAdapter{
				{name: "typescript", extensions: []string{".ts"}},
				{name: "tsx", extensions: []string{".tsx", ".jsx"}},
			},
			wantError: false,
		},
		{
			name: "duplicate extension fails",
			adapters: []*mockAdapter{
				{name: "typescript", extensions: []string{".ts"}},
				{name: "other", extensions: []string{".ts"}},
			},
			wantError: true,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			reg := NewLanguageRegistry()

			var lastErr error
			for _, a := range tt.adapters {
				lastErr = reg.Register(a)
			}

			if tt.wantError {
				assert.Error(t, lastErr)
			} else {
				assert.NoError(t, lastErr)
			}
		})
	}
}

func TestLanguageRegistry_GetAdapter(t *testing.T) {
	t.Parallel()

	reg := NewLanguageRegistry()
	tsAdapter := &mockAdapter{name: "typescript", extensions: []string{".ts"}}
	require.NoError(t, reg.Register(tsAdapter))

	t.Run("found", func(t *testing.T) {
		t.Parallel()
		adapter, ok := reg.GetAdapter(".ts")
		assert.True(t, ok)
		assert.Equal(t, "typescript", adapter.Name())
	})

	t.Run("not found", func(t *testing.T) {
		t.Parallel()
		_, ok := reg.GetAdapter(".go")
		assert.False(t, ok)
	})
}

func TestLanguageRegistry_Extensions(t *testing.T) {
	t.Parallel()

	reg := NewLanguageRegistry()
	require.NoError(t, reg.Register(&mockAdapter{name: "ts", extensions: []string{".ts"}}))
	require.NoError(t, reg.Register(&mockAdapter{name: "tsx", extensions: []string{".tsx", ".jsx"}}))

	exts := reg.Extensions()
	assert.Len(t, exts, 3)
	assert.Contains(t, exts, ".ts")
	assert.Contains(t, exts, ".tsx")
	assert.Contains(t, exts, ".jsx")
}
