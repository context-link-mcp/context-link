package indexer

import (
	"context"
	"crypto/sha256"
	"fmt"
	"log/slog"
	"strings"

	sitter "github.com/smacker/go-tree-sitter"

	"github.com/context-link-mcp/context-link/pkg/models"
)

// safeNodeContent extracts node content with bounds checking to prevent
// panics from stale tree-sitter node offsets.
func safeNodeContent(node *sitter.Node, source []byte) (string, bool) {
	start := node.StartByte()
	end := node.EndByte()
	if int(end) > len(source) || start > end {
		return "", false
	}
	return node.Content(source), true
}

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

// classLikeNodeTypes maps AST node types to the field name that holds the
// class/struct/impl name. Used by resolveParentClass to walk up the tree.
var classLikeNodeTypes = map[string]string{
	"class_definition":  "name", // Python
	"class_declaration": "name", // TypeScript, JavaScript, Java, C#
	"impl_item":         "type", // Rust
	"class_specifier":   "name", // C++
	"struct_specifier":  "name", // C++
}

// resolveParentClass walks up the AST from node to find an enclosing class,
// struct, or impl container. Returns the container name and true if found.
func resolveParentClass(node *sitter.Node, source []byte) (string, bool) {
	current := node.Parent()
	for current != nil {
		if nameField, ok := classLikeNodeTypes[current.Type()]; ok {
			nameNode := current.ChildByFieldName(nameField)
			if nameNode != nil {
				if content, ok := safeNodeContent(nameNode, source); ok && content != "" {
					return content, true
				}
			}
		}
		current = current.Parent()
	}
	return "", false
}

// resolveGoReceiver extracts the receiver type from a Go method_declaration node.
// For example, `func (c *Cache) Get()` returns "Cache".
func resolveGoReceiver(node *sitter.Node, source []byte) (string, bool) {
	if node.Type() != "method_declaration" {
		return "", false
	}
	receiverNode := node.ChildByFieldName("receiver")
	if receiverNode == nil {
		return "", false
	}
	// The receiver is (parameter_list (parameter_declaration name: ... type: ...))
	for i := 0; i < int(receiverNode.NamedChildCount()); i++ {
		param := receiverNode.NamedChild(i)
		typeNode := param.ChildByFieldName("type")
		if typeNode == nil {
			continue
		}
		// Unwrap pointer_type: *Foo → Foo
		if typeNode.Type() == "pointer_type" && typeNode.NamedChildCount() > 0 {
			typeNode = typeNode.NamedChild(0)
		}
		if content, ok := safeNodeContent(typeNode, source); ok && content != "" {
			return content, true
		}
	}
	return "", false
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

		sym := e.processSymbolMatch(query, match, source, adapter, repoName, filePath)
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
// It uses AST parent traversal for robust qualified name resolution
// across all languages, replacing the old currentClassName state machine.
func (e *Extractor) processSymbolMatch(
	query *sitter.Query,
	match *sitter.QueryMatch,
	source []byte,
	adapter LanguageAdapter,
	repoName string,
	filePath string,
) *models.Symbol {
	var name, body string
	var kind string
	var outerNode *sitter.Node

	for _, capture := range match.Captures {
		captureName := query.CaptureNameForId(capture.Index)

		switch captureName {
		case "symbol.name":
			if c, ok := safeNodeContent(capture.Node, source); ok {
				name = c
			}
		case "symbol.body":
			if c, ok := safeNodeContent(capture.Node, source); ok {
				body = c
			}
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

	// Build qualified name using AST parent traversal.
	qualifiedName := name
	if kind == "method" && adapter.Name() == "go" {
		// Go methods: extract receiver type from method_declaration node.
		if receiver, ok := resolveGoReceiver(outerNode, source); ok {
			qualifiedName = receiver + "." + name
		}
	} else if kind == "method" || kind == "function" {
		// All languages: walk up AST to find enclosing class/struct/impl.
		if className, ok := resolveParentClass(outerNode, source); ok {
			qualifiedName = className + "." + name
			// Promote function to method if inside a class container.
			if kind == "function" {
				kind = "method"
			}
		}
	}

	// Use the outer node (full declaration) as the code block.
	codeBlock, ok := safeNodeContent(outerNode, source)
	if !ok || codeBlock == "" {
		codeBlock = body
	}
	if codeBlock == "" {
		return nil
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
		content, ok := safeNodeContent(capture.Node, source)
		if !ok {
			slog.Debug("indexer: skipping capture with out-of-bounds node",
				"file", filePath, "capture", captureName)
			continue
		}

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
