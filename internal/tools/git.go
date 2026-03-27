// Package tools implements all MCP tool handlers for context-link.
package tools

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

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

// ExtractGitDiff extracts changed files and line ranges from git diff.
// baseRef is the git reference to diff against (e.g., "HEAD", "main", "origin/main", commit SHA).
// includeStaged controls whether to include staged changes (git add) in addition to unstaged.
func ExtractGitDiff(ctx context.Context, projectRoot, baseRef string, includeStaged bool) (*GitDiffResult, error) {
	// Step 1: Check if this is a git repository.
	if err := exec.CommandContext(ctx, "git", "-C", projectRoot, "rev-parse", "--git-dir").Run(); err != nil {
		return nil, fmt.Errorf("not a git repository: %w", err)
	}

	result := &GitDiffResult{
		Files: []GitFileDiff{},
	}

	// Step 2: Get changed file list with status.
	files, err := getChangedFiles(ctx, projectRoot, baseRef, includeStaged)
	if err != nil {
		return nil, err
	}

	// Step 3: For each modified/added file, extract line ranges.
	for filePath, status := range files {
		if status == "D" {
			// Deleted files have no line changes to extract.
			result.Files = append(result.Files, GitFileDiff{
				FilePath: filePath,
				Status:   status,
				Hunks:    nil,
			})
			continue
		}

		// Extract hunks for modified/added files.
		hunks, err := getFileHunks(ctx, projectRoot, baseRef, filePath)
		if err != nil {
			// If the file is binary or git diff fails, skip it.
			continue
		}

		if len(hunks) > 0 {
			result.Files = append(result.Files, GitFileDiff{
				FilePath: filePath,
				Status:   status,
				Hunks:    hunks,
			})
		}
	}

	result.FilesChanged = len(result.Files)
	return result, nil
}

// getChangedFiles returns a map of file paths to their status (M, A, D).
func getChangedFiles(ctx context.Context, projectRoot, baseRef string, includeStaged bool) (map[string]string, error) {
	files := make(map[string]string)

	// Get unstaged changes (working tree vs index).
	if includeStaged || baseRef != "HEAD" {
		// When diffing against non-HEAD, always include all changes.
		cmd := exec.CommandContext(ctx, "git", "-C", projectRoot, "diff", "--name-status", baseRef)
		out, err := cmd.Output()
		if err != nil {
			return nil, fmt.Errorf("git diff --name-status failed: %w", err)
		}
		parseNameStatus(string(out), files)
	}

	// Get staged changes (index vs HEAD) if includeStaged is true and baseRef is HEAD.
	if includeStaged && baseRef == "HEAD" {
		cmd := exec.CommandContext(ctx, "git", "-C", projectRoot, "diff", "--name-status", "--cached")
		out, err := cmd.Output()
		if err != nil {
			// If there's no HEAD (empty repo), this will fail — that's ok.
			return files, nil
		}
		parseNameStatus(string(out), files)
	}

	// Get untracked files (new files not yet added).
	cmd := exec.CommandContext(ctx, "git", "-C", projectRoot, "ls-files", "--others", "--exclude-standard")
	out, err := cmd.Output()
	if err == nil {
		for _, line := range strings.Split(string(out), "\n") {
			line = strings.TrimSpace(line)
			if line != "" {
				files[line] = "A"
			}
		}
	}

	return files, nil
}

// parseNameStatus parses git diff --name-status output into the files map.
// Format: "M\tpath/to/file.go\n"
func parseNameStatus(output string, files map[string]string) {
	scanner := bufio.NewScanner(strings.NewReader(output))
	for scanner.Scan() {
		line := scanner.Text()
		parts := strings.Split(line, "\t")
		if len(parts) >= 2 {
			status := parts[0]
			filePath := parts[1]
			files[filePath] = status
		}
	}
}

// getFileHunks extracts line ranges from git diff --unified=0 for a single file.
func getFileHunks(ctx context.Context, projectRoot, baseRef, filePath string) ([]GitHunk, error) {
	cmd := exec.CommandContext(ctx, "git", "-C", projectRoot, "diff", "--unified=0", baseRef, "--", filePath)
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	var hunks []GitHunk
	scanner := bufio.NewScanner(bytes.NewReader(out))
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
