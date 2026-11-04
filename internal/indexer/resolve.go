package indexer

import (
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/context-link-mcp/context-link/internal/store"
	"github.com/context-link-mcp/context-link/pkg/models"
)

// Dependency resolution maps each call site (a callee name plus, for method
// and selector calls, the receiver) to at most one symbol of the same
// language family. Candidates are searched in widening scopes and the first
// scope that has any candidate decides:
//
//  1. the caller's file,
//  2. the caller's directory (package),
//  3. the files the caller imports,
//  4. the rest of the repo, only if exactly one symbol has the name (not for
//     Go, where a plain call never leaves its package).
//
// Plain calls skip methods in languages where a method needs a receiver (Go,
// Python, JS/TS); in Java, C#, Kotlin, C++ and Swift foo() may be this.foo().
// Within a scope a single candidate wins; with several, the one in the
// caller's own class/type wins if it is the only one, otherwise the call is
// ambiguous and gets no edge. Method and selector calls are stricter:
//
//   - a receiver bound by an import (Go package, Python "import a.b" or a
//     name from "from m import x", JS/TS default or namespace import; the
//     longest bound dotted prefix counts) resolves only inside the imported
//     repo files (a Python package covers its modules, which __init__.py
//     often re-exports), and not at all when the import is external (os.Open,
//     sql.Open, os.path.join);
//   - self/this/cls resolve to methods of the caller's file, then of the
//     imported files;
//   - any other receiver resolves in the caller's file only, plus for Go the
//     caller's package and the methods of imported repo packages. Never
//     repo-wide, since the receiver's type is unknown (d.get() on a dict).
//
// The result depends only on the symbols and call sites, never on map order.

// symbolCandidate is a symbol a call may resolve to.
type symbolCandidate struct {
	id        int64
	file      string
	dir       string
	family    string
	isMethod  bool
	container string
	qualified string // qualified name; overloads in one file share it
}

// resolver resolves call sites against every symbol of a repo.
type resolver struct {
	byName  map[string][]symbolCandidate // name and qualified name → candidates, ordered by file, line, ID
	files   map[string]string            // file path → language family, for files with symbols
	dirs    map[string][]string          // directory → files with symbols, sorted
	ordered []string                     // every file with symbols, sorted

	// goModule is the module path in the repo root's go.mod, or "" when there
	// is none. Imports under it map to repo directories exactly.
	goModule string

	importCache map[string][]string // see importedFiles
}

// languageFamily groups languages whose files can call each other directly.
func languageFamily(lang string) string {
	switch lang {
	case "typescript", "tsx", "javascript":
		return "ecmascript"
	case "c", "cpp":
		return "c"
	default:
		return lang
	}
}

// plainCallsSkipMethods reports whether a plain call (no receiver) can never
// target a method in the language family: methods there need a receiver.
func plainCallsSkipMethods(family string) bool {
	switch family {
	case "go", "python", "ecmascript":
		return true
	}
	return false
}

// symbolContainer returns the class/type part of a symbol's qualified name.
func symbolContainer(s models.Symbol) string {
	if s.QualifiedName == s.Name {
		return ""
	}
	return strings.TrimSuffix(s.QualifiedName, "."+s.Name)
}

// newResolver indexes every symbol of a repo by name and qualified name.
func newResolver(symbolsByFile map[string][]models.Symbol) *resolver {
	r := &resolver{
		byName: make(map[string][]symbolCandidate),
		files:  make(map[string]string, len(symbolsByFile)),
		dirs:   make(map[string][]string),

		importCache: make(map[string][]string),
	}
	for file := range symbolsByFile {
		r.ordered = append(r.ordered, file)
	}
	sort.Strings(r.ordered)

	for _, file := range r.ordered {
		syms := append([]models.Symbol(nil), symbolsByFile[file]...)
		if len(syms) == 0 {
			continue
		}
		sort.SliceStable(syms, func(i, j int) bool {
			if syms[i].StartLine != syms[j].StartLine {
				return syms[i].StartLine < syms[j].StartLine
			}
			return syms[i].ID < syms[j].ID
		})
		dir := path.Dir(file)
		r.files[file] = languageFamily(syms[0].Language)
		r.dirs[dir] = append(r.dirs[dir], file)
		for _, s := range syms {
			c := symbolCandidate{
				id:        s.ID,
				file:      file,
				dir:       dir,
				family:    languageFamily(s.Language),
				isMethod:  s.Kind == "method",
				container: symbolContainer(s),
				qualified: s.QualifiedName,
			}
			r.byName[s.Name] = append(r.byName[s.Name], c)
			if s.QualifiedName != s.Name {
				r.byName[s.QualifiedName] = append(r.byName[s.QualifiedName], c)
			}
		}
	}
	return r
}

// importScope is what a file's imports bring into scope.
type importScope struct {
	files    map[string]bool         // repo files imported
	bindings map[string]importTarget // bound (dotted) name → what it refers to
}

// importTarget is what a name bound by an import refers to.
type importTarget struct {
	files  map[string]bool // repo files; empty for an external module
	module bool            // a module or package, not a class or other name in one
}

// lookup returns the longest dotted prefix of a receiver that an import
// binds (os.path → "os.path" or "os"), and its target.
func (s importScope) lookup(receiver string) (string, importTarget, bool) {
	for p := receiver; ; {
		if b, ok := s.bindings[p]; ok {
			return p, b, true
		}
		i := strings.LastIndexByte(p, '.')
		if i < 0 {
			return "", importTarget{}, false
		}
		p = p[:i]
	}
}

// bind records that an import binds name to files.
func (s importScope) bind(name string, files []string, module bool) {
	b, ok := s.bindings[name]
	if !ok {
		b = importTarget{files: map[string]bool{}, module: module}
	}
	b.module = b.module && module
	for _, f := range files {
		b.files[f] = true
	}
	s.bindings[name] = b
}

// resolveFile resolves the call sites of one file whose symbols are callers.
func (r *resolver) resolveFile(file string, callers []models.Symbol, sites []store.CallSite) []models.Dependency {
	if len(callers) == 0 || len(sites) == 0 {
		return nil
	}
	family := languageFamily(callers[0].Language)
	scope := r.imports(family, file, sites)

	var deps []models.Dependency
	for _, site := range sites {
		if site.Kind == "import" {
			continue // imports don't map to symbol-to-symbol edges
		}
		caller, ok := enclosingSymbol(callers, site.Line)
		if !ok {
			continue // module-level statement: no calling symbol
		}
		calleeID, ok := r.resolve(family, file, symbolContainer(caller), site, scope)
		if !ok {
			continue
		}
		deps = append(deps, models.Dependency{CallerID: caller.ID, CalleeID: calleeID, Kind: site.Kind})
	}
	return deps
}

// resolve returns the symbol a call site refers to, if it is unambiguous.
func (r *resolver) resolve(family, file, container string, site store.CallSite, scope importScope) (int64, bool) {
	var cands []symbolCandidate
	for _, c := range r.byName[site.Name] {
		if c.family == family {
			cands = append(cands, c)
		}
	}
	if len(cands) == 0 {
		return 0, false
	}
	dir := path.Dir(file)
	inFile := func(c symbolCandidate) bool { return c.file == file }
	inDir := func(c symbolCandidate) bool { return c.dir == dir }
	imported := func(c symbolCandidate) bool { return scope.files[c.file] }

	q := site.Qualifier
	if q == "" {
		if site.Kind == "call" && plainCallsSkipMethods(family) {
			cands = filterCandidates(cands, func(c symbolCandidate) bool { return !c.isMethod })
		}
		if id, ok, decided := pickScoped(cands, container, inFile, inDir, imported); decided {
			return id, ok
		}
		// The rest of the repo, only for a unique name. Not for Go: a plain
		// call there only reaches its own package (or a dot import, already
		// in imported), and repo-unique names would catch builtins (min, max).
		if cands = collapseOverloads(dedupeCandidates(cands)); len(cands) == 1 && family != "go" {
			return cands[0].id, true
		}
		return 0, false
	}

	switch q {
	case "self", "this", "cls":
		methods := filterCandidates(cands, func(c symbolCandidate) bool { return c.isMethod })
		id, ok, _ := pickScoped(methods, container, inFile, imported)
		return id, ok
	}

	if prefix, b, bound := scope.lookup(q); bound {
		if len(b.files) == 0 {
			return 0, false // external package or module: os.Open, fmt.Println
		}
		in := filterCandidates(cands, func(c symbolCandidate) bool { return b.files[c.file] })
		if prefix == q && b.module && (family == "go" || family == "python") {
			// pkg.Func() or module.func(): never a method.
			in = filterCandidates(in, func(c symbolCandidate) bool { return !c.isMethod })
		}
		return pickOne(in, container)
	}

	// Unknown receiver: its type can't be told, so stay local.
	if family == "go" {
		importedMethod := func(c symbolCandidate) bool { return c.isMethod && scope.files[c.file] }
		id, ok, _ := pickScoped(cands, container, inFile, inDir, importedMethod)
		return id, ok
	}
	id, ok, _ := pickScoped(cands, container, inFile)
	return id, ok
}

// filterCandidates returns the candidates keep accepts, in order.
func filterCandidates(cands []symbolCandidate, keep func(symbolCandidate) bool) []symbolCandidate {
	var out []symbolCandidate
	for _, c := range cands {
		if keep(c) {
			out = append(out, c)
		}
	}
	return out
}

// pickScoped tries each scope in order; the first scope with candidates
// decides (see pickOne), and decided reports whether any scope had one.
func pickScoped(cands []symbolCandidate, container string, scopes ...func(symbolCandidate) bool) (id int64, ok, decided bool) {
	for _, in := range scopes {
		matched := filterCandidates(cands, in)
		if len(matched) > 0 {
			id, ok = pickOne(matched, container)
			return id, ok, true
		}
	}
	return 0, false, false
}

// pickOne returns the only distinct candidate, or the only one in the
// caller's container; anything else is ambiguous. Overloads (several symbols
// with one qualified name in one file) count as one candidate: the first by
// line, so the edge is deterministic.
func pickOne(cands []symbolCandidate, container string) (int64, bool) {
	cands = collapseOverloads(dedupeCandidates(cands))
	if len(cands) == 1 {
		return cands[0].id, true
	}
	if container == "" {
		return 0, false
	}
	same := filterCandidates(cands, func(c symbolCandidate) bool { return c.container == container })
	if len(same) == 1 {
		return same[0].id, true
	}
	return 0, false
}

// dedupeCandidates drops repeated symbols (a name equal to another symbol's
// qualified name lists both), keeping order.
func dedupeCandidates(cands []symbolCandidate) []symbolCandidate {
	seen := make(map[int64]bool, len(cands))
	out := cands[:0:0]
	for _, c := range cands {
		if !seen[c.id] {
			seen[c.id] = true
			out = append(out, c)
		}
	}
	return out
}

// collapseOverloads keeps only the first of the candidates that share a file
// and a qualified name (overloads, several Go init()), keeping order. The
// candidates are ordered by file and line, so the first is the lowest line.
func collapseOverloads(cands []symbolCandidate) []symbolCandidate {
	type key struct{ file, qualified string }
	seen := make(map[key]bool, len(cands))
	out := cands[:0:0]
	for _, c := range cands {
		k := key{c.file, c.qualified}
		if !seen[k] {
			seen[k] = true
			out = append(out, c)
		}
	}
	return out
}

// imports maps a file's import call sites to repo files and bound names.
func (r *resolver) imports(family, file string, sites []store.CallSite) importScope {
	scope := importScope{files: map[string]bool{}, bindings: map[string]importTarget{}}
	for _, site := range sites {
		if site.Kind != "import" {
			continue
		}
		files := r.importedFiles(family, file, site.Name)
		binding := site.Qualifier
		switch {
		case binding == "":
		case family != "python":
			scope.bind(binding, files, true)
		case binding == site.Name:
			// import a.b.c: a, a.b and a.b.c are all usable as receivers.
			for p := site.Name; p != ""; {
				scope.bind(p, r.importedFiles(family, file, p), true)
				i := strings.LastIndexByte(p, '.')
				if i < 0 {
					break
				}
				p = p[:i]
			}
		case len(files) > 0:
			scope.bind(binding, files, true) // from pkg import module
		default:
			// from module import Name: Name is defined in module (or is external).
			if i := strings.LastIndexByte(site.Name, '.'); i >= 0 {
				parent := site.Name[:i]
				if strings.Trim(parent, ".") == "" {
					parent = site.Name[:i+1] // from . import Name: the package itself
				}
				files = r.importedFiles(family, file, parent)
			}
			scope.bind(binding, files, false)
		}
		for _, f := range files {
			scope.files[f] = true
		}
	}
	return scope
}

// pythonModuleTarget returns the path (without extension) a Python module
// name refers to and whether it is relative (exact) or absolute (matched as a
// path suffix, so src/ layouts work). "..pkg.mod" in a/b/c.py is a/pkg/mod.
func pythonModuleTarget(file, module string) (string, bool) {
	rest := strings.TrimLeft(module, ".")
	dots := len(module) - len(rest)
	modPath := strings.ReplaceAll(rest, ".", "/")
	if dots == 0 {
		return modPath, false
	}
	base := path.Dir(file)
	for i := 1; i < dots; i++ {
		base = path.Dir(base)
	}
	return path.Join(base, modPath), true
}

// importedFiles returns the repo files (with symbols, same family) an import
// source refers to, or nil for an external or unmappable import. Results are
// memoized: relative imports by importing directory, others by source.
func (r *resolver) importedFiles(family, file, source string) []string {
	key := family + "\x00" + source
	if strings.HasPrefix(source, ".") {
		key += "\x00" + path.Dir(file)
	}
	if out, ok := r.importCache[key]; ok {
		return out
	}
	out := r.lookupImport(family, file, source)
	r.importCache[key] = out
	return out
}

// lookupImport does the work of importedFiles.
func (r *resolver) lookupImport(family, file, source string) []string {
	var match func(f string) bool
	switch family {
	case "go":
		dir := r.goPackageDir(source)
		if dir == "" {
			return nil
		}
		return r.familyFiles(family, r.dirs[dir])
	case "python":
		// A module is its .py file; a package is its directory's modules,
		// since __init__.py often only re-exports them.
		mod, relative := pythonModuleTarget(file, source)
		match = func(f string) bool {
			p := strings.TrimSuffix(f, path.Ext(f))
			if relative {
				return p == mod || path.Dir(f) == mod
			}
			return pathHasSuffix(p, mod) || pathHasSuffix(path.Dir(f), mod)
		}
	case "ecmascript":
		if !strings.HasPrefix(source, ".") {
			return nil // a package, or a path alias we can't map
		}
		base := path.Join(path.Dir(file), source)
		baseNoExt := strings.TrimSuffix(base, path.Ext(base))
		match = func(f string) bool {
			p := strings.TrimSuffix(f, path.Ext(f))
			return f == base || p == base || p == baseNoExt || p == base+"/index"
		}
	case "java", "kotlin":
		pkg := strings.ReplaceAll(strings.TrimSuffix(source, ".*"), ".", "/")
		match = func(f string) bool {
			return pathHasSuffix(strings.TrimSuffix(f, path.Ext(f)), pkg) || pathHasSuffix(path.Dir(f), pkg)
		}
	case "c":
		inc := strings.Trim(source, "<>\"")
		match = func(f string) bool { return pathHasSuffix(f, inc) }
	default:
		return nil
	}
	var out []string
	for _, f := range r.ordered {
		if r.files[f] == family && match(f) {
			out = append(out, f)
		}
	}
	return out
}

// familyFiles keeps the files of one language family.
func (r *resolver) familyFiles(family string, files []string) []string {
	var out []string
	for _, f := range files {
		if r.files[f] == family {
			out = append(out, f)
		}
	}
	return out
}

// goPackageDir returns the repo directory a Go import path refers to, or ""
// for an external package:
//
//   - under the root go.mod's module path, the directory it names;
//   - a path whose first element has no dot is the standard library (Go
//     reserves those), even if a repo directory has the same name (log, http);
//   - otherwise (no root go.mod, or another module in the repo), the longest
//     directory with Go files that the import path ends with.
func (r *resolver) goPackageDir(importPath string) string {
	if m := r.goModule; m != "" {
		if importPath == m || strings.HasPrefix(importPath, m+"/") {
			dir := strings.TrimPrefix(strings.TrimPrefix(importPath, m), "/")
			if dir == "" {
				dir = "."
			}
			if len(r.familyFiles("go", r.dirs[dir])) == 0 {
				return ""
			}
			return dir
		}
	}
	if first, _, _ := strings.Cut(importPath, "/"); !strings.Contains(first, ".") {
		return ""
	}
	best := ""
	for dir, files := range r.dirs {
		if dir == "." || len(dir) <= len(best) || len(r.familyFiles("go", files)) == 0 {
			continue
		}
		if pathHasSuffix(importPath, dir) {
			best = dir
		}
	}
	return best
}

// goModulePath returns the module path declared in repoRoot/go.mod, or "" if
// there is no readable go.mod or it declares none.
func goModulePath(repoRoot string) string {
	data, err := os.ReadFile(filepath.Join(repoRoot, "go.mod"))
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		line, _, _ = strings.Cut(line, "//")
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[0] == "module" {
			return strings.Trim(fields[1], "\"`")
		}
	}
	return ""
}

// pathHasSuffix reports whether p equals suffix or ends with "/"+suffix.
func pathHasSuffix(p, suffix string) bool {
	return suffix != "" && (p == suffix || strings.HasSuffix(p, "/"+suffix))
}
