package watcher

import (
	"testing"

	"github.com/context-link-mcp/context-link/internal/indexer"
	"github.com/context-link-mcp/context-link/internal/indexer/adapters"
	"github.com/stretchr/testify/assert"
)

func buildTestRegistry() *indexer.LanguageRegistry {
	reg := indexer.NewLanguageRegistry()
	_ = reg.Register(adapters.NewGoAdapter())
	_ = reg.Register(adapters.NewTypeScriptAdapter())
	_ = reg.Register(adapters.NewPythonAdapter())
	return reg
}

func TestIsRelevantFile(t *testing.T) {
	t.Parallel()
	reg := buildTestRegistry()

	tests := []struct {
		path     string
		expected bool
	}{
		{"main.go", true},
		{"src/app.ts", true},
		{"script.py", true},
		{"readme.md", false},
		{"image.png", false},
		{"noext", false},
		{"Makefile", false},
		{".gitignore", false},
		{"data.json", false},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.path, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.expected, isRelevantFile(tc.path, reg))
		})
	}
}

func TestSkipDirs(t *testing.T) {
	t.Parallel()

	assert.True(t, skipDirs["node_modules"])
	assert.True(t, skipDirs[".git"])
	assert.True(t, skipDirs["vendor"])
	assert.True(t, skipDirs["__pycache__"])
	assert.True(t, skipDirs["target"])
	assert.False(t, skipDirs["src"])
	assert.False(t, skipDirs["internal"])
}
