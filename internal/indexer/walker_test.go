package indexer

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWalker_Walk(t *testing.T) {
	t.Parallel()

	// Create a temp directory structure.
	root := t.TempDir()
	createTestFile(t, root, "src/main.ts", `export function main() {}`)
	createTestFile(t, root, "src/utils.ts", `export const add = (a: number, b: number) => a + b;`)
	createTestFile(t, root, "src/App.tsx", `export const App = () => <div>Hello</div>;`)
	createTestFile(t, root, "node_modules/lib/index.ts", `export const lib = 1;`)
	createTestFile(t, root, "dist/bundle.js", `// bundled`)
	createTestFile(t, root, "README.md", `# Readme`)

	reg := NewLanguageRegistry()
	require.NoError(t, reg.Register(&mockAdapter{name: "ts", extensions: []string{".ts"}}))
	require.NoError(t, reg.Register(&mockAdapter{name: "tsx", extensions: []string{".tsx"}}))

	walker := NewWalker(reg, root)
	result, err := walker.Walk(context.Background())
	require.NoError(t, err)

	// Should find 3 files: main.ts, utils.ts, App.tsx
	// Should skip: node_modules/, dist/, README.md
	assert.Len(t, result.Files, 3)

	relPaths := make([]string, len(result.Files))
	for i, f := range result.Files {
		relPaths[i] = f.RelPath
	}
	assert.Contains(t, relPaths, "src/main.ts")
	assert.Contains(t, relPaths, "src/utils.ts")
	assert.Contains(t, relPaths, "src/App.tsx")
}

func TestWalker_Walk_GitignoreSupport(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	createTestFile(t, root, ".gitignore", "generated/\n*.test.ts\n")
	createTestFile(t, root, "src/main.ts", `export function main() {}`)
	createTestFile(t, root, "src/main.test.ts", `test('main', () => {});`)
	createTestFile(t, root, "generated/output.ts", `// generated`)

	reg := NewLanguageRegistry()
	require.NoError(t, reg.Register(&mockAdapter{name: "ts", extensions: []string{".ts"}}))

	walker := NewWalker(reg, root)
	result, err := walker.Walk(context.Background())
	require.NoError(t, err)

	// Should only find main.ts — generated/ dir and *.test.ts are gitignored.
	assert.Len(t, result.Files, 1)
	assert.Equal(t, "src/main.ts", result.Files[0].RelPath)
}

func TestWalker_Walk_LargeFileSkipped(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	// Create a file larger than maxFileSize (1 MB).
	largePath := filepath.Join(root, "large.ts")
	data := make([]byte, maxFileSize+1)
	for i := range data {
		data[i] = 'x'
	}
	require.NoError(t, os.WriteFile(largePath, data, 0o644))

	createTestFile(t, root, "small.ts", `export const x = 1;`)

	reg := NewLanguageRegistry()
	require.NoError(t, reg.Register(&mockAdapter{name: "ts", extensions: []string{".ts"}}))

	walker := NewWalker(reg, root)
	result, err := walker.Walk(context.Background())
	require.NoError(t, err)

	// Only small.ts should be found.
	assert.Len(t, result.Files, 1)
	assert.Equal(t, "small.ts", result.Files[0].RelPath)
}

func TestWalker_Walk_ContentHash(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	createTestFile(t, root, "a.ts", `const a = 1;`)
	createTestFile(t, root, "b.ts", `const a = 1;`) // same content

	reg := NewLanguageRegistry()
	require.NoError(t, reg.Register(&mockAdapter{name: "ts", extensions: []string{".ts"}}))

	walker := NewWalker(reg, root)
	result, err := walker.Walk(context.Background())
	require.NoError(t, err)

	require.Len(t, result.Files, 2)
	// Same content → same hash.
	assert.Equal(t, result.Files[0].ContentHash, result.Files[1].ContentHash)
	assert.NotEmpty(t, result.Files[0].ContentHash)
}

func TestWalker_Walk_ContextCancellation(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	createTestFile(t, root, "a.ts", `const a = 1;`)

	reg := NewLanguageRegistry()
	require.NoError(t, reg.Register(&mockAdapter{name: "ts", extensions: []string{".ts"}}))

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	walker := NewWalker(reg, root)
	_, err := walker.Walk(ctx)
	assert.Error(t, err)
}

func TestWalker_ToModelFiles(t *testing.T) {
	t.Parallel()

	reg := NewLanguageRegistry()
	walker := NewWalker(reg, "/tmp")

	files := []DiscoveredFile{
		{Path: "/tmp/src/a.ts", RelPath: "src/a.ts", ContentHash: "abc123", SizeBytes: 100, Extension: ".ts"},
	}

	models := walker.ToModelFiles(files, "myrepo")
	require.Len(t, models, 1)
	assert.Equal(t, "myrepo", models[0].RepoName)
	assert.Equal(t, "src/a.ts", models[0].Path)
	assert.Equal(t, "abc123", models[0].ContentHash)
}

func TestMatchPattern(t *testing.T) {
	t.Parallel()

	tests := []struct {
		pattern string
		path    string
		want    bool
	}{
		{"node_modules", "node_modules/foo/bar.ts", true},
		{"node_modules", "src/node_modules/bar.ts", true},
		{"*.test.ts", "src/main.test.ts", true},
		{"*.test.ts", "src/main.ts", false},
		{"dist/", "dist/bundle.js", true},
		{"dist/", "src/dist.ts", false},
		{"!important.ts", "important.ts", false}, // negation not applied
		{"src/*.ts", "src/main.ts", true},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.pattern+"_"+tt.path, func(t *testing.T) {
			t.Parallel()
			got := matchPattern(tt.pattern, tt.path)
			assert.Equal(t, tt.want, got, "matchPattern(%q, %q)", tt.pattern, tt.path)
		})
	}
}

// createTestFile creates a file with the given content in the temp directory.
func createTestFile(t *testing.T, root, relPath, content string) {
	t.Helper()
	fullPath := filepath.Join(root, filepath.FromSlash(relPath))
	require.NoError(t, os.MkdirAll(filepath.Dir(fullPath), 0o755))
	require.NoError(t, os.WriteFile(fullPath, []byte(content), 0o644))
}
