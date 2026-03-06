package indexer

import (
	"context"
	"fmt"
	"sync"
	"time"

	sitter "github.com/smacker/go-tree-sitter"
)

const defaultParseTimeout = 5 * time.Second

// ParserPool manages a pool of reusable Tree-sitter parsers for a single
// language grammar. One pool should be created per grammar (TS vs TSX).
type ParserPool struct {
	pool     sync.Pool
	language *sitter.Language
}

// NewParserPool creates a parser pool for the given Tree-sitter language.
func NewParserPool(lang *sitter.Language) *ParserPool {
	pp := &ParserPool{language: lang}
	pp.pool = sync.Pool{
		New: func() any {
			p := sitter.NewParser()
			p.SetLanguage(lang)
			return p
		},
	}
	return pp
}

// Parse parses the given source code into a Tree-sitter AST tree.
// It respects context cancellation and applies a default 5s timeout.
// The caller must NOT hold a reference to the returned tree after
// it is done reading — the parser may be reused.
func (pp *ParserPool) Parse(ctx context.Context, source []byte) (*sitter.Tree, error) {
	// Apply timeout if context doesn't already have a deadline.
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, defaultParseTimeout)
		defer cancel()
	}

	parser := pp.pool.Get().(*sitter.Parser)
	defer pp.pool.Put(parser)

	// Check for cancellation before parsing.
	select {
	case <-ctx.Done():
		return nil, fmt.Errorf("indexer: parse cancelled: %w", ctx.Err())
	default:
	}

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
