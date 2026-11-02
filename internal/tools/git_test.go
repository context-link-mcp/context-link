package tools

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/context-link-mcp/context-link/internal/store"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// requireGit skips the test when the git binary is not on PATH.
func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found on PATH")
	}
}

// gitOutput runs a git command in dir and returns its trimmed stdout.
func gitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	require.NoError(t, err, "git %v", args)
	return string(out)
}

// modifiedSymbolsResponse mirrors the JSON returned by get_modified_symbols.
type modifiedSymbolsResponse struct {
	BaseRef      string `json:"base_ref"`
	FilesChanged int    `json:"files_changed"`
	Symbols      []struct {
		Name         string `json:"name"`
		FilePath     string `json:"file_path"`
		ChangeType   string `json:"change_type"`
		ChangedLines []int  `json:"changed_lines"`
	} `json:"symbols"`
}

// callModifiedSymbols invokes the get_modified_symbols handler and returns the
// decoded response, or the error text when the tool reports an error.
func callModifiedSymbols(t *testing.T, db *store.DB, root string, args map[string]any) (modifiedSymbolsResponse, string) {
	t.Helper()
	handler := modifiedSymbolsHandler(db, "repo", root, NewSessionTokenTracker())
	req := mcp.CallToolRequest{}
	req.Params.Arguments = args

	result, err := handler(context.Background(), req)
	require.NoError(t, err)
	raw, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)

	var resp modifiedSymbolsResponse
	if result.IsError {
		return resp, raw.Text
	}
	require.NoError(t, json.Unmarshal([]byte(raw.Text), &resp))
	return resp, ""
}

func symbolNames(resp modifiedSymbolsResponse) []string {
	names := make([]string, 0, len(resp.Symbols))
	for _, s := range resp.Symbols {
		names = append(names, s.Name)
	}
	return names
}

func TestModifiedSymbols_RejectsOptionLikeBaseRef(t *testing.T) {
	t.Parallel()
	requireGit(t)

	tests := []struct {
		name    string
		baseRef func(outDir string) string
	}{
		{"output option", func(d string) string { return "--output=" + filepath.Join(d, "pwned.txt") }},
		{"short option", func(string) string { return "-p" }},
		{"end of options", func(string) string { return "--end-of-options" }},
		{"option after whitespace", func(d string) string { return " --output=" + filepath.Join(d, "pwned.txt") }},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			db := openToolTestDB(t)
			gitDir := setupGitRepo(t)
			// A pending change guarantees git diff would produce output to write.
			writeFile(t, gitDir, "test.go", "package main\n\nfunc main() {\n\tprintln(\"x\")\n}\n")
			outDir := t.TempDir()

			_, errText := callModifiedSymbols(t, db, gitDir, map[string]any{"base_ref": tc.baseRef(outDir)})
			assert.Contains(t, errText, "invalid base_ref")

			_, statErr := os.Stat(filepath.Join(outDir, "pwned.txt"))
			assert.True(t, os.IsNotExist(statErr), "git must not write the --output file")
		})
	}
}

func TestModifiedSymbols_SubfolderProjectRoot(t *testing.T) {
	t.Parallel()
	requireGit(t)
	db := openToolTestDB(t)

	gitDir := setupGitRepo(t)
	require.NoError(t, os.MkdirAll(filepath.Join(gitDir, "sub"), 0o755))
	writeFile(t, gitDir, "sub/pkg.go", "package sub\n\nfunc Sub() {\n\tprintln(\"a\")\n}\n")
	runGit(t, gitDir, "add", ".")
	runGit(t, gitDir, "commit", "-m", "add sub")

	// Index paths are relative to the project root (gitDir/sub).
	insertSymbolWithLines(t, db, "repo", "Sub", "function", "func Sub() {}", "pkg.go", 3, 5)

	writeFile(t, gitDir, "sub/pkg.go", "package sub\n\nfunc Sub() {\n\tprintln(\"b\")\n}\n")
	// A change outside the project root must be ignored.
	writeFile(t, gitDir, "test.go", "package main\n\nfunc main() {\n\tprintln(\"outside\")\n}\n")
	writeFile(t, gitDir, "outside_untracked.go", "package main\n")

	for _, staged := range []bool{true, false} {
		resp, errText := callModifiedSymbols(t, db, filepath.Join(gitDir, "sub"), map[string]any{"include_staged": staged})
		require.Empty(t, errText)
		assert.Equal(t, 1, resp.FilesChanged, "include_staged=%v", staged)
		require.Len(t, resp.Symbols, 1, "include_staged=%v", staged)
		assert.Equal(t, "Sub", resp.Symbols[0].Name)
		assert.Equal(t, "modified", resp.Symbols[0].ChangeType)
		assert.Equal(t, []int{4}, resp.Symbols[0].ChangedLines)
	}
}

func TestModifiedSymbols_ExcludeStagedHEADDiffsWorkingTree(t *testing.T) {
	t.Parallel()
	requireGit(t)
	db := openToolTestDB(t)

	gitDir := setupGitRepo(t)
	writeFile(t, gitDir, "two.go", "package main\n\nfunc staged() {\n\tprintln(1)\n}\n\nfunc unstaged() {\n\tprintln(2)\n}\n")
	runGit(t, gitDir, "add", ".")
	runGit(t, gitDir, "commit", "-m", "two")
	insertSymbolWithLines(t, db, "repo", "staged", "function", "func staged() {}", "two.go", 3, 5)
	insertSymbolWithLines(t, db, "repo", "unstaged", "function", "func unstaged() {}", "two.go", 7, 9)

	// Stage a change to staged(), then make an unstaged change to unstaged().
	writeFile(t, gitDir, "two.go", "package main\n\nfunc staged() {\n\tprintln(10)\n}\n\nfunc unstaged() {\n\tprintln(2)\n}\n")
	runGit(t, gitDir, "add", "two.go")
	writeFile(t, gitDir, "two.go", "package main\n\nfunc staged() {\n\tprintln(10)\n}\n\nfunc unstaged() {\n\tprintln(20)\n}\n")

	tests := []struct {
		name   string
		staged bool
		want   []string
	}{
		{"unstaged only", false, []string{"unstaged"}},
		{"staged and unstaged", true, []string{"staged", "unstaged"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			resp, errText := callModifiedSymbols(t, db, gitDir, map[string]any{"include_staged": tc.staged})
			require.Empty(t, errText)
			assert.ElementsMatch(t, tc.want, symbolNames(resp))
		})
	}
}

func TestModifiedSymbols_UntrackedFileIsFullyAdded(t *testing.T) {
	t.Parallel()
	requireGit(t)
	db := openToolTestDB(t)

	gitDir := setupGitRepo(t)
	writeFile(t, gitDir, "untracked.go", "package main\n\nfunc a() {}\n\nfunc b() {\n}\n")
	insertSymbolWithLines(t, db, "repo", "a", "function", "func a() {}", "untracked.go", 3, 3)
	insertSymbolWithLines(t, db, "repo", "b", "function", "func b() {}", "untracked.go", 5, 6)

	for _, args := range []map[string]any{
		{"include_staged": false},
		{"include_staged": true},
		{"base_ref": gitOutput(t, gitDir, "rev-parse", "HEAD")[:12]},
	} {
		resp, errText := callModifiedSymbols(t, db, gitDir, args)
		require.Empty(t, errText)
		assert.ElementsMatch(t, []string{"a", "b"}, symbolNames(resp), "args=%v", args)
		for _, s := range resp.Symbols {
			assert.Equal(t, "added", s.ChangeType)
		}
	}
}

func TestModifiedSymbols_RenamedFileUsesNewPath(t *testing.T) {
	t.Parallel()
	requireGit(t)
	db := openToolTestDB(t)

	gitDir := setupGitRepo(t)
	runGit(t, gitDir, "mv", "test.go", "renamed.go")
	// Keep the content similar enough that git would detect a rename.
	writeFile(t, gitDir, "renamed.go", "package main\n\nfunc main() {\n\tprintln(\"hello\")\n\tprintln(\"more\")\n}\n")
	runGit(t, gitDir, "add", "-A")
	insertSymbolWithLines(t, db, "repo", "main", "function", "func main() {}", "renamed.go", 3, 6)

	t.Run("staged rename vs HEAD", func(t *testing.T) {
		resp, errText := callModifiedSymbols(t, db, gitDir, map[string]any{"include_staged": true})
		require.Empty(t, errText)
		require.Len(t, resp.Symbols, 1)
		assert.Equal(t, "renamed.go", resp.Symbols[0].FilePath)
	})

	t.Run("committed rename vs base_ref", func(t *testing.T) {
		runGit(t, gitDir, "commit", "-m", "rename")
		resp, errText := callModifiedSymbols(t, db, gitDir, map[string]any{"base_ref": "HEAD~1"})
		require.Empty(t, errText)
		require.Len(t, resp.Symbols, 1)
		assert.Equal(t, "renamed.go", resp.Symbols[0].FilePath)
		assert.Equal(t, "added", resp.Symbols[0].ChangeType)
	})
}

func TestModifiedSymbols_NonASCIIPath(t *testing.T) {
	t.Parallel()
	requireGit(t)
	db := openToolTestDB(t)

	gitDir := setupGitRepo(t)
	// Make sure quoting would be on, as it is by default.
	runGit(t, gitDir, "config", "core.quotePath", "true")
	require.NoError(t, os.MkdirAll(filepath.Join(gitDir, "módulo"), 0o755))
	writeFile(t, gitDir, "módulo/café.go", "package m\n\nfunc Café() {\n\tprintln(1)\n}\n")
	runGit(t, gitDir, "add", ".")
	runGit(t, gitDir, "commit", "-m", "unicode")
	insertSymbolWithLines(t, db, "repo", "Café", "function", "func Café() {}", "módulo/café.go", 3, 5)
	insertSymbolWithLines(t, db, "repo", "Nuevo", "function", "func Nuevo() {}", "módulo/año nuevo.go", 1, 1)

	writeFile(t, gitDir, "módulo/café.go", "package m\n\nfunc Café() {\n\tprintln(2)\n}\n")
	writeFile(t, gitDir, "módulo/año nuevo.go", "package m\n")

	resp, errText := callModifiedSymbols(t, db, gitDir, map[string]any{"include_staged": false})
	require.Empty(t, errText)
	assert.ElementsMatch(t, []string{"Café", "Nuevo"}, symbolNames(resp))
}

func TestModifiedSymbols_UnknownRefIsError(t *testing.T) {
	t.Parallel()
	requireGit(t)
	db := openToolTestDB(t)
	gitDir := setupGitRepo(t)

	_, errText := callModifiedSymbols(t, db, gitDir, map[string]any{"base_ref": "no-such-branch"})
	assert.Contains(t, errText, "git diff extraction failed")
}
