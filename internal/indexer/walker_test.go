package indexer

import (
	"context"
	"os"
	"path"
	"path/filepath"
	"strings"
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

// TestWalker_Walk_RootNeverIgnored guards against the whole repo being skipped
// when the root directory itself looks like an ignore pattern.
func TestWalker_Walk_RootNeverIgnored(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		rootName  string
		gitignore string
		files     []string
		want      []string
	}{
		{
			name:     "root named vendor",
			rootName: "vendor",
			files:    []string{"main.ts", "src/a.ts", "vendor/dep.ts"},
			want:     []string{"main.ts", "src/a.ts"},
		},
		{
			name:     "root named dist",
			rootName: "dist",
			files:    []string{"main.ts", "build/out.ts"},
			want:     []string{"main.ts"},
		},
		{
			name:      "dotfile pattern",
			rootName:  "repo",
			gitignore: ".*\n",
			files:     []string{"main.ts", ".hidden/a.ts", ".cache.ts", "src/b.ts"},
			want:      []string{"main.ts", "src/b.ts"},
		},
		{
			name:      "ignore all then negate",
			rootName:  "repo",
			gitignore: "*\n!keep.ts\n",
			files:     []string{"keep.ts", "drop.ts", "src/keep.ts"},
			want:      []string{"keep.ts"}, // src/ is excluded, so its contents cannot be re-included
		},
		{
			name:      "negate inside an ignored glob",
			rootName:  "repo",
			gitignore: "*.ts\n!main.ts\n",
			files:     []string{"main.ts", "util.ts", "src/main.ts", "src/x.ts"},
			want:      []string{"main.ts", "src/main.ts"},
		},
		{
			name:      "anchored pattern only matches at root",
			rootName:  "repo",
			gitignore: "/gen\n",
			files:     []string{"gen/a.ts", "src/gen/b.ts", "main.ts"},
			want:      []string{"main.ts", "src/gen/b.ts"},
		},
		{
			name:      "double star",
			rootName:  "repo",
			gitignore: "**/fixtures/**\n!src/fixtures/keep.ts\n",
			files:     []string{"a/fixtures/x.ts", "src/fixtures/keep.ts", "src/fixtures/drop.ts", "main.ts"},
			want:      []string{"main.ts", "src/fixtures/keep.ts"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			root := filepath.Join(t.TempDir(), tt.rootName)
			if tt.gitignore != "" {
				createTestFile(t, root, ".gitignore", tt.gitignore)
			}
			for _, f := range tt.files {
				createTestFile(t, root, f, "export const x = 1;")
			}

			reg := NewLanguageRegistry()
			require.NoError(t, reg.Register(&mockAdapter{name: "ts", extensions: []string{".ts"}}))

			result, err := NewWalker(reg, root).Walk(context.Background())
			require.NoError(t, err)
			var got []string
			for _, f := range result.Files {
				got = append(got, f.RelPath)
			}
			assert.ElementsMatch(t, tt.want, got)
		})
	}
}

// TestWalker_Walk_NestedGitignore checks that .gitignore files below the root
// are read and their patterns anchored to their own directory. Fails on
// master, which read only the root .gitignore.
func TestWalker_Walk_NestedGitignore(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		gitignores map[string]string // dir -> content
		files      []string
		want       []string
	}{
		{
			name:       "basename pattern in nested file",
			gitignores: map[string]string{"pkg": "*.gen.ts\n"},
			files:      []string{"a.gen.ts", "pkg/b.gen.ts", "pkg/sub/c.gen.ts", "pkg/main.ts"},
			want:       []string{"a.gen.ts", "pkg/main.ts"},
		},
		{
			name:       "anchored pattern relative to its directory",
			gitignores: map[string]string{"pkg": "/out\n"},
			files:      []string{"out/a.ts", "pkg/out/b.ts", "pkg/sub/out/c.ts"},
			want:       []string{"out/a.ts", "pkg/sub/out/c.ts"},
		},
		{
			name:       "path pattern relative to its directory",
			gitignores: map[string]string{"pkg": "sub/*.ts\n"},
			files:      []string{"sub/a.ts", "pkg/sub/b.ts", "pkg/x/sub/c.ts"},
			want:       []string{"sub/a.ts", "pkg/x/sub/c.ts"},
		},
		{
			name:       "deeper file re-includes",
			gitignores: map[string]string{"": "*.ts\n!*/\n", "pkg": "!keep.ts\n"},
			files:      []string{"a.ts", "pkg/keep.ts", "pkg/drop.ts"},
			want:       []string{"pkg/keep.ts"},
		},
		{
			name:       "deeper file excludes what root re-included",
			gitignores: map[string]string{"": "!x.ts\n", "pkg": "x.ts\n"},
			files:      []string{"x.ts", "pkg/x.ts"},
			want:       []string{"x.ts"},
		},
		{
			name:       "nested dir-only rule prunes directory",
			gitignores: map[string]string{"a/b": "tmp/\n"},
			files:      []string{"a/b/tmp/x.ts", "a/tmp/y.ts", "a/b/z.ts"},
			want:       []string{"a/tmp/y.ts", "a/b/z.ts"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			for dir, content := range tt.gitignores {
				createTestFile(t, root, path.Join(dir, ".gitignore"), content)
			}
			for _, f := range tt.files {
				createTestFile(t, root, f, "export const x = 1;")
			}

			reg := NewLanguageRegistry()
			require.NoError(t, reg.Register(&mockAdapter{name: "ts", extensions: []string{".ts"}}))

			result, err := NewWalker(reg, root).Walk(context.Background())
			require.NoError(t, err)
			var got []string
			for _, f := range result.Files {
				got = append(got, f.RelPath)
			}
			assert.ElementsMatch(t, tt.want, got)
		})
	}
}

// TestDirFilter_NestedGitignore checks that the watcher's filter applies
// nested .gitignore files like the walker. Fails on master.
func TestDirFilter_NestedGitignore(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	createTestFile(t, root, "pkg/.gitignore", "/gen/\n")
	for _, d := range []string{"pkg/gen", "pkg/sub/gen", "gen"} {
		require.NoError(t, os.MkdirAll(filepath.Join(root, filepath.FromSlash(d)), 0o755))
	}

	f := NewDirFilter(root)
	assert.True(t, f.Skip(filepath.Join(root, "pkg", "gen")))
	assert.False(t, f.Skip(filepath.Join(root, "pkg", "sub", "gen")))
	assert.False(t, f.Skip(filepath.Join(root, "gen")))
	assert.False(t, f.Skip(filepath.Join(root, "pkg")))
}

// symlinkOrSkip creates a symlink or skips the test where the OS refuses
// (Windows without developer mode).
func symlinkOrSkip(t *testing.T, target, link string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(link), 0o755))
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks not supported here: %v", err)
	}
}

// TestWalker_Walk_Symlinks checks that symlinked files are confined to the
// root and size-limited by their target. Fails on master, which indexed the
// outside and oversized targets.
func TestWalker_Walk_Symlinks(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	root := filepath.Join(base, "repo")
	outside := filepath.Join(base, "outside")
	createTestFile(t, root, "real.ts", "export const real = 1;")
	createTestFile(t, root, "big.txt", strings.Repeat("x", maxFileSize+1))
	createTestFile(t, outside, "secret.ts", "export const secret = 1;")
	require.NoError(t, os.MkdirAll(filepath.Join(root, "dir.ts"), 0o755))

	symlinkOrSkip(t, filepath.Join(root, "real.ts"), filepath.Join(root, "link_inside.ts"))
	symlinkOrSkip(t, filepath.Join(outside, "secret.ts"), filepath.Join(root, "link_outside.ts"))
	symlinkOrSkip(t, filepath.Join(root, "big.txt"), filepath.Join(root, "link_big.ts"))
	symlinkOrSkip(t, filepath.Join(root, "missing.ts"), filepath.Join(root, "link_dangling.ts"))
	symlinkOrSkip(t, filepath.Join(root, "dir.ts"), filepath.Join(root, "link_dir.ts"))

	reg := NewLanguageRegistry()
	require.NoError(t, reg.Register(&mockAdapter{name: "ts", extensions: []string{".ts"}}))

	result, err := NewWalker(reg, root).Walk(context.Background())
	require.NoError(t, err)

	got := make(map[string]DiscoveredFile)
	for _, f := range result.Files {
		got[f.RelPath] = f
	}
	assert.ElementsMatch(t, []string{"real.ts", "link_inside.ts"}, keys(got))
	inside := got["link_inside.ts"]
	assert.Equal(t, got["real.ts"].ContentHash, inside.ContentHash)
	assert.Equal(t, got["real.ts"].SizeBytes, inside.SizeBytes, "size is the target's, not the link's")
}

func TestInsideRoot(t *testing.T) {
	t.Parallel()

	root := filepath.Join(t.TempDir(), "repo")
	tests := []struct {
		name string
		p    string
		want bool
	}{
		{"root itself", root, true},
		{"child", filepath.Join(root, "a.ts"), true},
		{"nested", filepath.Join(root, "a", "b.ts"), true},
		{"dotdot-prefixed name inside", filepath.Join(root, "..x.ts"), true},
		{"parent", filepath.Dir(root), false},
		{"sibling", filepath.Join(filepath.Dir(root), "other", "a.ts"), false},
		{"sibling with root as prefix", root + "2" + string(filepath.Separator) + "a.ts", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, insideRoot(root, tt.p))
		})
	}
}

func keys(m map[string]DiscoveredFile) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestIgnoreRules_Match(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		patterns string
		path     string
		isDir    bool
		want     bool
	}{
		{"basename at depth", "node_modules", "src/node_modules", true, true},
		{"glob basename", "*.test.ts", "src/main.test.ts", false, true},
		{"glob basename no match", "*.test.ts", "src/main.ts", false, false},
		{"dir-only matches dir", "dist/", "dist", true, true},
		{"dir-only skips file", "dist/", "dist", false, false},
		{"negation re-includes", "*.ts\n!important.ts", "important.ts", false, false},
		{"last match wins", "!a.ts\n*.ts", "a.ts", false, true},
		{"path pattern", "src/*.ts", "src/main.ts", false, true},
		{"star does not cross slash", "src/*.ts", "src/x/main.ts", false, false},
		{"anchored at root", "/build", "build", true, true},
		{"anchored not nested", "/build", "src/build", true, false},
		{"leading double star", "**/logs", "a/b/logs", true, true},
		{"middle double star zero dirs", "a/**/b.ts", "a/b.ts", false, true},
		{"middle double star many dirs", "a/**/b.ts", "a/x/y/b.ts", false, true},
		{"trailing double star excludes contents", "a/**", "a/x.ts", false, true},
		{"trailing double star keeps the dir itself", "a/**", "a", true, false},
		{"escaped bang is literal", `\!x.ts`, "!x.ts", false, true},
		{"comment ignored", "# *.ts", "a.ts", false, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			rules := parseIgnoreRules(strings.NewReader(tt.patterns))
			assert.Equal(t, tt.want, rules.match(tt.path, tt.isDir), "%q vs %q", tt.patterns, tt.path)
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
