package indexer

import (
	"context"
	"crypto/sha256"
	"fmt"
	"log/slog"
	"strings"

	sitter "github.com/smacker/go-tree-sitter"

	"github.com/context-link/context-link/pkg/models"
)

// captureKindMap maps Tree-sitter capture names to symbol kinds.
var captureKindMap = map[string]string{
	"symbol.function":  "function",
	"symbol.class":     "class",
	"symbol.method":    "method",
	"symbol.interface": "interface",
	"symbol.type":      "type",
	"symbol.variable":  "variable",
}

// Extractor uses Tree-sitter queries to extract symbols and dependencies
// from parsed ASTs. It is stateless and safe for concurrent use.
type Extractor struct{}

// NewExtractor creates a new Extractor.
func NewExtractor() *Extractor {
	return &Extractor{}
}

// ExtractSymbols runs the adapter's symbol query against the parsed tree
// and returns the extracted symbols.
func (e *Extractor) ExtractSymbols(
	ctx context.Context,
	tree *sitter.Tree,
	source []byte,
	adapter LanguageAdapter,
	repoName string,
	filePath string,
) ([]models.Symbol, error) {
	queryBytes := adapter.GetSymbolQuery()
	lang := adapter.GetLanguage()

	query, err := sitter.NewQuery(queryBytes, lang)
	if err != nil {
		return nil, fmt.Errorf("indexer: failed to compile symbol query for %s: %w", adapter.Name(), err)
	}
	defer query.Close()

	cursor := sitter.NewQueryCursor()
	defer cursor.Close()
	cursor.Exec(query, tree.RootNode())

	var symbols []models.Symbol
	// Track class names for building qualified names of methods.
	var currentClassName string
	// Deduplicate symbols matched by overlapping patterns (e.g., both
	// function_declaration and export_statement > function_declaration).
	type symKey struct {
		name      string
		startLine int
	}
	seen := make(map[symKey]bool)

	for {
		match, ok := cursor.NextMatch()
		if !ok {
			break
		}

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		match = cursor.FilterPredicates(match, source)

		sym := e.processSymbolMatch(query, match, source, adapter, repoName, filePath, &currentClassName)
		if sym != nil {
			key := symKey{name: sym.QualifiedName, startLine: sym.StartLine}
			if seen[key] {
				continue
			}
			seen[key] = true
			symbols = append(symbols, *sym)
		}
	}

	return symbols, nil
}

// processSymbolMatch extracts a single symbol from a query match.
func (e *Extractor) processSymbolMatch(
	query *sitter.Query,
	match *sitter.QueryMatch,
	source []byte,
	adapter LanguageAdapter,
	repoName string,
	filePath string,
	currentClassName *string,
) *models.Symbol {
	var name, body string
	var kind string
	var outerNode *sitter.Node

	for _, capture := range match.Captures {
		captureName := query.CaptureNameForId(capture.Index)

		switch captureName {
		case "symbol.name":
			name = capture.Node.Content(source)
		case "symbol.body":
			body = capture.Node.Content(source)
		default:
			// Check if this is a kind capture (symbol.function, symbol.class, etc.)
			if k, ok := captureKindMap[captureName]; ok {
				kind = k
				outerNode = capture.Node
			}
		}
	}

	if name == "" || kind == "" || outerNode == nil {
		return nil
	}

	// Build qualified name.
	qualifiedName := name
	if kind == "method" && *currentClassName != "" {
		qualifiedName = *currentClassName + "." + name
	}

	// Track class name for subsequent method extraction.
	if kind == "class" {
		*currentClassName = name
	}

	// Use the outer node (full declaration) as the code block.
	codeBlock := outerNode.Content(source)
	if codeBlock == "" {
		codeBlock = body
	}

	contentHash := hashString(codeBlock)

	return &models.Symbol{
		RepoName:      repoName,
		Name:          name,
		QualifiedName: qualifiedName,
		Kind:          kind,
		FilePath:      filePath,
		ContentHash:   contentHash,
		CodeBlock:     codeBlock,
		StartLine:     int(outerNode.StartPoint().Row) + 1, // 1-based
		EndLine:       int(outerNode.EndPoint().Row) + 1,
		Language:      adapter.Name(),
	}
}

// ExtractedDep represents a raw dependency extracted from a query match.
type ExtractedDep struct {
	Kind       string // "import", "call", "extends", "implements"
	Source     string // import source path (for imports)
	Symbol     string // called/extended/implemented symbol name
	CallerFile string // file where the dependency originates
}

// ExtractDependencies runs the adapter's dependency query against the parsed
// tree and returns raw dependency edges.
func (e *Extractor) ExtractDependencies(
	ctx context.Context,
	tree *sitter.Tree,
	source []byte,
	adapter LanguageAdapter,
	filePath string,
) ([]ExtractedDep, error) {
	queryBytes := adapter.GetDependencyQuery()
	lang := adapter.GetLanguage()

	query, err := sitter.NewQuery(queryBytes, lang)
	if err != nil {
		return nil, fmt.Errorf("indexer: failed to compile dependency query for %s: %w", adapter.Name(), err)
	}
	defer query.Close()

	cursor := sitter.NewQueryCursor()
	defer cursor.Close()
	cursor.Exec(query, tree.RootNode())

	var deps []ExtractedDep

	for {
		match, ok := cursor.NextMatch()
		if !ok {
			break
		}

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		match = cursor.FilterPredicates(match, source)

		extracted := e.processDependencyMatch(query, match, source, filePath)
		deps = append(deps, extracted...)
	}

	return deps, nil
}

// processDependencyMatch extracts dependencies from a single query match.
func (e *Extractor) processDependencyMatch(
	query *sitter.Query,
	match *sitter.QueryMatch,
	source []byte,
	filePath string,
) []ExtractedDep {
	var deps []ExtractedDep

	for _, capture := range match.Captures {
		captureName := query.CaptureNameForId(capture.Index)
		content := capture.Node.Content(source)

		switch captureName {
		case "dependency.source":
			// Import source — strip quotes.
			importPath := strings.Trim(content, "\"'`")
			deps = append(deps, ExtractedDep{
				Kind:       "import",
				Source:     importPath,
				CallerFile: filePath,
			})

		case "dependency.call":
			deps = append(deps, ExtractedDep{
				Kind:       "call",
				Symbol:     content,
				CallerFile: filePath,
			})

		case "dependency.extends":
			deps = append(deps, ExtractedDep{
				Kind:       "extends",
				Symbol:     content,
				CallerFile: filePath,
			})

		case "dependency.implements":
			deps = append(deps, ExtractedDep{
				Kind:       "implements",
				Symbol:     content,
				CallerFile: filePath,
			})
		}
	}

	return deps
}

// hashString computes a SHA-256 hex digest of a string.
func hashString(s string) string {
	h := sha256.Sum256([]byte(s))
	return fmt.Sprintf("%x", h)
}

// ResolveCallDependency attempts to resolve a call dependency to a known
// symbol ID by matching the called symbol name against the symbol registry.
func ResolveCallDependency(dep ExtractedDep, symbolsByName map[string]int64) *models.Dependency {
	calleeID, ok := symbolsByName[dep.Symbol]
	if !ok {
		slog.Debug("indexer: unresolved dependency", "symbol", dep.Symbol, "kind", dep.Kind)
		return nil
	}

	return &models.Dependency{
		CalleeID: calleeID,
		Kind:     dep.Kind,
	}
}
