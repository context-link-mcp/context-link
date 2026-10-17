package indexer

import (
	"context"
	"fmt"
	"sync"
	"time"

	sitter "github.com/smacker/go-tree-sitter"
)

const defaultParseTimeout = 5 * time.Second

// ParserPool creates Tree-sitter parsers for a single language grammar
// (TS vs TSX are distinct pools).
//
// Parsers are deliberately NOT reused. smacker's ParseCtx starts a watcher
// goroutine that sets the parser's cancellation flag when ctx is done. Parse
// cancels its timeout context on return, so that goroutine can win the race
// against parseComplete and set the flag on a parser that is already back in a
// pool; the next parse on it then aborts with "operation limit was hit". That
// silently dropped ~19% of symbols on a 364-file repo, so every parse gets a
// fresh parser. Creating one is cheap next to the parse itself.
type ParserPool struct {
	language *sitter.Language
}

// NewParserPool creates a parser factory for the given Tree-sitter language.
func NewParserPool(lang *sitter.Language) *ParserPool {
	return &ParserPool{language: lang}
}

// Parse parses the given source code into a Tree-sitter AST tree.
// It respects context cancellation and applies a default 5s timeout.
func (pp *ParserPool) Parse(ctx context.Context, source []byte) (*sitter.Tree, error) {
	// Apply timeout if context doesn't already have a deadline.
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, defaultParseTimeout)
		defer cancel()
	}

	// Check for cancellation before parsing.
	select {
	case <-ctx.Done():
		return nil, fmt.Errorf("indexer: parse cancelled: %w", ctx.Err())
	default:
	}

	parser := sitter.NewParser()
	defer parser.Close()
	parser.SetLanguage(pp.language)

	tree, err := parser.ParseCtx(ctx, nil, source)
	if err != nil {
		return nil, fmt.Errorf("indexer: tree-sitter parse failed: %w", err)
	}
	if tree == nil {
		return nil, fmt.Errorf("indexer: tree-sitter returned nil tree")
	}

	return tree, nil
}

// ParserPoolManager manages parser pools for multiple language adapters.
// It lazily creates one pool per unique grammar (keyed by adapter name).
type ParserPoolManager struct {
	mu    sync.RWMutex
	pools map[string]*ParserPool // adapter name → pool
}

// NewParserPoolManager creates a new ParserPoolManager.
func NewParserPoolManager() *ParserPoolManager {
	return &ParserPoolManager{
		pools: make(map[string]*ParserPool),
	}
}

// GetPool returns or creates a ParserPool for the given adapter.
func (m *ParserPoolManager) GetPool(adapter LanguageAdapter) *ParserPool {
	name := adapter.Name()

	m.mu.RLock()
	if pool, ok := m.pools[name]; ok {
		m.mu.RUnlock()
		return pool
	}
	m.mu.RUnlock()

	m.mu.Lock()
	defer m.mu.Unlock()

	// Double-check after acquiring write lock.
	if pool, ok := m.pools[name]; ok {
		return pool
	}

	pool := NewParserPool(adapter.GetLanguage())
	m.pools[name] = pool
	return pool
}
