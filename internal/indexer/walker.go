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
	gitignore := newGitignores(w.repoRoot)
	realRoot := resolvedRoot(w.repoRoot)

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

		readPath := path
		var info fs.FileInfo
		switch {
		case d.Type()&fs.ModeSymlink != 0:
			// A symlink is indexed as its target, which must be a regular
			// file inside the root; the size limit applies to the target.
			target, targetInfo, ok := resolveFileSymlink(path, realRoot)
			if !ok {
				return nil
			}
			readPath, info = target, targetInfo
		case d.Type().IsRegular():
			info, err = d.Info()
			if err != nil {
				slog.Warn("walker: failed to stat file", "path", path, "error", err)
				return nil
			}
		default:
			return nil // devices, pipes, sockets
		}
		if info.Size() > maxFileSize {
			slog.Debug("walker: skipping large file", "path", relPath, "size", info.Size())
			return nil
		}

		candidates = append(candidates, candidate{
			path:    readPath,
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
func (w *Walker) skipDir(path string, gitignore *gitignores) bool {
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
// ignore patterns (node_modules, vendor, .git, ...) and the .gitignore files
// of the root and of nested directories. The file watcher uses it so that it
// watches exactly the directories that are indexed.
type DirFilter struct {
	walker    *Walker
	gitignore *gitignores
}

// NewDirFilter returns the DirFilter for the repo at repoRoot. Each
// directory's .gitignore is read the first time a path below it is checked
// and cached for the filter's lifetime.
func NewDirFilter(repoRoot string) *DirFilter {
	return &DirFilter{walker: NewWalker(nil, repoRoot), gitignore: newGitignores(repoRoot)}
}

// Skip reports whether the directory at the absolute path dir is skipped by
// the walker, either itself or because it lies outside the repo root. It
// does not check whether an ancestor of dir is skipped.
func (f *DirFilter) Skip(dir string) bool {
	return f.walker.skipDir(filepath.Clean(dir), f.gitignore)
}

// gitignores holds the .gitignore rules of a repo: one file per directory,
// each read lazily on first use and cached. It is safe for concurrent use.
type gitignores struct {
	root string
	mu   sync.Mutex
	dirs map[string]ignoreRules // slash-separated dir relative to root ("" = root) -> rules (nil = no file)
}

func newGitignores(root string) *gitignores {
	return &gitignores{root: root, dirs: make(map[string]ignoreRules)}
}

// rules returns the rules of the .gitignore in relDir, reading it once.
func (g *gitignores) rules(relDir string) ignoreRules {
	g.mu.Lock()
	defer g.mu.Unlock()
	if rules, ok := g.dirs[relDir]; ok {
		return rules
	}
	var rules ignoreRules
	if f, err := os.Open(filepath.Join(g.root, filepath.FromSlash(relDir), ".gitignore")); err == nil {
		rules = parseIgnoreRules(f)
		f.Close()
	}
	g.dirs[relDir] = rules
	return rules
}

// match reports whether relPath (slash-separated, relative to the root) is
// ignored. The .gitignore of every directory above relPath applies, with its
// patterns relative to that directory; as in git, a deeper file overrides a
// shallower one and within a file the last matching rule decides.
func (g *gitignores) match(relPath string, isDir bool) bool {
	parts := strings.Split(relPath, "/")
	ignored := false
	for i := range parts {
		rules := g.rules(strings.Join(parts[:i], "/"))
		ignored = rules.apply(parts[i:], isDir, ignored)
	}
	return ignored
}

// resolvedRoot returns root with symlinks resolved, for confining symlink
// targets. It falls back to the absolute, cleaned root.
func resolvedRoot(root string) string {
	if real, err := filepath.EvalSymlinks(root); err == nil {
		return real
	}
	if abs, err := filepath.Abs(root); err == nil {
		return abs
	}
	return filepath.Clean(root)
}

// insideRoot reports whether the absolute path p is root or lies below it.
func insideRoot(root, p string) bool {
	rel, err := filepath.Rel(root, p)
	if err != nil || filepath.IsAbs(rel) {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// resolveFileSymlink resolves the symlink at path and returns its target if
// the target is a regular file inside realRoot. Links that dangle, point at a
// directory or other non-regular file, or leave the root are skipped.
func resolveFileSymlink(path, realRoot string) (string, fs.FileInfo, bool) {
	target, err := filepath.EvalSymlinks(path)
	if err != nil {
		slog.Debug("walker: skipping unresolvable symlink", "path", path, "error", err)
		return "", nil, false
	}
	if !insideRoot(realRoot, target) {
		slog.Warn("walker: skipping symlink that points outside the project root", "path", path, "target", target)
		return "", nil, false
	}
	info, err := os.Stat(target)
	if err != nil || !info.Mode().IsRegular() {
		return "", nil, false
	}
	return target, info, true
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

// match reports whether relPath (slash-separated, relative to the directory
// holding the rules) is ignored by these rules alone. It evaluates only relPath itself: the walker prunes ignored
// directories, so the contents of an excluded directory are never reached,
// matching git's rule that they cannot be re-included.
func (rules ignoreRules) match(relPath string, isDir bool) bool {
	return rules.apply(strings.Split(relPath, "/"), isDir, false)
}

// apply evaluates rules against the path components parts (relative to the
// directory holding the rules) and returns the new ignored state, starting
// from ignored as decided by shallower .gitignore files.
func (rules ignoreRules) apply(parts []string, isDir bool, ignored bool) bool {
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
