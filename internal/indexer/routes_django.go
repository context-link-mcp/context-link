package indexer

import (
	"strings"

	sitter "github.com/smacker/go-tree-sitter"

	"github.com/context-link-mcp/context-link/internal/store"
)

// Django route extraction. Routes are the path(), re_path() and legacy url()
// calls in a module-level urlpatterns list (urlpatterns = [...],
// urlpatterns += [...], urlpatterns = [...] + other); a path() call anywhere
// else is not a route. Django paths have no leading slash, so "/" is added.
// Django routes declare no method: every route is ALL, class-based views too
// (one route per view, not one per get/post method).
//
// The view argument gives the handler:
//
//   - a name (index) or dotted name (views.index: Handler index, qualifier
//     views), resolved through the file's imports like a call;
//   - FooView.as_view() or views.FooView.as_view(): the class FooView;
//   - a lambda or any other call (login_required(v)): inline, no handler;
//   - include([...]) with a list: those routes, with this route's prefix;
//   - include(router.urls) or router.urls: the routes of that DRF router of
//     this file, with this route's prefix;
//   - include("app.urls") and other includes: no route. The included file's
//     routes keep their own path (prefixes are not joined across files).
//
// re_path() and url() patterns are regexes. PathPattern keeps them as
// written; the normalised path drops ^ and $ and turns named groups
// (?P<id>...) into :param (* when the group can match "/"), when the rest is
// a plain path. Otherwise the normalised path is the pattern itself, so it
// only matches exactly.
//
// DRF: router.register(r"users", UserViewSet) on a router this file assigns
// from a *Router() call (DefaultRouter, SimpleRouter, ...) gives /users/ and
// /users/<pk>/, both ALL, framework django-rest-framework, linked to the
// viewset class. They get the prefix of each include(router.urls) of this
// file, or none.

const (
	djangoFramework = "django"
	drfFramework    = "django-rest-framework"
)

// classHandlerFrameworks are the frameworks whose route handlers can be
// classes (Django class-based views, DRF viewsets). Handlers of the other
// frameworks are only ever linked to functions and methods.
var classHandlerFrameworks = map[string]bool{djangoFramework: true, drfFramework: true}

// routeHandlerKind reports whether a symbol of kind can be the handler of a
// route of framework.
func routeHandlerKind(framework, kind string) bool {
	return kind == "function" || kind == "method" || (kind == "class" && classHandlerFrameworks[framework])
}

// djangoRouteFuncs are the route functions; the value says whether the
// pattern is a regex.
var djangoRouteFuncs = map[string]bool{"path": false, "re_path": true, "url": true}

// djangoSegment is a route path, or the prefix of one: as written and
// normalised. Both start with "/" once joined to the root.
type djangoSegment struct {
	pattern, norm string
}

// join appends a child path to the prefix s.
func (s djangoSegment) join(child djangoSegment) djangoSegment {
	return djangoSegment{joinRoutePath(s.pattern, child.pattern), joinRoutePath(s.norm, child.norm)}
}

// djangoRoot is the prefix of a urlpatterns list.
var djangoRoot = djangoSegment{"/", "/"}

// drfRegistration is one router.register(prefix, viewset) call.
type drfRegistration struct {
	router, prefix     string
	handler, qualifier string
	line               int
}

// djangoRoutes collects one Python file's Django and DRF matches; routes()
// builds the routes once every match is in.
type djangoRoutes struct {
	source   []byte
	filePath string

	patterns      []*sitter.Node // urlpatterns values, in source order
	routers       map[string]bool
	registrations []drfRegistration
}

// add takes one route-query match, and reports whether it was a Django or
// DRF match (consumed) rather than a FastAPI/Flask one.
func (d *djangoRoutes) add(caps map[string]*sitter.Node, text func(string) string) bool {
	switch {
	case caps["django.var"] != nil:
		if text("django.var") == "urlpatterns" {
			d.patterns = append(d.patterns, caps["django.value"])
		}
	case caps["drf.var"] != nil:
		ctor := text("drf.ctor")
		if i := strings.LastIndexByte(ctor, '.'); i >= 0 {
			ctor = ctor[i+1:]
		}
		// FastAPI's APIRouter has no register(); keep it out.
		if strings.HasSuffix(ctor, "Router") && ctor != "APIRouter" && isIdentifier(ctor) {
			if d.routers == nil {
				d.routers = make(map[string]bool)
			}
			d.routers[text("drf.var")] = true
		}
	case caps["drf.call"] != nil:
		if text("drf.verb") == "register" {
			d.register(text("drf.receiver"), caps["drf.call"])
		}
	default:
		return false
	}
	return true
}

// register records a router.register(prefix, viewset) call.
func (d *djangoRoutes) register(router string, call *sitter.Node) {
	pos, kw := pythonArgs(call, d.source)
	prefixArg, viewArg := pythonArg(pos, kw, 0, "prefix"), pythonArg(pos, kw, 1, "viewset")
	if prefixArg == nil || viewArg == nil {
		return
	}
	prefix, ok := d.stringValue(prefixArg)
	if !ok {
		return
	}
	reg := drfRegistration{router: router, prefix: prefix, line: int(call.StartPoint().Row) + 1}
	reg.handler, reg.qualifier, _ = d.name(viewArg)
	d.registrations = append(d.registrations, reg)
}

// routes builds the file's Django and DRF routes.
func (d *djangoRoutes) routes() []ExtractedRoute {
	if len(d.patterns) == 0 && len(d.registrations) == 0 {
		return nil
	}
	b := djangoBuild{d: d, routerPrefixes: make(map[string][]djangoSegment)}
	for _, v := range d.patterns {
		b.value(v, djangoRoot)
	}

	for _, reg := range d.registrations {
		if !d.routers[reg.router] {
			continue // admin.site.register(Model) and the like
		}
		prefixes := b.routerPrefixes[reg.router]
		if len(prefixes) == 0 {
			prefixes = []djangoSegment{djangoRoot} // included from another file, or not at all
		}
		base := strings.Trim(reg.prefix, "/")
		for _, p := range prefixes {
			for _, rel := range []string{base + "/", base + "/<pk>/"} {
				seg := p.join(djangoSegment{rel, store.NormalizePath(rel)})
				b.out = append(b.out, ExtractedRoute{
					Method:           "ALL",
					PathPattern:      seg.pattern,
					NormalizedPath:   seg.norm,
					Handler:          reg.handler,
					HandlerQualifier: reg.qualifier,
					Framework:        drfFramework,
					FilePath:         d.filePath,
					StartLine:        reg.line,
				})
			}
		}
	}
	return b.out
}

// djangoBuild is the state of one routes() pass.
type djangoBuild struct {
	d              *djangoRoutes
	routerPrefixes map[string][]djangoSegment // DRF router → include(router.urls) prefixes
	out            []ExtractedRoute
}

// value walks a urlpatterns value: a list, a sum of lists (and router.urls).
func (b *djangoBuild) value(n *sitter.Node, prefix djangoSegment) {
	switch n.Type() {
	case "list":
		for i := 0; i < int(n.NamedChildCount()); i++ {
			b.element(n.NamedChild(i), prefix)
		}
	case "binary_operator":
		b.value(n.ChildByFieldName("left"), prefix)
		b.value(n.ChildByFieldName("right"), prefix)
	case "parenthesized_expression":
		if n.NamedChildCount() == 1 {
			b.value(n.NamedChild(0), prefix)
		}
	case "attribute":
		b.routerURLs(n, prefix)
	}
}

// routerURLs records prefix for router.urls when router is a DRF router of
// this file.
func (b *djangoBuild) routerURLs(n *sitter.Node, prefix djangoSegment) {
	obj := n.ChildByFieldName("object")
	if obj == nil || obj.Type() != "identifier" || b.d.text(n.ChildByFieldName("attribute")) != "urls" {
		return
	}
	if r := b.d.text(obj); b.d.routers[r] {
		b.routerPrefixes[r] = append(b.routerPrefixes[r], prefix)
	}
}

// element builds the routes of one urlpatterns element.
func (b *djangoBuild) element(n *sitter.Node, prefix djangoSegment) {
	if n == nil || n.Type() != "call" {
		return
	}
	regex, ok := djangoRouteFuncs[pythonCallName(n.ChildByFieldName("function"), b.d.source)]
	if !ok {
		return
	}
	pos, kw := pythonArgs(n, b.d.source)
	routeArg := pythonArg(pos, kw, 0, "route")
	if regex {
		routeArg = pythonArg(pos, kw, 0, "regex")
		if routeArg == nil {
			routeArg = pythonArg(pos, kw, 0, "route")
		}
	}
	viewArg := pythonArg(pos, kw, 1, "view")
	if routeArg == nil || viewArg == nil {
		return
	}
	raw, ok := b.d.stringValue(routeArg)
	if !ok {
		return // dynamic pattern
	}
	seg := djangoSegment{raw, store.NormalizePath(raw)}
	if regex {
		seg.norm = djangoRegexPath(raw)
	}
	full := prefix.join(seg)

	// include() and .urls are prefixes, not routes.
	if viewArg.Type() == "attribute" && b.d.text(viewArg.ChildByFieldName("attribute")) == "urls" {
		b.routerURLs(viewArg, full)
		return
	}
	if viewArg.Type() == "call" && pythonCallName(viewArg.ChildByFieldName("function"), b.d.source) == "include" {
		ipos, ikw := pythonArgs(viewArg, b.d.source)
		inner := pythonArg(ipos, ikw, 0, "arg")
		if inner != nil && inner.Type() == "tuple" && inner.NamedChildCount() > 0 {
			inner = inner.NamedChild(0) // include((patterns, "app_name"))
		}
		if inner != nil {
			b.value(inner, full)
		}
		return
	}

	handler, qualifier, _ := b.d.name(viewArg)
	if viewArg.Type() == "call" {
		handler, qualifier = "", ""
		if fn := viewArg.ChildByFieldName("function"); fn != nil && fn.Type() == "attribute" &&
			b.d.text(fn.ChildByFieldName("attribute")) == "as_view" {
			handler, qualifier, _ = b.d.name(fn.ChildByFieldName("object"))
		}
	}
	b.out = append(b.out, ExtractedRoute{
		Method:           "ALL",
		PathPattern:      full.pattern,
		NormalizedPath:   full.norm,
		Handler:          handler,
		HandlerQualifier: qualifier,
		Framework:        djangoFramework,
		FilePath:         b.d.filePath,
		StartLine:        int(n.StartPoint().Row) + 1,
	})
}

// text returns a node's source text, "" for nil.
func (d *djangoRoutes) text(n *sitter.Node) string {
	if n == nil {
		return ""
	}
	s, _ := safeNodeContent(n, d.source)
	return s
}

// stringValue returns the value of a plain string literal.
func (d *djangoRoutes) stringValue(n *sitter.Node) (string, bool) {
	if n == nil || n.Type() != "string" {
		return "", false
	}
	return unquoteStringLiteral(d.text(n))
}

// name reads a handler reference: a name (index) or a dotted name
// (views.index: name index, qualifier views). ok is false for anything else.
func (d *djangoRoutes) name(n *sitter.Node) (name, qualifier string, ok bool) {
	if n == nil {
		return "", "", false
	}
	switch n.Type() {
	case "identifier":
		return d.text(n), "", true
	case "attribute":
		obj := n.ChildByFieldName("object")
		if q := d.text(obj); normalizeReceiver(q) == q {
			return d.text(n.ChildByFieldName("attribute")), q, true
		}
	}
	return "", "", false
}

// pythonCallName returns the called name of a call's function: path for
// path(...) and urls.path(...), "" for anything else.
func pythonCallName(fn *sitter.Node, source []byte) string {
	if fn == nil {
		return ""
	}
	if fn.Type() == "attribute" {
		fn = fn.ChildByFieldName("attribute")
	}
	if fn == nil || fn.Type() != "identifier" {
		return ""
	}
	s, _ := safeNodeContent(fn, source)
	return s
}

// pythonArgs splits a call's arguments into positional ones and keyword ones
// by name. A call with *args or **kwargs has unknown positions: none.
func pythonArgs(call *sitter.Node, source []byte) (pos []*sitter.Node, kw map[string]*sitter.Node) {
	kw = make(map[string]*sitter.Node)
	args := call.ChildByFieldName("arguments")
	if args == nil || args.Type() != "argument_list" {
		return nil, kw
	}
	for i := 0; i < int(args.NamedChildCount()); i++ {
		a := args.NamedChild(i)
		switch a.Type() {
		case "comment":
		case "keyword_argument":
			if name := a.ChildByFieldName("name"); name != nil {
				if k, ok := safeNodeContent(name, source); ok {
					kw[k] = a.ChildByFieldName("value")
				}
			}
		case "list_splat", "dictionary_splat":
			return nil, map[string]*sitter.Node{}
		default:
			pos = append(pos, a)
		}
	}
	return pos, kw
}

// pythonArg returns the argument at position i, else the keyword argument
// key, else nil.
func pythonArg(pos []*sitter.Node, kw map[string]*sitter.Node, i int, key string) *sitter.Node {
	if i < len(pos) {
		return pos[i]
	}
	return kw[key]
}

// djangoRegexPath normalises a re_path()/url() regex to a path: the ^ and $
// anchors are dropped and each named group (?P<name>...) becomes :param, or
// * when it can match "/". It returns the pattern unchanged when anything
// else is a regex construct (unnamed groups, classes, quantifiers outside a
// group, alternation), so such a route only matches exactly.
func djangoRegexPath(raw string) string {
	s := strings.TrimPrefix(raw, "^")
	if strings.HasSuffix(s, "$") && !strings.HasSuffix(s, `\$`) {
		s = s[:len(s)-1]
	}
	var out strings.Builder
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case c == '\\':
			if i+1 < len(s) && strings.IndexByte("./-_~", s[i+1]) >= 0 {
				out.WriteByte(s[i+1]) // an escaped literal: robots\.txt
				i += 2
				continue
			}
			return raw
		case strings.HasPrefix(s[i:], "(?P<"):
			end := regexGroupEnd(s, i)
			nameEnd := strings.IndexByte(s[i:], '>')
			if end < 0 || nameEnd < 0 || i+nameEnd > end || !isIdentifier(s[i+4:i+nameEnd]) {
				return raw
			}
			if regexMatchesSlash(s[i+nameEnd+1 : end]) {
				out.WriteString("*")
			} else {
				out.WriteString(":param")
			}
			i = end + 1
			continue
		case strings.IndexByte(`.^$*+?()[]{}|`, c) >= 0:
			return raw
		}
		out.WriteByte(c)
		i++
	}
	return store.NormalizePath(out.String())
}

// regexGroupEnd returns the index of the ")" closing the group that opens at
// s[start], skipping escapes and character classes; -1 if it isn't closed.
func regexGroupEnd(s string, start int) int {
	depth, inClass := 0, false
	for i := start; i < len(s); i++ {
		switch c := s[i]; {
		case c == '\\':
			i++
		case inClass:
			if c == ']' {
				inClass = false
			}
		case c == '[':
			inClass = true
		case c == '(':
			depth++
		case c == ')':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

// regexMatchesSlash reports whether a group body can match "/": a "." outside
// a character class, or a "/" anywhere. Negated classes ([^/]) don't count.
func regexMatchesSlash(body string) bool {
	inClass := false
	for i := 0; i < len(body); i++ {
		switch c := body[i]; {
		case c == '\\':
			i++
		case inClass:
			if c == ']' {
				inClass = false
			} else if c == '/' && !strings.HasPrefix(body[strings.LastIndexByte(body[:i], '[')+1:], "^") {
				return true
			}
		case c == '[':
			inClass = true
		case c == '.' || c == '/':
			return true
		}
	}
	return false
}
