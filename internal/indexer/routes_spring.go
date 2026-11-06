package indexer

import (
	"bytes"
	"context"
	"fmt"
	"strconv"
	"strings"

	sitter "github.com/smacker/go-tree-sitter"
)

// Spring route extraction (Java and Kotlin). A route is a method annotated
// with a mapping annotation (springVerbs) whose class is a controller: the
// nearest class around the method is annotated @RestController or
// @Controller. A class-level @RequestMapping gives the prefix of every route
// of the class. The handler is the annotated method itself.
//
//   - Paths come from value =, path = or the positional argument; an array
//     ({"/a", "/b"} in Java, ["/a", "/b"] or several arguments in Kotlin)
//     gives one route per path, and no path is "" (the class prefix alone,
//     or "/").
//   - @RequestMapping takes its methods from method = RequestMethod.X (or an
//     array of them); without one it is ALL.
//   - A path that is not a string literal (a constant, a concatenation, a
//     Kotlin template) can't be read: the annotation yields no route, and an
//     unreadable class prefix drops the class's routes.
//
// Nothing else is a route: a @Service, a @FeignClient interface, a class
// without annotations, a nested class that isn't a controller itself, or a
// Kotlin top-level function.

// springFramework is the framework of every Spring route.
const springFramework = "spring"

// springVerbs maps the Spring mapping annotations to their HTTP method;
// @RequestMapping ("") reads the method from its arguments.
var springVerbs = map[string]string{
	"GetMapping":     "GET",
	"PostMapping":    "POST",
	"PutMapping":     "PUT",
	"DeleteMapping":  "DELETE",
	"PatchMapping":   "PATCH",
	"RequestMapping": "",
}

// springControllers are the class annotations that make a class a controller.
var springControllers = map[string]bool{"RestController": true, "Controller": true}

// springAnnotation is one annotation: its bare name and its arguments by key.
// Positional arguments are under "value"; several of them (Kotlin varargs)
// are all kept, in order.
type springAnnotation struct {
	name string
	args map[string][]*sitter.Node
}

// springLang reads the annotation and literal shapes of one grammar.
type springLang interface {
	// annotation parses an annotation node.
	annotation(n *sitter.Node) (springAnnotation, bool)
	// stringValues reads a literal path argument: a string or an array of strings.
	stringValues(n *sitter.Node) ([]string, bool)
	// methods reads a RequestMethod argument: RequestMethod.GET, GET, or an
	// array of those.
	methods(n *sitter.Node) []string
	// class returns the class declaration around a method, or nil when the
	// nearest type around it is not a class (an interface, enum, object,
	// anonymous class) or there is none.
	class(method *sitter.Node) *sitter.Node
	// classAnnotations returns the annotations of a class declaration.
	classAnnotations(class *sitter.Node) []springAnnotation
	// className returns the name of a class declaration.
	className(class *sitter.Node) string
}

// springController is what a controller class gives its routes.
type springController struct {
	name     string
	prefixes []string
	ok       bool // a controller with readable prefixes
}

// springRoutes extracts the Spring routes of one Java or Kotlin file.
func springRoutes(
	ctx context.Context,
	tree *sitter.Tree,
	source []byte,
	adapter LanguageAdapter,
	queryBytes []byte,
	filePath string,
) ([]ExtractedRoute, error) {
	// Every route needs a mapping annotation in a controller.
	if !bytes.Contains(source, []byte("Mapping")) || !bytes.Contains(source, []byte("Controller")) {
		return nil, nil
	}
	var lang springLang
	switch adapter.Name() {
	case "java":
		lang = springJava{source: source}
	case "kotlin":
		lang = springKotlin{source: source}
	default:
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

	controllers := make(map[uint32]springController) // class start byte → controller
	var routes []ExtractedRoute
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
		method, annNode, nameNode := caps["route.call"], caps["route.annotation"], caps["route.handler"]
		if method == nil || annNode == nil || nameNode == nil {
			continue
		}
		ann, ok := lang.annotation(annNode)
		if !ok {
			continue
		}
		verb, ok := springVerbs[ann.name]
		if !ok {
			continue
		}

		class := lang.class(method)
		if class == nil {
			continue
		}
		ctrl, seen := controllers[class.StartByte()]
		if !seen {
			ctrl = springControllerOf(lang, class)
			controllers[class.StartByte()] = ctrl
		}
		if !ctrl.ok {
			continue
		}

		paths, ok := springPaths(lang, ann)
		if !ok {
			continue // a path that isn't a literal: the route would be wrong
		}
		methods := []string{verb}
		if verb == "" {
			methods = []string{"ALL"}
			if vals := ann.args["method"]; len(vals) > 0 {
				var ms []string
				for _, v := range vals {
					ms = append(ms, lang.methods(v)...)
				}
				if len(ms) == 0 {
					continue // method = someConstant: unknown
				}
				methods = ms
			}
		}

		handler, _ := safeNodeContent(nameNode, source)
		if handler == "" {
			continue
		}
		line := int(annNode.StartPoint().Row) + 1
		handlerLine := int(method.StartPoint().Row) + 1
		for _, prefix := range ctrl.prefixes {
			for _, p := range paths {
				full := springJoin(prefix, p)
				for _, m := range methods {
					routes = append(routes, ExtractedRoute{
						Method:      m,
						PathPattern: full,
						Handler:     ctrl.name + "." + handler,
						HandlerLine: handlerLine,
						Framework:   springFramework,
						FilePath:    filePath,
						StartLine:   line,
					})
				}
			}
		}
	}
	return routes, nil
}

// springControllerOf reads a class's controller annotation and its
// class-level @RequestMapping prefixes.
func springControllerOf(lang springLang, class *sitter.Node) springController {
	anns := lang.classAnnotations(class)
	isController := false
	for _, a := range anns {
		if springControllers[a.name] {
			isController = true
		}
	}
	if !isController {
		return springController{}
	}
	ctrl := springController{name: lang.className(class), prefixes: []string{""}, ok: true}
	if ctrl.name == "" {
		return springController{}
	}
	for _, a := range anns {
		if a.name != "RequestMapping" {
			continue
		}
		prefixes, ok := springPaths(lang, a)
		if !ok {
			return springController{} // dynamic prefix: every path would be wrong
		}
		ctrl.prefixes = prefixes
	}
	return ctrl
}

// springPaths returns the paths of a mapping annotation: its value or path
// argument (aliases), [""] when it has neither. ok is false when the paths
// can't be read.
func springPaths(lang springLang, a springAnnotation) ([]string, bool) {
	vals := a.args["path"]
	if len(vals) == 0 {
		vals = a.args["value"]
	}
	if len(vals) == 0 {
		return []string{""}, true
	}
	var out []string
	for _, v := range vals {
		s, ok := lang.stringValues(v)
		if !ok {
			return nil, false
		}
		out = append(out, s...)
	}
	if len(out) == 0 {
		return []string{""}, true // path = {}: no path
	}
	return out, true
}

// springJoin joins a class prefix and a method path. Spring adds the leading
// slash a path may lack, and a controller with neither maps "/".
func springJoin(prefix, path string) string {
	full := joinRoutePath(prefix, path)
	if !strings.HasPrefix(full, "/") {
		full = "/" + full
	}
	return full
}

// unescapeJVMString resolves the backslash escapes of a Java or Kotlin string
// literal's content (\\, \", \n, \uXXXX, Kotlin \$, ...). Unknown escapes
// keep the escaped character.
func unescapeJVMString(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c != '\\' || i+1 == len(s) {
			b.WriteByte(c)
			continue
		}
		i++
		switch s[i] {
		case 'n':
			b.WriteByte('\n')
		case 't':
			b.WriteByte('\t')
		case 'r':
			b.WriteByte('\r')
		case 'b':
			b.WriteByte('\b')
		case 'f':
			b.WriteByte('\f')
		case 's':
			b.WriteByte(' ')
		case 'u':
			j := i + 1
			for j < len(s) && s[j] == 'u' { // Java allows \uuuu0041
				j++
			}
			if j+4 <= len(s) {
				if r, err := strconv.ParseUint(s[j:j+4], 16, 16); err == nil {
					b.WriteRune(rune(r))
					i = j + 3
					continue
				}
			}
			b.WriteByte('u')
		default:
			b.WriteByte(s[i])
		}
	}
	return b.String()
}

// springJava reads Java annotations.
type springJava struct{ source []byte }

func (j springJava) text(n *sitter.Node) string {
	if n == nil {
		return ""
	}
	s, _ := safeNodeContent(n, j.source)
	return s
}

// lastName returns the last identifier of a (scoped) name:
// org.springframework...GetMapping gives "GetMapping".
func (j springJava) lastName(n *sitter.Node) string {
	for n != nil && n.Type() == "scoped_identifier" {
		n = n.ChildByFieldName("name")
	}
	return j.text(n)
}

func (j springJava) annotation(n *sitter.Node) (springAnnotation, bool) {
	switch n.Type() {
	case "marker_annotation", "annotation":
	default:
		return springAnnotation{}, false
	}
	a := springAnnotation{name: j.lastName(n.ChildByFieldName("name")), args: map[string][]*sitter.Node{}}
	if a.name == "" {
		return springAnnotation{}, false
	}
	list := n.ChildByFieldName("arguments")
	if list == nil {
		return a, true
	}
	for i := 0; i < int(list.NamedChildCount()); i++ {
		arg := list.NamedChild(i)
		switch arg.Type() {
		case "line_comment", "block_comment":
		case "element_value_pair":
			if key, value := arg.ChildByFieldName("key"), arg.ChildByFieldName("value"); key != nil && value != nil {
				a.args[j.text(key)] = append(a.args[j.text(key)], value)
			}
		default:
			a.args["value"] = append(a.args["value"], arg)
		}
	}
	return a, true
}

func (j springJava) stringValues(n *sitter.Node) ([]string, bool) {
	switch n.Type() {
	case "string_literal":
		lit := j.text(n)
		if strings.HasPrefix(lit, `"""`) || len(lit) < 2 {
			return nil, false // a text block is never a path
		}
		return []string{unescapeJVMString(lit[1 : len(lit)-1])}, true
	case "element_value_array_initializer":
		out := []string{}
		for i := 0; i < int(n.NamedChildCount()); i++ {
			el := n.NamedChild(i)
			if el.Type() == "line_comment" || el.Type() == "block_comment" {
				continue
			}
			s, ok := j.stringValues(el)
			if !ok {
				return nil, false
			}
			out = append(out, s...)
		}
		return out, true
	case "parenthesized_expression":
		if n.NamedChildCount() == 1 {
			return j.stringValues(n.NamedChild(0))
		}
	}
	return nil, false
}

func (j springJava) methods(n *sitter.Node) []string {
	switch n.Type() {
	case "field_access":
		if j.text(n.ChildByFieldName("object")) == "RequestMethod" ||
			strings.HasSuffix(j.text(n.ChildByFieldName("object")), ".RequestMethod") {
			return springMethod(j.text(n.ChildByFieldName("field")))
		}
	case "identifier": // import static ...RequestMethod.GET
		return springMethod(j.text(n))
	case "element_value_array_initializer":
		var out []string
		for i := 0; i < int(n.NamedChildCount()); i++ {
			out = append(out, j.methods(n.NamedChild(i))...)
		}
		return out
	}
	return nil
}

func (j springJava) class(method *sitter.Node) *sitter.Node {
	for p := method.Parent(); p != nil; p = p.Parent() {
		switch p.Type() {
		case "class_declaration":
			return p
		case "interface_declaration", "enum_declaration", "record_declaration",
			"annotation_type_declaration", "object_creation_expression":
			return nil
		}
	}
	return nil
}

func (j springJava) classAnnotations(class *sitter.Node) []springAnnotation {
	var out []springAnnotation
	for i := 0; i < int(class.NamedChildCount()); i++ {
		mods := class.NamedChild(i)
		if mods.Type() != "modifiers" {
			continue
		}
		for k := 0; k < int(mods.NamedChildCount()); k++ {
			if a, ok := j.annotation(mods.NamedChild(k)); ok {
				out = append(out, a)
			}
		}
	}
	return out
}

func (j springJava) className(class *sitter.Node) string {
	return j.text(class.ChildByFieldName("name"))
}

// springMethod returns the HTTP method of a RequestMethod constant name.
func springMethod(name string) []string {
	switch name {
	case "GET", "HEAD", "POST", "PUT", "PATCH", "DELETE", "OPTIONS", "TRACE":
		return []string{name}
	}
	return nil
}

// springKotlin reads Kotlin annotations. The Kotlin grammar has no fields.
type springKotlin struct{ source []byte }

func (k springKotlin) text(n *sitter.Node) string {
	if n == nil {
		return ""
	}
	s, _ := safeNodeContent(n, k.source)
	return s
}

// child returns n's first named child of type typ.
func (k springKotlin) child(n *sitter.Node, typ string) *sitter.Node {
	for i := 0; i < int(n.NamedChildCount()); i++ {
		if c := n.NamedChild(i); c.Type() == typ {
			return c
		}
	}
	return nil
}

// typeName returns the last type_identifier of a user_type:
// org.springframework...GetMapping gives "GetMapping".
func (k springKotlin) typeName(userType *sitter.Node) string {
	name := ""
	for i := 0; userType != nil && i < int(userType.NamedChildCount()); i++ {
		if c := userType.NamedChild(i); c.Type() == "type_identifier" {
			name = k.text(c)
		}
	}
	return name
}

func (k springKotlin) annotation(n *sitter.Node) (springAnnotation, bool) {
	if n.Type() != "annotation" || k.child(n, "use_site_target") != nil {
		return springAnnotation{}, false // @get:GetMapping targets a getter
	}
	a := springAnnotation{args: map[string][]*sitter.Node{}}
	if ut := k.child(n, "user_type"); ut != nil {
		a.name = k.typeName(ut)
	} else if ci := k.child(n, "constructor_invocation"); ci != nil {
		a.name = k.typeName(k.child(ci, "user_type"))
		if args := k.child(ci, "value_arguments"); args != nil {
			k.addArgs(a.args, args)
		}
	}
	return a, a.name != ""
}

// addArgs adds the value_argument children of list to args: name = value
// under its name, a positional one under "value".
func (k springKotlin) addArgs(args map[string][]*sitter.Node, list *sitter.Node) {
	for i := 0; i < int(list.NamedChildCount()); i++ {
		arg := list.NamedChild(i)
		if arg.Type() != "value_argument" || arg.NamedChildCount() == 0 {
			continue
		}
		value := arg.NamedChild(int(arg.NamedChildCount()) - 1)
		key := "value"
		for c := 0; c < int(arg.ChildCount()); c++ {
			if arg.Child(c).Type() == "=" && arg.NamedChildCount() >= 2 {
				key = k.text(arg.NamedChild(0))
				break
			}
		}
		args[key] = append(args[key], value)
	}
}

func (k springKotlin) stringValues(n *sitter.Node) ([]string, bool) {
	switch n.Type() {
	case "string_literal":
		for i := 0; i < int(n.NamedChildCount()); i++ {
			switch n.NamedChild(i).Type() {
			case "interpolated_identifier", "interpolated_expression":
				return nil, false // "/x/$id": not a constant
			}
		}
		lit := k.text(n)
		if strings.HasPrefix(lit, `"""`) && strings.HasSuffix(lit, `"""`) && len(lit) >= 6 {
			return []string{lit[3 : len(lit)-3]}, true // raw string: no escapes
		}
		if len(lit) < 2 {
			return nil, false
		}
		return []string{unescapeJVMString(lit[1 : len(lit)-1])}, true
	case "collection_literal":
		out := []string{}
		for i := 0; i < int(n.NamedChildCount()); i++ {
			el := n.NamedChild(i)
			if el.Type() == "line_comment" || el.Type() == "multiline_comment" {
				continue
			}
			s, ok := k.stringValues(el)
			if !ok {
				return nil, false
			}
			out = append(out, s...)
		}
		return out, true
	case "call_expression": // arrayOf("/a", "/b")
		vals, ok := k.arrayOf(n)
		if !ok {
			return nil, false
		}
		out := []string{}
		for _, v := range vals {
			s, ok := k.stringValues(v)
			if !ok {
				return nil, false
			}
			out = append(out, s...)
		}
		return out, true
	case "parenthesized_expression":
		if n.NamedChildCount() == 1 {
			return k.stringValues(n.NamedChild(0))
		}
	}
	return nil, false
}

// arrayOf returns the arguments of an arrayOf(...) call.
func (k springKotlin) arrayOf(n *sitter.Node) ([]*sitter.Node, bool) {
	if n.NamedChildCount() != 2 || k.text(n.NamedChild(0)) != "arrayOf" {
		return nil, false
	}
	suffix := n.NamedChild(1)
	if suffix.Type() != "call_suffix" {
		return nil, false
	}
	list := k.child(suffix, "value_arguments")
	if list == nil {
		return nil, false
	}
	args := map[string][]*sitter.Node{}
	k.addArgs(args, list)
	return args["value"], true
}

func (k springKotlin) methods(n *sitter.Node) []string {
	switch n.Type() {
	case "navigation_expression": // RequestMethod.GET
		if n.NamedChildCount() != 2 {
			return nil
		}
		recv, suffix := k.text(n.NamedChild(0)), n.NamedChild(1)
		if recv != "RequestMethod" && !strings.HasSuffix(recv, ".RequestMethod") {
			return nil
		}
		return springMethod(k.text(k.child(suffix, "simple_identifier")))
	case "simple_identifier": // import ...RequestMethod.GET
		return springMethod(k.text(n))
	case "collection_literal":
		var out []string
		for i := 0; i < int(n.NamedChildCount()); i++ {
			out = append(out, k.methods(n.NamedChild(i))...)
		}
		return out
	case "call_expression":
		vals, _ := k.arrayOf(n)
		var out []string
		for _, v := range vals {
			out = append(out, k.methods(v)...)
		}
		return out
	}
	return nil
}

func (k springKotlin) class(method *sitter.Node) *sitter.Node {
	for p := method.Parent(); p != nil; p = p.Parent() {
		switch p.Type() {
		case "class_declaration":
			for i := 0; i < int(p.ChildCount()); i++ {
				if p.Child(i).Type() == "interface" {
					return nil
				}
			}
			return p
		case "object_declaration", "companion_object", "object_literal",
			"lambda_literal", "anonymous_function", "function_declaration":
			return nil
		}
	}
	return nil
}

func (k springKotlin) classAnnotations(class *sitter.Node) []springAnnotation {
	var out []springAnnotation
	if mods := k.child(class, "modifiers"); mods != nil {
		for i := 0; i < int(mods.NamedChildCount()); i++ {
			if a, ok := k.annotation(mods.NamedChild(i)); ok {
				out = append(out, a)
			}
		}
		return out
	}
	return k.detachedAnnotations(class)
}

// detachedAnnotations works around a tree-sitter-kotlin ambiguity: the
// annotations of a top-level class followed by other declarations can parse
// as a separate statement before it, a chain of prefix_expression nodes
// holding the annotations, with an annotation's single argument as the
// parenthesized_expression after it:
//
//	(prefix_expression (annotation @RestController)
//	  (prefix_expression (annotation @RequestMapping) (parenthesized_expression "/api")))
//	(class_declaration ...)
//
// They are the class's annotations when that chain is the class's previous
// sibling (comments aside) and holds nothing else.
func (k springKotlin) detachedAnnotations(class *sitter.Node) []springAnnotation {
	prev := class.PrevNamedSibling()
	for prev != nil && (prev.Type() == "line_comment" || prev.Type() == "multiline_comment") {
		prev = prev.PrevNamedSibling()
	}
	if prev == nil || prev.Type() != "prefix_expression" {
		return nil
	}
	var flat []*sitter.Node
	for n := prev; n != nil; {
		if n.Type() != "prefix_expression" {
			flat = append(flat, n)
			break
		}
		var next *sitter.Node
		for i := 0; i < int(n.NamedChildCount()); i++ {
			c := n.NamedChild(i)
			if i == int(n.NamedChildCount())-1 && c.Type() == "prefix_expression" {
				next = c
				break
			}
			flat = append(flat, c)
		}
		n = next
	}
	var out []springAnnotation
	for i := 0; i < len(flat); i++ {
		n := flat[i]
		if n.Type() != "annotation" {
			return nil // an expression statement, not detached annotations
		}
		a, ok := k.annotation(n)
		if !ok {
			return nil
		}
		if i+1 < len(flat) && flat[i+1].Type() == "parenthesized_expression" && k.child(n, "user_type") != nil {
			if p := flat[i+1]; p.NamedChildCount() == 1 {
				a.args["value"] = append(a.args["value"], p.NamedChild(0))
			}
			i++
		}
		out = append(out, a)
	}
	return out
}

func (k springKotlin) className(class *sitter.Node) string {
	return k.text(k.child(class, "type_identifier"))
}
