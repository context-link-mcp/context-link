package tools

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/context-link-mcp/context-link/internal/store"
)

// Match types reported by tools that resolve a symbol name.
const (
	matchExact = "exact" // the name or qualified name matched exactly
	matchFuzzy = "fuzzy" // no exact match; the only symbol containing the name
)

// matchType returns the match_type value for a resolution.
func matchType(res store.SymbolResolution) string {
	if res.Fuzzy {
		return matchFuzzy
	}
	return matchExact
}

// resolveSymbolArg resolves a symbol_name argument for a read-only tool, which
// may fall back to a unique substring match (reported as match_type "fuzzy").
// On failure it returns a message for the tool's error result.
func resolveSymbolArg(ctx context.Context, db *store.DB, tool, repoName, name string) (store.SymbolResolution, string) {
	res, err := store.ResolveSymbol(ctx, db, repoName, strings.TrimSpace(name), true)
	if err != nil {
		return res, symbolLookupMessage(tool, repoName, name, res, err)
	}
	return res, ""
}

// symbolLookupMessage describes a failed symbol lookup, distinguishing a blank
// name, an unknown name (with suggestions), an ambiguous name and an internal
// failure.
func symbolLookupMessage(tool, repoName, name string, res store.SymbolResolution, err error) string {
	prefix := ""
	if tool != "" {
		prefix = tool + ": "
	}
	switch {
	case errors.Is(err, store.ErrEmptySymbolName):
		return prefix + "'symbol_name' parameter is required and must not be empty"
	case errors.Is(err, store.ErrAmbiguousSymbol):
		return fmt.Sprintf("%ssymbol %q is ambiguous in repository %q: no exact match, several symbols contain it (%s). "+
			"Pass an exact name or qualified name.", prefix, name, repoName, strings.Join(candidateNames(res), ", "))
	case errors.Is(err, store.ErrSymbolNotFound):
		if len(res.Candidates) > 0 {
			return fmt.Sprintf("%ssymbol %q not found in repository %q; did you mean: %s?",
				prefix, name, repoName, strings.Join(candidateNames(res), ", "))
		}
		return fmt.Sprintf("%ssymbol %q not found in repository %q. Try using semantic_search_symbols first.",
			prefix, name, repoName)
	default:
		return fmt.Sprintf("%sfailed to look up symbol %q: %v", prefix, name, err)
	}
}

// candidateNames lists the qualified names (with location) of a resolution's
// candidates.
func candidateNames(res store.SymbolResolution) []string {
	names := make([]string, 0, len(res.Candidates))
	for _, c := range res.Candidates {
		names = append(names, fmt.Sprintf("%s (%s:%d)", c.QualifiedName, c.FilePath, c.StartLine))
	}
	return names
}

// errPathOutsideRoot is returned by cleanRepoPath for a path that leaves the
// project root.
var errPathOutsideRoot = errors.New("path must be relative to the project root and stay inside it")

// cleanRepoPath normalises a file path argument to the repo-relative,
// slash-separated form the index stores: backslashes become slashes, "./" and
// ".." segments are resolved, and an absolute path under projectRoot is made
// relative. A path that escapes the root (or is absolute without a root, or
// names the root itself) returns errPathOutsideRoot before the filesystem is
// touched, so the error reveals nothing about files outside the root.
func cleanRepoPath(projectRoot, p string) (string, error) {
	p = strings.TrimSpace(p)
	if p == "" {
		return "", errors.New("path is empty")
	}
	p = strings.ReplaceAll(p, "\\", "/")
	native := filepath.FromSlash(p)
	if filepath.IsAbs(native) || filepath.VolumeName(native) != "" || strings.HasPrefix(p, "/") {
		if projectRoot == "" {
			return "", errPathOutsideRoot
		}
		rel, err := filepath.Rel(filepath.Clean(projectRoot), native)
		if err != nil {
			return "", errPathOutsideRoot
		}
		p = filepath.ToSlash(rel)
	}
	p = path.Clean(p)
	if p == "." || p == ".." || strings.HasPrefix(p, "../") {
		return "", errPathOutsideRoot
	}
	return p, nil
}

// cleanFileFilter normalises an optional file_path filter (see cleanRepoPath)
// and, with a project root, checks that the file exists. An empty filter is
// returned unchanged. On failure it returns a message for the tool's error
// result.
func cleanFileFilter(tool, projectRoot, p string) (string, string) {
	if strings.TrimSpace(p) == "" {
		return "", ""
	}
	cleaned, err := cleanRepoPath(projectRoot, p)
	if err != nil {
		return "", fmt.Sprintf("%s: invalid file_path %q: %v", tool, p, err)
	}
	if projectRoot != "" {
		absPath := filepath.Join(projectRoot, filepath.FromSlash(cleaned))
		if _, err := os.Stat(absPath); errors.Is(err, fs.ErrNotExist) {
			return "", fmt.Sprintf("%s: file %q does not exist on disk.", tool, cleaned)
		}
	}
	return cleaned, ""
}

// cleanPathPrefix normalises a file path prefix filter: backslashes become
// slashes and a leading "./" is dropped. The prefix stays literal (no cleaning
// of a trailing slash), since "internal/store/" and "internal/store" differ.
func cleanPathPrefix(prefix string) string {
	prefix = strings.ReplaceAll(strings.TrimSpace(prefix), "\\", "/")
	for strings.HasPrefix(prefix, "./") {
		prefix = prefix[2:]
	}
	return prefix
}
