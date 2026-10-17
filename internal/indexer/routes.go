package indexer

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"strings"

	sitter "github.com/smacker/go-tree-sitter"

	"github.com/context-link-mcp/context-link/internal/store"
	"github.com/context-link-mcp/context-link/pkg/models"
)

// RouteQueryProvider is implemented by language adapters that can extract
// HTTP route definitions. It is optional: adapters without it simply yield no
// routes.
type RouteQueryProvider interface {
	// GetRouteQuery returns the Scheme query for route extraction. The
	// captures it uses are documented in the adapter's *_routes.scm file.
	GetRouteQuery() []byte
}

// ExtractedRoute is a raw HTTP route definition found in one file. The handler
// is a symbol name; the indexer resolves it to a symbol ID after storing.
type ExtractedRoute struct {
	Method      string // upper-case HTTP method, or "ALL"
	PathPattern string // path as written, with any router prefix applied
	Handler     string // handler function name, empty for inline/anonymous handlers
	Framework   string // "fastapi", "flask", "express", ...
	FilePath    string
	StartLine   int // 1-based
}

// pythonRouteVerbs maps decorator attribute names to HTTP methods. "route" and
// "api_route" take their methods from a methods=[...] keyword instead.
var pythonRouteVerbs = map[string]string{
	"get": "GET", "post": "POST", "put": "PUT", "delete": "DELETE",
	"patch": "PATCH", "head": "HEAD", "options": "OPTIONS",
}

// jsRouteVerbs are the call names treated as route registration in JS/TS.
var jsRouteVerbs = map[string]string{
	"get": "GET", "post": "POST", "put": "PUT", "delete": "DELETE",
	"patch": "PATCH", "head": "HEAD", "options": "OPTIONS", "all": "ALL",
}

// httpClientReceivers are receiver names of HTTP clients. `axios.get("/x")`
// looks like a route registration but is a call, so these are skipped.
var httpClientReceivers = map[string]bool{
	"axios": true, "http": true, "https": true, "httpclient": true, "fetch": true,
	"request": true, "client": true, "api": true, "got": true, "ky": true,
	"superagent": true, "supertest": true, "agent": true, "$": true, "map": true,
	"cache": true, "params": true, "headers": true, "req": true, "res": true,
}

// routerPrefixKeys are the keyword arguments that carry a router-level prefix.
var routerPrefixKeys = map[string]string{
	"APIRouter": "prefix",
	"Blueprint": "url_prefix",
}

var quotedStringPattern = regexp.MustCompile(`["']([A-Za-z]+)["']`)

// unquoteStringLiteral strips a string literal's prefix (r, b, f, u), quotes
// and triple quotes. Returns false for anything that isn't a plain literal.
func unquoteStringLiteral(lit string) (string, bool) {
	lit = strings.TrimLeft(lit, "rRbBfFuU")
	for _, q := range []string{`"""`, `'''`, `"`, `'`} {
		if strings.HasPrefix(lit, q) && strings.HasSuffix(lit, q) && len(lit) >= 2*len(q) {
			return lit[len(q) : len(lit)-len(q)], true
		}
	}
	return "", false
}

// joinRoutePath joins a router prefix and a route path with exactly one slash.
func joinRoutePath(prefix, path string) string {
	if prefix == "" {
		return path
	}
	if path == "" {
		return prefix
	}
	return strings.TrimRight(prefix, "/") + "/" + strings.TrimLeft(path, "/")
}

// storeRoutes persists the routes extracted in this run, linking each to its
// handler symbol (same file, matching name) when one exists. Returns the
// number of routes stored. Failures are logged, not fatal.
func (idx *Indexer) storeRoutes(
	ctx context.Context,
	repoName string,
	results []fileResult,
	symbolsByFile map[string][]models.Symbol,
) int {
	var routes []models.Route
	for _, r := range results {
		for _, er := range r.routes {
			route := models.Route{
				RepoName:       repoName,
				Method:         er.Method,
				PathPattern:    er.PathPattern,
				NormalizedPath: store.NormalizePath(er.PathPattern),
				FilePath:       er.FilePath,
				StartLine:      er.StartLine,
				Framework:      er.Framework,
				Kind:           "definition",
			}
			if er.Handler != "" {
				for _, sym := range symbolsByFile[er.FilePath] {
					if sym.Name == er.Handler && (sym.Kind == "function" || sym.Kind == "method") {
						id := sym.ID
						route.HandlerSymbolID = &id
						break
					}
				}
			}
			routes = append(routes, route)
		}
	}
	if err := store.BatchInsertRoutes(ctx, idx.db, routes); err != nil {
		slog.Warn("indexer: failed to store routes", "error", err)
		return 0
	}
	return len(routes)
}

// ExtractRoutes runs the adapter's route query and returns the HTTP route
// definitions found in the file. Adapters that don't implement
// RouteQueryProvider produce no routes.
func (e *Extractor) ExtractRoutes(
	ctx context.Context,
	tree *sitter.Tree,
	source []byte,
	adapter LanguageAdapter,
	filePath string,
) ([]ExtractedRoute, error) {
	provider, ok := adapter.(RouteQueryProvider)
	if !ok {
		return nil, nil
	}
	queryBytes := provider.GetRouteQuery()
	if len(queryBytes) == 0 {
		return nil, nil
	}

	query, err := sitter.NewQuery(queryBytes, adapter.GetLanguage())
	if err != nil {
		return nil, fmt.Errorf("indexer: failed to compile route query for %s: %w", adapter.Name(), err)
	}
	defer query.Close()

	cursor := sitter.NewQueryCursor()
	defer cursor.Close()
	cursor.Exec(query, tree.RootNode())

	prefixes := make(map[string]string) // router variable → prefix
	var routes []ExtractedRoute
	var receivers []string // parallel to routes: receiver text, for prefix lookup

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

		caps := make(map[string]*sitter.Node, len(match.Captures))
		for _, c := range match.Captures {
			caps[query.CaptureNameForId(c.Index)] = c.Node
		}
		text := func(name string) string {
			n := caps[name]
			if n == nil {
				return ""
			}
			s, _ := safeNodeContent(n, source)
			return s
		}

		if caps["prefix.var"] != nil {
			ctor := text("prefix.ctor")
			if key, ok := routerPrefixKeys[ctor]; ok && text("prefix.key") == key {
				if v, ok := unquoteStringLiteral(text("prefix.value")); ok {
					prefixes[text("prefix.var")] = v
				}
			}
			continue
		}

		if caps["route.path"] == nil {
			continue
		}
		path, ok := unquoteStringLiteral(text("route.path"))
		// An empty path is valid on a prefixed router (@router.get("")).
		if !ok || (path != "" && !strings.HasPrefix(path, "/")) {
			continue
		}

		var found []ExtractedRoute
		switch adapter.Name() {
		case "python":
			found = pythonRoutes(caps, text, source, path, filePath)
		case "javascript", "typescript", "tsx":
			found = jsRoutes(caps, text, source, path, filePath)
		}
		for _, r := range found {
			routes = append(routes, r)
			receivers = append(receivers, text("route.receiver"))
		}
	}

	// Prefixes can be declared after use in source order; apply them last.
	// Routes whose path is still empty (no prefix to give them one) are dropped.
	kept := routes[:0]
	for i, r := range routes {
		if p, ok := prefixes[receivers[i]]; ok {
			r.PathPattern = joinRoutePath(p, r.PathPattern)
		}
		if r.PathPattern != "" {
			kept = append(kept, r)
		}
	}
	return kept, nil
}

// pythonRoutes builds routes for one matched FastAPI/Flask decorator.
func pythonRoutes(
	caps map[string]*sitter.Node,
	text func(string) string,
	source []byte,
	path, filePath string,
) []ExtractedRoute {
	verb := text("route.verb")
	var methods []string
	if m, ok := pythonRouteVerbs[verb]; ok {
		methods = []string{m}
	} else if verb == "route" || verb == "api_route" {
		methods = pythonMethodsKwarg(caps["route.call"], source)
		if len(methods) == 0 {
			methods = []string{"GET"}
		}
	} else {
		return nil
	}

	framework := pythonFramework(source, verb)
	line := int(caps["route.call"].StartPoint().Row) + 1
	routes := make([]ExtractedRoute, 0, len(methods))
	for _, m := range methods {
		routes = append(routes, ExtractedRoute{
			Method:      m,
			PathPattern: path,
			Handler:     text("route.handler"),
			Framework:   framework,
			FilePath:    filePath,
			StartLine:   line,
		})
	}
	return routes
}

// pythonMethodsKwarg reads the methods=["GET", "POST"] keyword argument of a
// decorator call. Returns nil when absent.
func pythonMethodsKwarg(call *sitter.Node, source []byte) []string {
	args := call.ChildByFieldName("arguments")
	if args == nil {
		return nil
	}
	for i := 0; i < int(args.NamedChildCount()); i++ {
		arg := args.NamedChild(i)
		if arg.Type() != "keyword_argument" {
			continue
		}
		name := arg.ChildByFieldName("name")
		value := arg.ChildByFieldName("value")
		if name == nil || value == nil {
			continue
		}
		if n, _ := safeNodeContent(name, source); n != "methods" {
			continue
		}
		content, _ := safeNodeContent(value, source)
		var methods []string
		for _, m := range quotedStringPattern.FindAllStringSubmatch(content, -1) {
			methods = append(methods, strings.ToUpper(m[1]))
		}
		return methods
	}
	return nil
}

// pythonFramework guesses the framework from the file's imports.
func pythonFramework(source []byte, verb string) string {
	src := strings.ToLower(string(source))
	switch {
	case strings.Contains(src, "fastapi"):
		return "fastapi"
	case strings.Contains(src, "flask"):
		return "flask"
	case verb == "route":
		return "flask"
	default:
		return "python"
	}
}

// jsRoutes builds the route for one matched Express-style call, or nothing
// when the call looks like an HTTP client call rather than a registration.
func jsRoutes(
	caps map[string]*sitter.Node,
	text func(string) string,
	source []byte,
	path, filePath string,
) []ExtractedRoute {
	method, ok := jsRouteVerbs[text("route.verb")]
	if !ok {
		return nil
	}
	receiver := strings.ToLower(text("route.receiver"))
	if i := strings.LastIndex(receiver, "."); i >= 0 {
		receiver = receiver[i+1:]
	}
	if httpClientReceivers[receiver] {
		return nil
	}

	args := caps["route.call"].ChildByFieldName("arguments")
	if args == nil || args.NamedChildCount() < 2 {
		return nil // a route needs a path and at least one handler
	}
	last := args.NamedChild(int(args.NamedChildCount()) - 1)

	handler := ""
	switch last.Type() {
	case "identifier":
		handler, _ = safeNodeContent(last, source)
	case "member_expression":
		if prop := last.ChildByFieldName("property"); prop != nil {
			handler, _ = safeNodeContent(prop, source)
		}
	case "arrow_function", "function_expression", "function", "call_expression":
		// Inline or wrapped handler: valid route, no resolvable name.
	default:
		return nil // object/string/number argument: an HTTP client call
	}

	return []ExtractedRoute{{
		Method:      method,
		PathPattern: path,
		Handler:     handler,
		Framework:   "express",
		FilePath:    filePath,
		StartLine:   int(caps["route.call"].StartPoint().Row) + 1,
	}}
}
