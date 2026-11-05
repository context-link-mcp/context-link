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
// Grammars without a name field (Kotlin) fall back to the node's first
// type_identifier child, see containerNameNode.
var classLikeNodeTypes = map[string]string{
	"class_definition":           "name", // Python
	"class_declaration":          "name", // TypeScript, JavaScript, Java, C#, Swift; Kotlin (positional)
	"abstract_class_declaration": "name", // TypeScript
	"interface_declaration":      "name", // Java, C# (default methods)
	"enum_declaration":           "name", // Java, C#
	"record_declaration":         "name", // Java, C#
	"struct_declaration":         "name", // C#
	"object_declaration":         "name", // Kotlin (positional)
	"impl_item":                  "type", // Rust
	"trait_item":                 "name", // Rust (default methods)
	"class_specifier":            "name", // C++
	"struct_specifier":           "name", // C++
}

// resolveParentClass walks up the AST from node to find an enclosing class,
// struct, or impl container. Returns the container name and true if found.
func resolveParentClass(node *sitter.Node, source []byte) (string, bool) {
	current := node.Parent()
	for current != nil {
		if nameField, ok := classLikeNodeTypes[current.Type()]; ok {
			if nameNode := containerNameNode(current, nameField); nameNode != nil {
				if content := typeName(nameNode, source); content != "" {
					return content, true
				}
			}
		}
		current = current.Parent()
	}
	return "", false
}

// containerNameNode returns the node holding a container's name: its
// nameField child, or for grammars that expose no fields (Kotlin), its first
// type_identifier child.
func containerNameNode(container *sitter.Node, nameField string) *sitter.Node {
	if n := container.ChildByFieldName(nameField); n != nil {
		return n
	}
	for i := 0; i < int(container.NamedChildCount()); i++ {
		if c := container.NamedChild(i); c.Type() == "type_identifier" {
			return c
		}
	}
	return nil
}

// typeName returns the bare name of a type node, without pointer, type
// arguments or path: *Cache[K, V] (Go), Foo<T> and a::Foo (Rust) all give the
// last plain type name ("Cache", "Foo").
func typeName(node *sitter.Node, source []byte) string {
	for node != nil {
		switch node.Type() {
		case "pointer_type", "reference_type":
			if node.NamedChildCount() == 0 {
				return ""
			}
			node = node.NamedChild(int(node.NamedChildCount()) - 1)
			continue
		case "generic_type":
			if t := node.ChildByFieldName("type"); t != nil {
				node = t
				continue
			}
		case "scoped_type_identifier", "template_type":
			if n := node.ChildByFieldName("name"); n != nil {
				node = n
				continue
			}
		}
		break
	}
	if node == nil {
		return ""
	}
	content, ok := safeNodeContent(node, source)
	if !ok {
		return ""
	}
	// Grammars that don't split type arguments into their own node.
	if i := strings.IndexAny(content, "[<"); i >= 0 {
		content = content[:i]
	}
	return strings.TrimSpace(content)
}

// cppScopedName splits a C++ qualified_identifier (Foo::bar, ns::Foo::bar,
// Foo<T>::bar, Foo::~Foo, Foo::operator==) into the last scope segment and the
// member name: ("Foo", "bar"), ("Foo", "~Foo"), ("Foo", "operator=="). Only
// the last segment is kept, so an out-of-class definition gets the same
// qualified name (Foo.bar) as a member defined in the class body, and the
// same container naming as the other languages; namespaces are dropped.
// Template arguments are dropped (typeName). The grammar can't tell a class
// scope from a namespace, so ns::helper gives ("ns", "helper") as well. The
// scope is "" for a global-scope name (::foo) or a node of another type.
func cppScopedName(node *sitter.Node, source []byte) (scope, name string) {
	for node != nil && node.Type() == "qualified_identifier" {
		if s := node.ChildByFieldName("scope"); s != nil {
			scope = typeName(s, source)
		}
		node = node.ChildByFieldName("name")
	}
	if node == nil {
		return "", ""
	}
	if node.Type() == "template_function" || node.Type() == "template_method" {
		if n := node.ChildByFieldName("name"); n != nil {
			node = n // Foo::bar<int> → bar
		}
	}
	name, _ = safeNodeContent(node, source)
	return scope, name
}

// resolveGoReceiver extracts the receiver type from a Go method_declaration node.
// For example, `func (c *Cache) Get()` and `func (c *Cache[K, V]) Get()` both
// return "Cache".
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
		// *Foo, Foo[K, V] and *Foo[K, V] are all methods of Foo.
		if content := typeName(typeNode, source); content != "" {
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
	seen := make(map[symKey]int) // key → index in symbols

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
			if i, dup := seen[key]; dup {
				// A generic pattern (exported variable) and a specific one
				// (exported arrow function) can match the same declaration;
				// keep the specific kind whichever match comes first.
				if symbols[i].Kind == "variable" && sym.Kind != "variable" {
					symbols[i] = *sym
				}
				continue
			}
			seen[key] = len(symbols)
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
	scope := "" // explicit qualifier of the name (C++ Foo::bar), see cppScopedName

	for _, capture := range match.Captures {
		captureName := query.CaptureNameForId(capture.Index)

		switch captureName {
		case "symbol.name":
			if capture.Node.Type() == "qualified_identifier" {
				scope, name = cppScopedName(capture.Node, source)
			} else if c, ok := safeNodeContent(capture.Node, source); ok {
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
	} else if scope != "" {
		// C++ out-of-class definition: int Foo::bar() {} is Foo.bar. The kind
		// stays what the query says (method), also for a namespace function
		// (ns::helper), which the grammar can't tell from a class member.
		qualifiedName = scope + "." + name
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
	Line       int    // 1-based line of the capture, used to find the calling symbol
	// Qualifier is, for a method or selector call, its receiver: a dotted
	// name such as "s", "self.client" or "os.path", or "*" for any other
	// expression; it is empty for a plain call. For an import it is the local
	// name the import binds (Go package name or alias, the dotted path of a
	// Python "import a.b", the name or alias of a Python from-import name,
	// JS/TS default or namespace import), empty when it binds none.
	Qualifier string
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

	// The receiver of a selector call is captured in the same match as the
	// method name; keep it as the call's qualifier.
	receiver := ""
	for _, capture := range match.Captures {
		if query.CaptureNameForId(capture.Index) == "dependency.call_object" {
			if content, ok := safeNodeContent(capture.Node, source); ok {
				receiver = normalizeReceiver(content)
			}
		}
	}

	for _, capture := range match.Captures {
		captureName := query.CaptureNameForId(capture.Index)
		content, ok := safeNodeContent(capture.Node, source)
		if !ok {
			slog.Debug("indexer: skipping capture with out-of-bounds node",
				"file", filePath, "capture", captureName)
			continue
		}
		line := int(capture.Node.StartPoint().Row) + 1

		switch captureName {
		case "dependency.source":
			// Import source — strip quotes.
			importPath := strings.Trim(content, "\"'`")
			deps = append(deps, ExtractedDep{
				Kind:       "import",
				Source:     importPath,
				CallerFile: filePath,
				Line:       line,
				Qualifier:  importBinding(capture.Node, source, importPath),
			})
			// Python "from m import a, b as c": one more import per name,
			// with the source m.a and the local name it binds.
			if parent := capture.Node.Parent(); parent != nil && parent.Type() == "import_from_statement" {
				for _, n := range pythonImportedNames(parent, source) {
					sep := "."
					if strings.HasSuffix(importPath, ".") {
						sep = "" // from . import x → .x
					}
					deps = append(deps, ExtractedDep{
						Kind:       "import",
						Source:     importPath + sep + n.name,
						CallerFile: filePath,
						Line:       line,
						Qualifier:  n.binding,
					})
				}
			}

		case "dependency.call":
			if capture.Node.Type() == "qualified_identifier" {
				// C++ Foo::bar() and ns::Foo::bar() call Foo.bar, the
				// qualified name the definition gets (cppScopedName), so they
				// resolve by it; std::sort() looks for std.sort and links
				// nothing unless the repo defines one.
				scope, name := cppScopedName(capture.Node, source)
				if name == "" {
					continue
				}
				if scope != "" {
					name = scope + "." + name
				}
				content = name
			}
			deps = append(deps, ExtractedDep{
				Kind:       "call",
				Symbol:     content,
				CallerFile: filePath,
				Line:       line,
			})

		case "dependency.call_method":
			// The method/function name of a selector call (s.Load(),
			// pkg.Func(), self.helper()); the receiver is not a callee but
			// scopes the resolution.
			q := receiver
			if q == "" {
				q = "*"
			}
			deps = append(deps, ExtractedDep{
				Kind:       "call",
				Symbol:     content,
				CallerFile: filePath,
				Line:       line,
				Qualifier:  q,
			})

		case "dependency.extends":
			deps = append(deps, ExtractedDep{
				Kind:       "extends",
				Symbol:     content,
				CallerFile: filePath,
				Line:       line,
			})

		case "dependency.implements":
			deps = append(deps, ExtractedDep{
				Kind:       "implements",
				Symbol:     content,
				CallerFile: filePath,
				Line:       line,
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

// normalizeReceiver returns a call receiver as written when it is a dotted
// name (s, self.client, os.path), and "*" for any other expression.
func normalizeReceiver(text string) string {
	if text == "" {
		return "*"
	}
	for _, part := range strings.Split(text, ".") {
		if !isIdentifier(part) {
			return "*"
		}
	}
	return text
}

// isIdentifier reports whether s is a plain ASCII identifier ($ allowed for JS).
func isIdentifier(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		switch {
		case r == '_' || r == '$' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z'):
		case i > 0 && r >= '0' && r <= '9':
		default:
			return false
		}
	}
	return true
}

// pythonImportedName is one name of a Python from-import.
type pythonImportedName struct {
	name    string // the imported name
	binding string // the local name: the alias, or the name itself
}

// pythonImportedNames returns the names a Python import_from_statement
// imports ("from m import a, b as c"); none for "from m import *".
func pythonImportedNames(stmt *sitter.Node, source []byte) []pythonImportedName {
	var out []pythonImportedName
	for i := 0; i < int(stmt.ChildCount()); i++ {
		if stmt.FieldNameForChild(i) != "name" {
			continue
		}
		child := stmt.Child(i)
		nameNode, aliasNode := child, child
		if child.Type() == "aliased_import" {
			nameNode, aliasNode = child.ChildByFieldName("name"), child.ChildByFieldName("alias")
		}
		if nameNode == nil || aliasNode == nil {
			continue
		}
		name, ok1 := safeNodeContent(nameNode, source)
		binding, ok2 := safeNodeContent(aliasNode, source)
		if ok1 && ok2 && name != "" && binding != "" {
			out = append(out, pythonImportedName{name: name, binding: binding})
		}
	}
	return out
}

// importBinding returns the local name an import binds, from the import
// source node: a Go alias or the package name guessed from the path, the
// dotted path of a Python "import a.b" (which makes a and a.b usable), or a
// JS/TS default or namespace import. It returns "" for imports that bind no
// single name (the module of a Python from-import, whose names get imports of
// their own, JS named imports, Go blank and dot imports, other languages).
func importBinding(node *sitter.Node, source []byte, importPath string) string {
	parent := node.Parent()
	if parent == nil {
		return ""
	}
	switch parent.Type() {
	case "import_spec": // Go
		if alias := parent.ChildByFieldName("name"); alias != nil {
			name, ok := safeNodeContent(alias, source)
			if !ok || name == "_" || name == "." {
				return ""
			}
			return name
		}
		return goPackageName(importPath)
	case "import_statement":
		if node.Type() == "dotted_name" { // Python: import a.b makes a, a.b usable
			return importPath
		}
		// JS/TS: import x from '...' or import * as x from '...'.
		for i := 0; i < int(parent.NamedChildCount()); i++ {
			clause := parent.NamedChild(i)
			if clause.Type() != "import_clause" {
				continue
			}
			for j := 0; j < int(clause.NamedChildCount()); j++ {
				c := clause.NamedChild(j)
				switch c.Type() {
				case "identifier":
					name, _ := safeNodeContent(c, source)
					return name
				case "namespace_import":
					for k := 0; k < int(c.NamedChildCount()); k++ {
						if id := c.NamedChild(k); id.Type() == "identifier" {
							name, _ := safeNodeContent(id, source)
							return name
						}
					}
				}
			}
		}
	}
	return ""
}

// goPackageName guesses the package name of a Go import path without an
// alias: the last element, skipping a major-version suffix (/v2), without a
// "go-" prefix, a "-go" suffix or a ".vN" suffix (gopkg.in/yaml.v3).
func goPackageName(importPath string) string {
	parts := strings.Split(importPath, "/")
	name := parts[len(parts)-1]
	if len(parts) > 1 && len(name) > 1 && name[0] == 'v' && strings.Trim(name[1:], "0123456789") == "" {
		name = parts[len(parts)-2]
	}
	name = strings.TrimPrefix(name, "go-")
	name = strings.TrimSuffix(name, "-go")
	if i := strings.IndexByte(name, '.'); i > 0 {
		name = name[:i]
	}
	return name
}
