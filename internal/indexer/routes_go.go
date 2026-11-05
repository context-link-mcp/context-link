package indexer

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	sitter "github.com/smacker/go-tree-sitter"
)

// Go route extraction. A call X.Verb(...) is a route registration only when
// X is a router of a framework the file imports (see goRouterPackages):
//
//   - a constructor call (gin.Default(), echo.New(), chi.NewRouter(),
//     http.NewServeMux()) or http.DefaultServeMux;
//   - a variable assigned one of those, or a group of one (r.Group("/api"),
//     chained: v1 := api.Group("/v1"); chi r.With(mw), r.Group(fn));
//   - a parameter or struct field of a router type (*gin.Engine,
//     *gin.RouterGroup, *echo.Echo, *echo.Group, chi.Router, *http.ServeMux);
//   - the net/http package itself, for http.Handle/http.HandleFunc.
//
// So http.Get(url), client.Get(...) and cache.Get("k") are never routes: the
// receiver is a package without that function, or a value that isn't a
// router. The handler argument must also be a handler (a name, selector,
// func literal or wrapper call), not a string or number.
//
// Prefixes come from the same file only: a group variable's chain, and for
// chi the r.Route("/p", func(r chi.Router) {...}) callbacks around the call
// (its AST ancestry). Routers passed in from other files have no prefix.

// goRouterPackage describes one Go HTTP router package.
type goRouterPackage struct {
	framework    string
	constructors map[string]bool // pkg.Func() returning a router
	types        map[string]bool // pkg.Type of a router or group
}

// goRouterPackages maps import paths (without a /vN suffix) to routers.
var goRouterPackages = map[string]goRouterPackage{
	"github.com/gin-gonic/gin": {
		framework:    "gin",
		constructors: map[string]bool{"New": true, "Default": true},
		types:        map[string]bool{"Engine": true, "RouterGroup": true, "IRouter": true, "IRoutes": true},
	},
	"github.com/labstack/echo": {
		framework:    "echo",
		constructors: map[string]bool{"New": true},
		types:        map[string]bool{"Echo": true, "Group": true},
	},
	"github.com/go-chi/chi": {
		framework:    "chi",
		constructors: map[string]bool{"NewRouter": true, "NewMux": true},
		types:        map[string]bool{"Router": true, "Mux": true},
	},
	"net/http": {
		framework:    "net/http",
		constructors: map[string]bool{"NewServeMux": true},
		types:        map[string]bool{"ServeMux": true},
	},
}

// goVerb says how to read one registration method's arguments.
type goVerb struct {
	method    string // fixed method ("GET", "ALL"); empty: read argument methodArg
	methodArg int    // argument holding a method string, or a []string of methods
	pathArg   int    // argument holding the path
	handler   int    // argument holding the handler; -1 for the last argument
	pattern   bool   // the path may be a Go 1.22 "METHOD [host]/path" pattern
}

// goVerbs are the registration methods per framework.
var goVerbs = map[string]map[string]goVerb{
	"gin": withVerbs(map[string]goVerb{
		"Any":    {method: "ALL", handler: -1},
		"Handle": {methodArg: 0, pathArg: 1, handler: -1},
		"Match":  {methodArg: 0, pathArg: 1, handler: -1},
	}, strings.ToUpper, -1, "GET", "POST", "PUT", "DELETE", "PATCH", "HEAD", "OPTIONS"),
	"echo": withVerbs(map[string]goVerb{
		"Any":   {method: "ALL", handler: 1},
		"Add":   {methodArg: 0, pathArg: 1, handler: 2},
		"Match": {methodArg: 0, pathArg: 1, handler: 2},
	}, strings.ToUpper, 1, "GET", "POST", "PUT", "DELETE", "PATCH", "HEAD", "OPTIONS", "CONNECT", "TRACE"),
	"chi": withVerbs(map[string]goVerb{
		"Handle":     {method: "ALL", handler: 1, pattern: true},
		"HandleFunc": {method: "ALL", handler: 1, pattern: true},
		"Method":     {methodArg: 0, pathArg: 1, handler: 2},
		"MethodFunc": {methodArg: 0, pathArg: 1, handler: 2},
	}, func(m string) string { return m[:1] + strings.ToLower(m[1:]) }, 1,
		"GET", "POST", "PUT", "DELETE", "PATCH", "HEAD", "OPTIONS", "CONNECT", "TRACE"),
	"net/http": {
		"Handle":     {method: "ALL", handler: 1, pattern: true},
		"HandleFunc": {method: "ALL", handler: 1, pattern: true},
	},
}

// withVerbs adds one fixed-method entry per HTTP method to verbs, named by
// name(method) ("GET" for Gin, "Get" for chi).
func withVerbs(verbs map[string]goVerb, name func(string) string, handler int, methods ...string) map[string]goVerb {
	for _, m := range methods {
		verbs[name(m)] = goVerb{method: m, handler: handler}
	}
	return verbs
}

// goAnyVerb is every registration method name, for a quick first filter.
var goAnyVerb = func() map[string]bool {
	out := make(map[string]bool)
	for _, verbs := range goVerbs {
		for v := range verbs {
			out[v] = true
		}
	}
	return out
}()

// goMajorVersion matches a module path's major version suffix (/v5).
var goMajorVersion = regexp.MustCompile(`/v[0-9]+$`)

// goMaxRouterDepth bounds how far router expressions are followed.
const goMaxRouterDepth = 16

// goRouter is a router value: its framework and the prefix of its group.
type goRouter struct {
	framework string
	prefix    string
}

// goBinding is one assignment of an expression to a variable or field.
type goBinding struct {
	value *sitter.Node
	scope *sitter.Node // enclosing function; nil at package level
	end   uint32       // end of the assignment: visible after it
}

// goRouteFile holds what one Go file's route extraction needs.
type goRouteFile struct {
	source   []byte
	pkgs     map[string]goRouterPackage // import name → router package
	bindings map[string][]goBinding     // variable ("r") or field ("s.router") → assignments
	fields   map[string]string          // router-typed struct field → framework
}

// goRoutes extracts the Gin, Echo, chi and net/http routes of one Go file.
func goRoutes(
	ctx context.Context,
	tree *sitter.Tree,
	source []byte,
	adapter LanguageAdapter,
	queryBytes []byte,
	filePath string,
) ([]ExtractedRoute, error) {
	f := &goRouteFile{
		source:   source,
		pkgs:     goRouterImports(tree.RootNode(), source),
		bindings: make(map[string][]goBinding),
		fields:   make(map[string]string),
	}
	if len(f.pkgs) == 0 {
		return nil, nil // no router framework imported: no routes
	}

	query, err := sitter.NewQuery(queryBytes, adapter.GetLanguage())
	if err != nil {
		return nil, fmt.Errorf("indexer: failed to compile route query for %s: %w", adapter.Name(), err)
	}
	defer query.Close()
	cursor := sitter.NewQueryCursor()
	defer cursor.Close()
	cursor.Exec(query, tree.RootNode())

	// Collect every binding before looking at calls: a router can be
	// assigned below a use in source order (package-level vars, fields).
	type candidate struct{ call, receiver, verb *sitter.Node }
	var calls []candidate
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
		switch {
		case caps["route.call"] != nil:
			if goAnyVerb[f.text(caps["route.verb"])] {
				calls = append(calls, candidate{caps["route.call"], caps["route.receiver"], caps["route.verb"]})
			}
		case caps["prefix.var"] != nil:
			f.bind(caps["prefix.var"], caps["prefix.value"])
		case caps["router.field"] != nil:
			if fw, ok := f.routerType(caps["router.type"]); ok {
				f.fields[f.text(caps["router.field"])] = fw
			}
		}
	}

	var routes []ExtractedRoute
	for _, c := range calls {
		routes = append(routes, f.routes(c.call, c.receiver, f.text(c.verb), filePath)...)
	}
	return routes, nil
}

// goRouterImports maps the names a file's imports bind to the router
// packages among them.
func goRouterImports(root *sitter.Node, source []byte) map[string]goRouterPackage {
	out := make(map[string]goRouterPackage)
	var visit func(n *sitter.Node)
	visit = func(n *sitter.Node) {
		for i := 0; i < int(n.NamedChildCount()); i++ {
			child := n.NamedChild(i)
			switch child.Type() {
			case "import_declaration", "import_spec_list":
				visit(child)
			case "import_spec":
				pathNode := child.ChildByFieldName("path")
				if pathNode == nil {
					continue
				}
				raw, _ := safeNodeContent(pathNode, source)
				importPath := strings.Trim(raw, "\"`")
				pkg, ok := goRouterPackages[goMajorVersion.ReplaceAllString(importPath, "")]
				if !ok {
					continue
				}
				name := goPackageName(importPath)
				if alias := child.ChildByFieldName("name"); alias != nil {
					name, _ = safeNodeContent(alias, source)
				}
				if name != "_" && name != "." && name != "" {
					out[name] = pkg
				}
			}
		}
	}
	visit(root)
	return out
}

// text returns a node's source text, "" for nil.
func (f *goRouteFile) text(n *sitter.Node) string {
	if n == nil {
		return ""
	}
	s, _ := safeNodeContent(n, f.source)
	return s
}

// bind records that value is assigned to the variable or field v.
func (f *goRouteFile) bind(v, value *sitter.Node) {
	if v == nil || value == nil {
		return
	}
	switch value.Type() {
	case "call_expression", "selector_expression", "identifier", "parenthesized_expression":
	default:
		return // literals, composites, &T{}: never a router
	}
	stmt := v.Parent()
	if stmt != nil && stmt.Type() == "expression_list" {
		stmt = stmt.Parent()
	}
	if stmt == nil {
		return
	}
	key := f.text(v)
	f.bindings[key] = append(f.bindings[key], goBinding{
		value: value,
		scope: enclosingGoFunc(v),
		end:   stmt.EndByte(),
	})
}

// enclosingGoFunc returns the innermost function, method or func literal
// around n, or nil at package level.
func enclosingGoFunc(n *sitter.Node) *sitter.Node {
	for p := n.Parent(); p != nil; p = p.Parent() {
		switch p.Type() {
		case "function_declaration", "method_declaration", "func_literal":
			return p
		}
	}
	return nil
}

// contains reports whether outer's byte range contains n.
func contains(outer, n *sitter.Node) bool {
	return n.StartByte() >= outer.StartByte() && n.EndByte() <= outer.EndByte()
}

// routerType returns the framework of a router type expression (*gin.Engine,
// chi.Router), or false for any other type.
func (f *goRouteFile) routerType(t *sitter.Node) (string, bool) {
	for t != nil && (t.Type() == "pointer_type" || t.Type() == "parenthesized_type") {
		t = t.NamedChild(0)
	}
	if t == nil || t.Type() != "qualified_type" {
		return "", false
	}
	pkg, ok := f.pkgs[f.text(t.ChildByFieldName("package"))]
	if !ok || !pkg.types[f.text(t.ChildByFieldName("name"))] {
		return "", false
	}
	return pkg.framework, true
}

// router evaluates an expression used as a receiver to a router, if it is one.
func (f *goRouteFile) router(n *sitter.Node, depth int) (goRouter, bool) {
	if n == nil || depth > goMaxRouterDepth {
		return goRouter{}, false
	}
	switch n.Type() {
	case "parenthesized_expression":
		return f.router(n.NamedChild(0), depth+1)

	case "identifier":
		name := f.text(n)
		if fn, typ := goParamDecl(n, name, f.source); fn != nil {
			fw, ok := f.routerType(typ)
			if !ok {
				return goRouter{}, false // a parameter of another type
			}
			return goRouter{framework: fw, prefix: f.callbackPrefix(fn, depth)}, true
		}
		return f.bound(n, name, depth)

	case "selector_expression":
		operand, field := n.ChildByFieldName("operand"), f.text(n.ChildByFieldName("field"))
		if pkg, ok := f.pkgs[f.text(operand)]; ok && operand.Type() == "identifier" {
			if pkg.framework == "net/http" && field == "DefaultServeMux" {
				return goRouter{framework: "net/http"}, true
			}
			return goRouter{}, false
		}
		if r, ok := f.bound(n, f.text(n), depth); ok {
			return r, true
		}
		if fw, ok := f.fields[field]; ok {
			return goRouter{framework: fw}, true
		}
		return goRouter{}, false

	case "call_expression":
		fn := n.ChildByFieldName("function")
		if fn == nil || fn.Type() != "selector_expression" {
			return goRouter{}, false
		}
		operand, method := fn.ChildByFieldName("operand"), f.text(fn.ChildByFieldName("field"))
		if pkg, ok := f.pkgs[f.text(operand)]; ok && operand.Type() == "identifier" {
			if pkg.constructors[method] {
				return goRouter{framework: pkg.framework}, true
			}
			return goRouter{}, false
		}
		base, ok := f.router(operand, depth+1)
		if !ok {
			return goRouter{}, false
		}
		args := goArgs(n)
		switch {
		case (base.framework == "gin" || base.framework == "echo") && method == "Group",
			base.framework == "chi" && method == "Route":
			if len(args) == 0 {
				return goRouter{}, false
			}
			p, ok := goStringLiteral(args[0], f.source)
			if !ok {
				return goRouter{}, false // dynamic prefix: the paths would be wrong
			}
			return goRouter{framework: base.framework, prefix: joinRoutePath(base.prefix, p)}, true
		case base.framework == "gin" && method == "Use",
			base.framework == "chi" && (method == "With" || method == "Group"):
			return base, true
		}
	}
	return goRouter{}, false
}

// bound evaluates the assignment to a variable or field that is visible at n.
// A variable's is the last one before n in a function around n (or at
// package level); a field's (s.router) is the last one anywhere in the file.
func (f *goRouteFile) bound(n *sitter.Node, key string, depth int) (goRouter, bool) {
	var best *goBinding
	isField := strings.Contains(key, ".")
	for i := range f.bindings[key] {
		b := &f.bindings[key][i]
		if !isField {
			if b.scope != nil && !contains(b.scope, n) {
				continue
			}
			if b.scope != nil && b.end > n.StartByte() {
				continue
			}
		}
		if best == nil || b.end > best.end {
			best = b
		}
	}
	if best == nil {
		return goRouter{}, false
	}
	return f.router(best.value, depth+1)
}

// callbackPrefix returns the prefix a func literal's router parameter has:
// for a chi r.Route("/p", func(r chi.Router) {...}) callback, the prefix of
// r plus "/p"; for r.Group(func(r chi.Router) {...}), the prefix of r.
// Anything else (a function taking a router) has none.
func (f *goRouteFile) callbackPrefix(fn *sitter.Node, depth int) string {
	if fn.Type() != "func_literal" {
		return ""
	}
	args := fn.Parent()
	if args == nil || args.Type() != "argument_list" {
		return ""
	}
	call := args.Parent()
	if call == nil || call.Type() != "call_expression" {
		return ""
	}
	sel := call.ChildByFieldName("function")
	if sel == nil || sel.Type() != "selector_expression" {
		return ""
	}
	base, ok := f.router(sel.ChildByFieldName("operand"), depth+1)
	if !ok || base.framework != "chi" {
		return ""
	}
	switch f.text(sel.ChildByFieldName("field")) {
	case "Route":
		if a := goArgs(call); len(a) == 2 && a[1].Equal(fn) {
			if p, ok := goStringLiteral(a[0], f.source); ok {
				return joinRoutePath(base.prefix, p)
			}
		}
	case "Group":
		return base.prefix
	}
	return ""
}

// goParamDecl finds the function around n that declares a parameter (or
// method receiver) called name, and that parameter's type node. It returns a
// nil function when no enclosing function declares it.
func goParamDecl(n *sitter.Node, name string, source []byte) (fn, typ *sitter.Node) {
	for p := enclosingGoFunc(n); p != nil; p = enclosingGoFunc(p) {
		lists := []*sitter.Node{p.ChildByFieldName("parameters")}
		if p.Type() == "method_declaration" {
			lists = append(lists, p.ChildByFieldName("receiver"))
		}
		for _, list := range lists {
			if list == nil {
				continue
			}
			for i := 0; i < int(list.NamedChildCount()); i++ {
				decl := list.NamedChild(i)
				if decl.Type() != "parameter_declaration" && decl.Type() != "variadic_parameter_declaration" {
					continue
				}
				for j := 0; j < int(decl.ChildCount()); j++ {
					if decl.FieldNameForChild(j) != "name" {
						continue
					}
					if s, _ := safeNodeContent(decl.Child(j), source); s == name {
						return p, decl.ChildByFieldName("type")
					}
				}
			}
		}
	}
	return nil, nil
}

// goArgs returns a call's arguments, without comments.
func goArgs(call *sitter.Node) []*sitter.Node {
	list := call.ChildByFieldName("arguments")
	if list == nil {
		return nil
	}
	var out []*sitter.Node
	for i := 0; i < int(list.NamedChildCount()); i++ {
		if a := list.NamedChild(i); a.Type() != "comment" {
			out = append(out, a)
		}
	}
	return out
}

// goStringLiteral returns the value of a Go string literal ("..." or `...`).
func goStringLiteral(n *sitter.Node, source []byte) (string, bool) {
	if n == nil {
		return "", false
	}
	lit, ok := safeNodeContent(n, source)
	if !ok {
		return "", false
	}
	switch n.Type() {
	case "interpreted_string_literal":
		s, err := strconv.Unquote(lit)
		return s, err == nil
	case "raw_string_literal":
		return strings.TrimSuffix(strings.TrimPrefix(lit, "`"), "`"), len(lit) >= 2
	}
	return "", false
}

// routes builds the routes of one candidate call, or none when it isn't a
// registration on a router.
func (f *goRouteFile) routes(call, receiver *sitter.Node, verb, filePath string) []ExtractedRoute {
	var r goRouter
	ok := false
	if pkg, isPkg := f.pkgs[f.text(receiver)]; isPkg && receiver.Type() == "identifier" {
		// http.HandleFunc registers on the default mux; any other
		// package-level function (http.Get, gin.New) is not a route.
		if pkg.framework != "net/http" {
			return nil
		}
		r, ok = goRouter{framework: "net/http"}, true
	} else {
		r, ok = f.router(receiver, 0)
	}
	if !ok {
		return nil
	}
	spec, ok := goVerbs[r.framework][verb]
	if !ok {
		return nil
	}

	args := goArgs(call)
	handlerArg := spec.handler
	if handlerArg < 0 {
		handlerArg = len(args) - 1
	}
	if handlerArg <= spec.pathArg || handlerArg >= len(args) {
		return nil // a route needs a path and a handler after it
	}
	path, ok := goStringLiteral(args[spec.pathArg], f.source)
	if !ok {
		return nil // dynamic path
	}

	methods := []string{spec.method}
	if spec.method == "" {
		methods = f.methods(args[spec.methodArg])
		if len(methods) == 0 {
			return nil
		}
	}
	if spec.pattern {
		var m string
		if m, path, ok = goPattern(path); !ok {
			return nil
		}
		if m != "" {
			methods = []string{m}
		}
	}
	if path != "" && !strings.HasPrefix(path, "/") {
		return nil
	}

	handler, qualifier, ok := f.handler(args[handlerArg])
	if !ok {
		return nil // a string, number or nil: an HTTP client call or a lookup
	}
	full := joinRoutePath(r.prefix, path)
	if full == "" {
		return nil
	}
	line := int(call.StartPoint().Row) + 1
	out := make([]ExtractedRoute, 0, len(methods))
	for _, m := range methods {
		out = append(out, ExtractedRoute{
			Method:           m,
			PathPattern:      full,
			Handler:          handler,
			HandlerQualifier: qualifier,
			Framework:        r.framework,
			FilePath:         filePath,
			StartLine:        line,
		})
	}
	return out
}

// goPattern splits a net/http (Go 1.22) or chi pattern "[METHOD ][HOST]/path"
// into its method ("" when absent) and path, dropping the host.
func goPattern(p string) (method, path string, ok bool) {
	fields := strings.Fields(p)
	switch len(fields) {
	case 0:
		return "", "", false
	case 1:
		path = fields[0]
	case 2:
		method, path = strings.ToUpper(fields[0]), fields[1]
	default:
		return "", "", false
	}
	if !strings.HasPrefix(path, "/") {
		i := strings.IndexByte(path, '/')
		if i < 0 {
			return "", "", false
		}
		path = path[i:] // example.com/x → /x
	}
	return method, path, true
}

// methods reads a method argument: "GET", http.MethodGet, or a
// []string{...} of those. Unknown forms give none.
func (f *goRouteFile) methods(n *sitter.Node) []string {
	switch n.Type() {
	case "interpreted_string_literal", "raw_string_literal":
		if s, ok := goStringLiteral(n, f.source); ok && s != "" {
			return []string{strings.ToUpper(s)}
		}
	case "selector_expression":
		field := f.text(n.ChildByFieldName("field"))
		if pkg, ok := f.pkgs[f.text(n.ChildByFieldName("operand"))]; ok && pkg.framework == "net/http" &&
			strings.HasPrefix(field, "Method") && len(field) > len("Method") {
			return []string{strings.ToUpper(strings.TrimPrefix(field, "Method"))}
		}
	case "composite_literal":
		body := n.ChildByFieldName("body")
		if body == nil {
			return nil
		}
		var out []string
		for i := 0; i < int(body.NamedChildCount()); i++ {
			el := body.NamedChild(i)
			if el.Type() == "literal_element" || el.Type() == "element" {
				el = el.NamedChild(0)
			}
			if el == nil {
				continue
			}
			out = append(out, f.methods(el)...)
		}
		return out
	}
	return nil
}

// handler reads a handler argument: a name (listUsers), a selector
// (handlers.List, h.List: the field, with its receiver as qualifier), or an
// inline or wrapped handler with no name. http.HandlerFunc(x) is unwrapped.
// ok is false for anything that isn't a handler.
func (f *goRouteFile) handler(n *sitter.Node) (name, qualifier string, ok bool) {
	switch n.Type() {
	case "identifier":
		switch s := f.text(n); s {
		case "nil", "true", "false", "iota":
			return "", "", false
		default:
			return s, "", true
		}
	case "selector_expression":
		return f.text(n.ChildByFieldName("field")), f.text(n.ChildByFieldName("operand")), true
	case "call_expression":
		if fn := n.ChildByFieldName("function"); fn != nil && fn.Type() == "selector_expression" {
			pkg, isPkg := f.pkgs[f.text(fn.ChildByFieldName("operand"))]
			if isPkg && pkg.framework == "net/http" && f.text(fn.ChildByFieldName("field")) == "HandlerFunc" {
				if args := goArgs(n); len(args) == 1 {
					return f.handler(args[0])
				}
			}
		}
		return "", "", true // a wrapper: middleware(h), gin.WrapF(h)
	case "func_literal", "unary_expression", "composite_literal", "parenthesized_expression",
		"index_expression", "generic_type":
		return "", "", true
	}
	return "", "", false
}
