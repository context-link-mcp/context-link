// Package indexer provides language-agnostic code indexing via Tree-sitter.
// It discovers source files, parses them into ASTs, and extracts symbols
// and dependencies into the SQLite store.
package indexer

import (
	"fmt"
	"sync"

	sitter "github.com/smacker/go-tree-sitter"
)

// LanguageAdapter defines the capabilities required to index a specific
// programming language. Each adapter bundles a Tree-sitter grammar with
// the Scheme queries needed for symbol and dependency extraction.
type LanguageAdapter interface {
	// Name returns the canonical name of the language (e.g., "typescript", "go").
	Name() string

	// Extensions returns the file extensions this adapter handles (e.g., [".ts"]).
	Extensions() []string

	// GetLanguage returns the compiled Tree-sitter C-binding for the language.
	GetLanguage() *sitter.Language

	// GetSymbolQuery returns the Tree-sitter Scheme query used to extract
	// functions, classes, interfaces, methods, and other symbols.
	GetSymbolQuery() []byte

	// GetDependencyQuery returns the Scheme query used to extract imports,
	// function calls, and inheritance relationships.
	GetDependencyQuery() []byte
}

// LanguageRegistry is a concurrent-safe registry that maps file extensions
// to their corresponding LanguageAdapter.
type LanguageRegistry struct {
	mu       sync.RWMutex
	adapters map[string]LanguageAdapter // extension → adapter (e.g., ".ts" → TypeScript)
}

// NewLanguageRegistry creates an empty LanguageRegistry.
func NewLanguageRegistry() *LanguageRegistry {
	return &LanguageRegistry{
		adapters: make(map[string]LanguageAdapter),
	}
}

// Register adds a LanguageAdapter to the registry, keyed by each of its
// declared extensions. Returns an error if any extension is already registered.
func (r *LanguageRegistry) Register(adapter LanguageAdapter) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	for _, ext := range adapter.Extensions() {
		if existing, ok := r.adapters[ext]; ok {
			return fmt.Errorf("indexer: extension %q already registered by adapter %q", ext, existing.Name())
		}
	}

	for _, ext := range adapter.Extensions() {
		r.adapters[ext] = adapter
	}
	return nil
}

// GetAdapter returns the LanguageAdapter for the given file extension.
// The extension must include the dot (e.g., ".ts"). Returns nil, false
// if no adapter is registered for this extension.
func (r *LanguageRegistry) GetAdapter(extension string) (LanguageAdapter, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	adapter, ok := r.adapters[extension]
	return adapter, ok
}

// Extensions returns all registered file extensions.
func (r *LanguageRegistry) Extensions() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	exts := make([]string, 0, len(r.adapters))
	for ext := range r.adapters {
		exts = append(exts, ext)
	}
	return exts
}
