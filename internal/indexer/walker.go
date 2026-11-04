package indexer

import (
	"bufio"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"

	"golang.org/x/sync/errgroup"

	"github.com/context-link-mcp/context-link/pkg/models"
)

// defaultIgnorePatterns are directories always skipped during walking.
var defaultIgnorePatterns = []string{
	"node_modules",
	"dist",
	"build",
	".git",
	".next",
	".nuxt",
	"coverage",
	"__pycache__",
	"vendor",
}

// maxFileSize is the maximum file size to index (1 MB).
const maxFileSize = 1 << 20

// WalkResult holds the discovered files from a directory walk.
type WalkResult struct {
	Files []DiscoveredFile
}

// DiscoveredFile represents a source file discovered during walking.
type DiscoveredFile struct {
	Path        string // absolute path
	RelPath     string // relative to repo root
	ContentHash string // SHA-256 hex
	SizeBytes   int64
	Extension   string // e.g., ".ts"
}

// Walker discovers source files in a directory tree, respecting .gitignore
// rules and the language registry's supported extensions.
type Walker struct {
	registry       *LanguageRegistry
	ignorePatterns []string
	repoRoot       string
}

// NewWalker creates a Walker for the given repository root directory.
func NewWalker(registry *LanguageRegistry, repoRoot string) *Walker {
	return &Walker{
		registry:       registry,
		ignorePatterns: defaultIgnorePatterns,
		repoRoot:       repoRoot,
	}
}

// Walk discovers all indexable source files under the repo root.
// It respects .gitignore patterns and skips files exceeding maxFileSize.
// Phase 1 discovers files (no I/O beyond stat), Phase 2 hashes in parallel.
func (w *Walker) Walk(ctx context.Context) (*WalkResult, error) {
	gitignore := w.loadGitignore()

	// Phase 1: Discover files (no hashing — fast directory traversal).
	type candidate struct {
		path    string
		relPath string
		size    int64
		ext     string
	}
	var candidates []candidate

	err := filepath.WalkDir(w.repoRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			slog.Warn("walker: error accessing path", "path", path, "error", err)
			return nil // skip errored paths, don't abort
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		if d.IsDir() {
			if w.skipDir(path, gitignore) {
				return fs.SkipDir
			}
			return nil
		}

		ext := filepath.Ext(path)
		if _, ok := w.registry.GetAdapter(ext); !ok {
			return nil
		}

		relPath, err := filepath.Rel(w.repoRoot, path)
		if err != nil {
			slog.Warn("walker: failed to compute relative path", "path", path, "error", err)
			return nil
		}
		relPath = filepath.ToSlash(relPath)

		if gitignore.match(relPath, false) {
			return nil
		}

		info, err := d.Info()
		if err != nil {
			slog.Warn("walker: failed to stat file", "path", path, "error", err)
			return nil
		}
		if info.Size() > maxFileSize {
			slog.Debug("walker: skipping large file", "path", relPath, "size", info.Size())
			return nil
		}

		candidates = append(candidates, candidate{
			path:    path,
			relPath: relPath,
			size:    info.Size(),
			ext:     ext,
		})

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("indexer: walk failed: %w", err)
	}

	// Phase 2: Hash files in parallel with bounded concurrency.
	files := make([]DiscoveredFile, len(candidates))
	var mu sync.Mutex
	var hashErrors int

	g, gCtx := errgroup.WithContext(ctx)
	g.SetLimit(8) // bounded parallelism for I/O

	for i, c := range candidates {
		g.Go(func() error {
			select {
			case <-gCtx.Done():
				return gCtx.Err()
			default:
			}

			hash, err := hashFile(c.path)
			if err != nil {
				slog.Warn("walker: failed to hash file", "path", c.path, "error", err)
				mu.Lock()
				hashErrors++
				mu.Unlock()
				return nil // non-fatal
			}

			files[i] = DiscoveredFile{
				Path:        c.path,
				RelPath:     c.relPath,
				ContentHash: hash,
				SizeBytes:   c.size,
				Extension:   c.ext,
			}
			return nil
		})
	}

	if err := g.Wait(); err != nil {
		return nil, fmt.Errorf("indexer: parallel hashing failed: %w", err)
	}

	// Filter out files that failed hashing (empty ContentHash).
	result := make([]DiscoveredFile, 0, len(files)-hashErrors)
	for _, f := range files {
		if f.ContentHash != "" {
			result = append(result, f)
		}
	}

	return &WalkResult{Files: result}, nil
}

// ToModelFiles converts discovered files to model File structs.
func (w *Walker) ToModelFiles(files []DiscoveredFile, repoName string) []models.File {
	result := make([]models.File, len(files))
	for i, f := range files {
		result[i] = models.File{
			RepoName:    repoName,
			Path:        f.RelPath,
			ContentHash: f.ContentHash,
			SizeBytes:   f.SizeBytes,
		}
	}
	return result
}

// skipDir reports whether the walk skips the directory at the absolute path.
// Ignore rules apply to entries inside the root, never to the root itself (a
// repo checked out as "vendor" or a ".*" rule); paths outside the root are
// always skipped.
func (w *Walker) skipDir(path string, gitignore ignoreRules) bool {
	if path == w.repoRoot {
		return false
	}
	relDir, err := filepath.Rel(w.repoRoot, path)
	if err != nil || relDir == ".." || strings.HasPrefix(relDir, ".."+string(filepath.Separator)) {
		return true
	}
	if relDir == "." {
		return false
	}
	name := filepath.Base(path)
	for _, pattern := range w.ignorePatterns {
		if name == pattern {
			return true
		}
	}
	return gitignore.match(filepath.ToSlash(relDir), true)
}

// DirFilter reports which directories a walk of a repo skips: the default
// ignore patterns (node_modules, vendor, .git, ...) and the root .gitignore.
// The file watcher uses it so that it watches exactly the directories that
// are indexed.
type DirFilter struct {
	walker    *Walker
	gitignore ignoreRules
}

// NewDirFilter returns the DirFilter for the repo at repoRoot. The root
// .gitignore is read once, here.
func NewDirFilter(repoRoot string) *DirFilter {
	w := NewWalker(nil, repoRoot)
	return &DirFilter{walker: w, gitignore: w.loadGitignore()}
}

// Skip reports whether the directory at the absolute path dir is skipped by
// the walker, either itself or because it lies outside the repo root. It
// does not check dir's ancestors.
func (f *DirFilter) Skip(dir string) bool {
	return f.walker.skipDir(filepath.Clean(dir), f.gitignore)
}

// loadGitignore reads .gitignore from the repo root and returns its rules.
// Nested .gitignore files are not read.
func (w *Walker) loadGitignore() ignoreRules {
	f, err := os.Open(filepath.Join(w.repoRoot, ".gitignore"))
	if err != nil {
		return nil
	}
	defer f.Close()
	return parseIgnoreRules(f)
}

// ignoreRule is one parsed .gitignore line.
type ignoreRule struct {
	segments []string // pattern split on "/"; a lone "**" segment spans directories
	negate   bool     // "!pattern": re-include a path an earlier rule excluded
	dirOnly  bool     // "pattern/": matches directories only
	anchored bool     // the pattern has a "/" before its end: match the whole path from the root
}

// ignoreRules is an ordered list of .gitignore rules. The last matching rule
// decides, as in git.
type ignoreRules []ignoreRule

// parseIgnoreRules parses .gitignore content. Blank lines and "#" comments are
// skipped, and a leading backslash escapes "#" or "!".
func parseIgnoreRules(r io.Reader) ignoreRules {
	var rules ignoreRules
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := strings.TrimRight(scanner.Text(), " \t\r")
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		var rule ignoreRule
		if strings.HasPrefix(line, "!") {
			rule.negate = true
			line = line[1:]
		} else if strings.HasPrefix(line, `\!`) || strings.HasPrefix(line, `\#`) {
			line = line[1:]
		}
		if strings.HasSuffix(line, "/") {
			rule.dirOnly = true
			line = strings.TrimRight(line, "/")
		}
		if strings.Contains(line, "/") {
			rule.anchored = true
			line = strings.TrimPrefix(line, "/")
		}
		if line == "" {
			continue
		}
		rule.segments = strings.Split(line, "/")
		rules = append(rules, rule)
	}
	return rules
}

// match reports whether relPath (slash-separated, relative to the repo root)
// is ignored. It evaluates only relPath itself: the walker prunes ignored
// directories, so the contents of an excluded directory are never reached,
// matching git's rule that they cannot be re-included.
func (rules ignoreRules) match(relPath string, isDir bool) bool {
	ignored := false
	parts := strings.Split(relPath, "/")
	for _, rule := range rules {
		if rule.dirOnly && !isDir {
			continue
		}
		var ok bool
		if rule.anchored {
			ok = matchSegments(rule.segments, parts)
		} else {
			ok = matchSegment(rule.segments[0], parts[len(parts)-1])
		}
		if ok {
			ignored = !rule.negate
		}
	}
	return ignored
}

// matchSegments matches pattern segments against path segments. "**" matches
// zero or more directories, or at least one entry when it ends the pattern
// ("dir/**" matches everything inside dir but not dir itself).
func matchSegments(pattern, parts []string) bool {
	if len(pattern) == 0 {
		return len(parts) == 0
	}
	if pattern[0] == "**" {
		if len(pattern) == 1 {
			return len(parts) > 0
		}
		for i := 0; i <= len(parts); i++ {
			if matchSegments(pattern[1:], parts[i:]) {
				return true
			}
		}
		return false
	}
	if len(parts) == 0 || !matchSegment(pattern[0], parts[0]) {
		return false
	}
	return matchSegments(pattern[1:], parts[1:])
}

// matchSegment matches one glob segment against one path component. It uses
// path.Match, whose "*" never crosses "/" on any OS.
func matchSegment(pattern, name string) bool {
	if pattern == "**" {
		return true
	}
	ok, _ := path.Match(pattern, name)
	return ok
}

// hashFile computes the SHA-256 hex digest of a file's contents.
func hashFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("indexer: failed to read file for hashing: %w", err)
	}
	h := sha256.Sum256(data)
	return fmt.Sprintf("%x", h), nil
}
