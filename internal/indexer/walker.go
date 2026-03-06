package indexer

import (
	"bufio"
	"context"
	"crypto/sha256"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/context-link/context-link/pkg/models"
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
func (w *Walker) Walk(ctx context.Context) (*WalkResult, error) {
	gitignorePatterns := w.loadGitignore()

	var files []DiscoveredFile

	err := filepath.WalkDir(w.repoRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			slog.Warn("walker: error accessing path", "path", path, "error", err)
			return nil // skip errored paths, don't abort
		}

		// Check context cancellation.
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// Skip default ignore directories.
		if d.IsDir() {
			name := d.Name()
			for _, pattern := range w.ignorePatterns {
				if name == pattern {
					return fs.SkipDir
				}
			}

			// Check gitignore patterns for directories.
			relDir, _ := filepath.Rel(w.repoRoot, path)
			relDir = filepath.ToSlash(relDir)
			if w.matchesGitignore(relDir+"/", gitignorePatterns) {
				return fs.SkipDir
			}

			return nil
		}

		// Only process files with registered extensions.
		ext := filepath.Ext(path)
		if _, ok := w.registry.GetAdapter(ext); !ok {
			return nil
		}

		// Check gitignore for files.
		relPath, err := filepath.Rel(w.repoRoot, path)
		if err != nil {
			slog.Warn("walker: failed to compute relative path", "path", path, "error", err)
			return nil
		}
		relPath = filepath.ToSlash(relPath)

		if w.matchesGitignore(relPath, gitignorePatterns) {
			return nil
		}

		// Check file size.
		info, err := d.Info()
		if err != nil {
			slog.Warn("walker: failed to stat file", "path", path, "error", err)
			return nil
		}
		if info.Size() > maxFileSize {
			slog.Debug("walker: skipping large file", "path", relPath, "size", info.Size())
			return nil
		}

		// Compute content hash.
		hash, err := hashFile(path)
		if err != nil {
			slog.Warn("walker: failed to hash file", "path", path, "error", err)
			return nil
		}

		files = append(files, DiscoveredFile{
			Path:        path,
			RelPath:     relPath,
			ContentHash: hash,
			SizeBytes:   info.Size(),
			Extension:   ext,
		})

		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("indexer: walk failed: %w", err)
	}

	return &WalkResult{Files: files}, nil
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

// loadGitignore reads .gitignore from the repo root and returns the patterns.
func (w *Walker) loadGitignore() []string {
	gitignorePath := filepath.Join(w.repoRoot, ".gitignore")
	f, err := os.Open(gitignorePath)
	if err != nil {
		return nil
	}
	defer f.Close()

	var patterns []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		patterns = append(patterns, line)
	}
	return patterns
}

// matchesGitignore checks if a path matches any gitignore pattern.
// This is a simplified implementation supporting:
// - Exact directory matches (pattern ending with /)
// - Simple glob patterns with * wildcard
// - Prefix matches
func (w *Walker) matchesGitignore(relPath string, patterns []string) bool {
	for _, pattern := range patterns {
		if matchPattern(pattern, relPath) {
			return true
		}
	}
	return false
}

// matchPattern matches a single gitignore-style pattern against a path.
func matchPattern(pattern, path string) bool {
	// Negation patterns not supported in simplified implementation.
	if strings.HasPrefix(pattern, "!") {
		return false
	}

	// Remove leading slash (anchored pattern).
	pattern = strings.TrimPrefix(pattern, "/")

	// Directory-only pattern.
	isDir := strings.HasSuffix(pattern, "/")
	pattern = strings.TrimSuffix(pattern, "/")

	if isDir && !strings.HasSuffix(path, "/") {
		// Directory pattern only matches directory paths.
		// Check if path is within this directory.
		prefix := pattern + "/"
		return strings.HasPrefix(path, prefix)
	}

	// Try exact match.
	if path == pattern {
		return true
	}

	// Try basename match (pattern without path separators).
	if !strings.Contains(pattern, "/") {
		base := filepath.Base(path)
		if matched, _ := filepath.Match(pattern, base); matched {
			return true
		}
		// Also check path components.
		parts := strings.Split(path, "/")
		for _, part := range parts {
			if matched, _ := filepath.Match(pattern, part); matched {
				return true
			}
		}
	}

	// Try glob match on full path.
	if matched, _ := filepath.Match(pattern, path); matched {
		return true
	}

	// Try prefix match for directory patterns.
	if strings.HasPrefix(path, pattern+"/") {
		return true
	}

	return false
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
