// Package tools implements all MCP tool handlers for context-link.
package tools

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// ErrInvalidGitRef is returned when a caller-supplied git ref is unsafe to pass
// to git (e.g. it starts with "-" and would be parsed as an option).
var ErrInvalidGitRef = errors.New("invalid git ref")

// GitHunk represents a contiguous block of changed lines in a file.
type GitHunk struct {
	StartLine int // 1-indexed line number where the change starts
	LineCount int // Number of lines in this change
}

// GitFileDiff represents all changes in a single file.
type GitFileDiff struct {
	FilePath string
	Hunks    []GitHunk
	Status   string // "M" (modified), "A" (added), "D" (deleted)
}

// GitDiffResult contains all changed files and their hunks.
type GitDiffResult struct {
	FilesChanged int
	Files        []GitFileDiff
}

// hunkHeaderRe matches git diff hunk headers like: @@ -10,5 +12,8 @@
// Captures the new-side start line and line count.
var hunkHeaderRe = regexp.MustCompile(`^@@ -\d+(?:,\d+)? \+(\d+)(?:,(\d+))? @@`)

// gitDiffBaseArgs are passed to every git diff invocation. They make output
// independent of user config: no external diff drivers or textconv filters,
// no colour, and no rename detection (so a renamed file is reported under its
// new path as an addition). --relative limits the diff to the current
// directory (the project root, via -C) and makes paths relative to it, which
// is how the index stores them.
var gitDiffBaseArgs = []string{"diff", "--no-color", "--no-ext-diff", "--no-textconv", "--no-renames", "--relative"}

// ValidateGitRef rejects refs that git could interpret as an option or that
// contain control characters. It does not check that the ref exists.
func ValidateGitRef(ref string) error {
	trimmed := strings.TrimSpace(ref)
	if trimmed == "" {
		return fmt.Errorf("%w: ref is empty", ErrInvalidGitRef)
	}
	if strings.HasPrefix(trimmed, "-") {
		return fmt.Errorf("%w: %q must not start with '-'", ErrInvalidGitRef, ref)
	}
	for _, r := range ref {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("%w: %q contains control characters", ErrInvalidGitRef, ref)
		}
	}
	return nil
}

// ExtractGitDiff extracts changed files and line ranges from git diff.
// baseRef is the git reference to diff against (e.g., "HEAD", "main", "origin/main", commit SHA).
// When baseRef is "HEAD" and includeStaged is false, only unstaged changes
// (working tree vs index) are reported. Otherwise the working tree is diffed
// against baseRef, which covers both staged and unstaged changes.
// Untracked (not ignored) files are always reported as fully added.
// Paths are relative to projectRoot, which may be a subdirectory of the repository.
func ExtractGitDiff(ctx context.Context, projectRoot, baseRef string, includeStaged bool) (*GitDiffResult, error) {
	if err := ValidateGitRef(baseRef); err != nil {
		return nil, err
	}
	baseRef = strings.TrimSpace(baseRef)

	// Step 1: Check if this is a git repository.
	if err := exec.CommandContext(ctx, "git", "-C", projectRoot, "rev-parse", "--git-dir").Run(); err != nil {
		return nil, fmt.Errorf("not a git repository: %w", err)
	}

	// Step 2: Decide what to diff against. No user input reaches git argv
	// except through rev-parse --end-of-options; diff only sees a resolved SHA.
	var revArgs []string
	if includeStaged || baseRef != "HEAD" {
		sha, err := resolveCommit(ctx, projectRoot, baseRef)
		if err != nil {
			return nil, err
		}
		revArgs = []string{sha}
	}

	result := &GitDiffResult{
		Files: []GitFileDiff{},
	}

	// Step 3: Get changed tracked files with status.
	files, err := getChangedFiles(ctx, projectRoot, revArgs)
	if err != nil {
		return nil, err
	}

	// Step 4: For each modified/added file, extract line ranges.
	for _, f := range files {
		if f.Status == "D" {
			// Deleted files have no line changes to extract.
			result.Files = append(result.Files, GitFileDiff{FilePath: f.FilePath, Status: f.Status})
			continue
		}

		hunks, err := getFileHunks(ctx, projectRoot, revArgs, f.FilePath)
		if err != nil {
			// If the file is binary or git diff fails, skip it.
			continue
		}
		if len(hunks) > 0 {
			result.Files = append(result.Files, GitFileDiff{FilePath: f.FilePath, Status: f.Status, Hunks: hunks})
		}
	}

	// Step 5: Untracked files have no diff hunks; every line counts as added.
	untracked, err := getUntrackedFiles(ctx, projectRoot)
	if err != nil {
		return nil, err
	}
	for _, path := range untracked {
		hunk, ok := wholeFileHunk(filepath.Join(projectRoot, filepath.FromSlash(path)))
		if !ok {
			continue
		}
		result.Files = append(result.Files, GitFileDiff{FilePath: path, Status: "A", Hunks: []GitHunk{hunk}})
	}

	result.FilesChanged = len(result.Files)
	return result, nil
}

// resolveCommit resolves ref to a full commit SHA. --end-of-options keeps git
// from parsing ref as an option even if validation were bypassed.
func resolveCommit(ctx context.Context, projectRoot, ref string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", "-C", projectRoot,
		"rev-parse", "--verify", "--quiet", "--end-of-options", ref+"^{commit}")
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("unknown git revision %q: %w", ref, err)
	}
	sha := strings.TrimSpace(string(out))
	if sha == "" || strings.HasPrefix(sha, "-") {
		return "", fmt.Errorf("unknown git revision %q", ref)
	}
	return sha, nil
}

// changedFile is one entry from git diff --name-status.
type changedFile struct {
	FilePath string
	Status   string
}

// getChangedFiles returns changed tracked files relative to projectRoot.
// revArgs is empty (working tree vs index) or a single resolved commit SHA
// (working tree vs that commit).
func getChangedFiles(ctx context.Context, projectRoot string, revArgs []string) ([]changedFile, error) {
	args := append([]string{"-C", projectRoot}, gitDiffBaseArgs...)
	args = append(args, "--name-status", "-z")
	args = append(args, revArgs...)
	args = append(args, "--")

	out, err := exec.CommandContext(ctx, "git", args...).Output()
	if err != nil {
		return nil, fmt.Errorf("git diff --name-status failed: %w", err)
	}
	return parseNameStatusZ(out), nil
}

// parseNameStatusZ parses `git diff --name-status -z --no-renames` output:
// "<status>\0<path>\0" repeated. Paths are raw (never quoted) with -z.
// Typechanges ("T") are reported as modifications.
func parseNameStatusZ(out []byte) []changedFile {
	fields := strings.Split(string(out), "\x00")
	var files []changedFile
	for i := 0; i+1 < len(fields); i += 2 {
		status, path := fields[i], fields[i+1]
		if status == "" || path == "" {
			continue
		}
		switch status[0] {
		case 'A', 'D':
			status = status[:1]
		default:
			status = "M"
		}
		files = append(files, changedFile{FilePath: path, Status: status})
	}
	return files
}

// getUntrackedFiles lists untracked, non-ignored files under projectRoot,
// relative to it.
func getUntrackedFiles(ctx context.Context, projectRoot string) ([]string, error) {
	out, err := exec.CommandContext(ctx, "git", "-C", projectRoot,
		"ls-files", "--others", "--exclude-standard", "-z").Output()
	if err != nil {
		return nil, fmt.Errorf("git ls-files failed: %w", err)
	}
	var paths []string
	for _, p := range strings.Split(string(out), "\x00") {
		if p != "" {
			paths = append(paths, p)
		}
	}
	return paths, nil
}

// wholeFileHunk returns a hunk spanning every line of the file at path.
// It returns false for unreadable or empty files.
func wholeFileHunk(path string) (GitHunk, bool) {
	data, err := os.ReadFile(path)
	if err != nil || len(data) == 0 {
		return GitHunk{}, false
	}
	lines := bytes.Count(data, []byte{'\n'})
	if data[len(data)-1] != '\n' {
		lines++
	}
	return GitHunk{StartLine: 1, LineCount: lines}, true
}

// getFileHunks extracts line ranges from git diff --unified=0 for a single file.
// filePath is passed as a literal pathspec so glob characters in names are not expanded.
func getFileHunks(ctx context.Context, projectRoot string, revArgs []string, filePath string) ([]GitHunk, error) {
	args := append([]string{"-C", projectRoot}, gitDiffBaseArgs...)
	args = append(args, "--unified=0")
	args = append(args, revArgs...)
	args = append(args, "--", ":(literal)"+filePath)

	out, err := exec.CommandContext(ctx, "git", args...).Output()
	if err != nil {
		return nil, err
	}

	var hunks []GitHunk
	scanner := bufio.NewScanner(bytes.NewReader(out))
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		matches := hunkHeaderRe.FindStringSubmatch(line)
		if matches == nil {
			continue
		}

		// Parse the new-side start line and line count.
		startLine, err := strconv.Atoi(matches[1])
		if err != nil {
			continue
		}

		lineCount := 1 // Default to 1 line if not specified.
		if matches[2] != "" {
			lineCount, err = strconv.Atoi(matches[2])
			if err != nil {
				continue
			}
		}

		// Git reports line count of 0 for deletions — skip these.
		if lineCount > 0 {
			hunks = append(hunks, GitHunk{
				StartLine: startLine,
				LineCount: lineCount,
			})
		}
	}

	return hunks, scanner.Err()
}
