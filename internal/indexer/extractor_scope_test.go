package indexer

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/context-link-mcp/context-link/internal/indexer/adapters"
)

// extractSymbolKeys parses code with adapter and returns each symbol as
// "kind qualified_name@start_line".
func extractSymbolKeys(t *testing.T, adapter LanguageAdapter, code string) []string {
	t.Helper()
	tree, err := NewParserPoolManager().GetPool(adapter).Parse(context.Background(), []byte(code))
	require.NoError(t, err)
	defer tree.Close()
	syms, err := NewExtractor().ExtractSymbols(context.Background(), tree, []byte(code), adapter, "repo", "f")
	require.NoError(t, err)
	keys := make([]string, 0, len(syms))
	for _, s := range syms {
		keys = append(keys, fmt.Sprintf("%s %s@%d", s.Kind, s.QualifiedName, s.StartLine))
	}
	return keys
}

// TestExtractSymbols_LocalsAreNotSymbols checks that function-local variables
// are not extracted (M4): being the smallest span around the calls next to
// them, they would take those calls' edges from the enclosing function.
func TestExtractSymbols_LocalsAreNotSymbols(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		adapter LanguageAdapter
		code    string
		want    []string // symbols that must be extracted
		notWant []string // locals that must not be
	}{
		{
			name:    "go var and const in a function",
			adapter: adapters.NewGoAdapter(),
			code: "package p\n" +
				"var Global = 1\n" +
				"const Limit = 2\n" +
				"func Run() {\n" +
				"\tvar cfg = Load()\n" +
				"\tconst k = 3\n" +
				"\t_ = cfg\n" +
				"}\n",
			want:    []string{"variable Global@2", "variable Limit@3", "function Run@4"},
			notWant: []string{"variable cfg@5", "variable k@6"},
		},
		{
			name:    "kotlin val inside a function",
			adapter: adapters.NewKotlinAdapter(),
			code: "val top = 1\n" +
				"class A {\n" +
				"    val member = 2\n" +
				"    fun f() {\n" +
				"        val local = load()\n" +
				"    }\n" +
				"}\n" +
				"fun g() {\n" +
				"    var counter = 0\n" +
				"}\n",
			want:    []string{"variable top@1", "variable member@3", "method A.f@4", "function g@8"},
			notWant: []string{"variable local@5", "variable counter@9"},
		},
		{
			name:    "swift let inside a function",
			adapter: adapters.NewSwiftAdapter(),
			code: "let top = 1\n" +
				"class C {\n" +
				"    var member = 2\n" +
				"    func f() {\n" +
				"        let local = load()\n" +
				"    }\n" +
				"}\n",
			want:    []string{"variable top@1", "variable member@3", "method C.f@4"},
			notWant: []string{"variable local@5"},
		},
		{
			name:    "typescript arrow const inside a function",
			adapter: adapters.NewTypeScriptAdapter(),
			code: "const top = () => 1;\n" +
				"export const exported = () => 2;\n" +
				"function outer() {\n" +
				"  const handler = () => load();\n" +
				"  return handler;\n" +
				"}\n",
			want:    []string{"function top@1", "function exported@2", "function outer@3"},
			notWant: []string{"function handler@4"},
		},
		{
			name:    "javascript arrow const inside a function",
			adapter: adapters.NewJavaScriptAdapter(),
			code: "const top = () => 1;\n" +
				"function outer() {\n" +
				"  const handler = () => load();\n" +
				"}\n",
			want:    []string{"function top@1", "function outer@2"},
			notWant: []string{"function handler@3"},
		},
		{
			name:    "rust const and static inside a function",
			adapter: adapters.NewRustAdapter(),
			code: "const TOP: i32 = 1;\n" +
				"static S: i32 = 2;\n" +
				"impl Foo {\n" +
				"    const ASSOC: i32 = 3;\n" +
				"}\n" +
				"fn f() {\n" +
				"    const LOCAL: i32 = 4;\n" +
				"    static LS: i32 = 5;\n" +
				"}\n",
			want:    []string{"variable TOP@1", "variable S@2", "variable ASSOC@4", "function f@6"},
			notWant: []string{"variable LOCAL@7", "variable LS@8"},
		},
		{
			name:    "python assignment inside a function",
			adapter: adapters.NewPythonAdapter(),
			code: "TOP = 1\n" +
				"def f():\n" +
				"    local = load()\n",
			want:    []string{"variable TOP@1", "function f@2"},
			notWant: []string{"variable local@3"},
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := extractSymbolKeys(t, tt.adapter, tt.code)
			for _, w := range tt.want {
				assert.Contains(t, got, w)
			}
			for _, nw := range tt.notWant {
				assert.NotContains(t, got, nw)
			}
		})
	}
}

// TestExtractSymbols_MethodContainerPrefix checks that methods keep the name
// of their class, interface, enum, struct, trait or object (M5), and that Go
// generic receivers drop their type parameters.
func TestExtractSymbols_MethodContainerPrefix(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		adapter LanguageAdapter
		code    string
		want    []string
	}{
		{
			name:    "go generic receiver",
			adapter: adapters.NewGoAdapter(),
			code: "package p\n" +
				"func (c *Cache[K, V]) Get() {}\n" +
				"func (c Cache[K, V]) Len() int { return 0 }\n" +
				"func (s *Server) Run() {}\n",
			want: []string{"method Cache.Get@2", "method Cache.Len@3", "method Server.Run@4"},
		},
		{
			name:    "typescript abstract class",
			adapter: adapters.NewTypeScriptAdapter(),
			code: "abstract class Base {\n" +
				"  run() {}\n" +
				"}\n" +
				"export abstract class Shape {\n" +
				"  area() { return 0; }\n" +
				"}\n",
			want: []string{"class Base@1", "method Base.run@2", "class Shape@4", "method Shape.area@5"},
		},
		{
			name:    "tsx abstract class",
			adapter: adapters.NewTSXAdapter(),
			code: "export abstract class Widget {\n" +
				"  render() { return <div/>; }\n" +
				"}\n",
			want: []string{"class Widget@1", "method Widget.render@2"},
		},
		{
			name:    "java interface default method and enum method",
			adapter: adapters.NewJavaAdapter(),
			code: "interface Greeter {\n" +
				"  default void greet() {}\n" +
				"}\n" +
				"enum Color {\n" +
				"  RED;\n" +
				"  void paint() {}\n" +
				"}\n",
			want: []string{"interface Greeter@1", "method Greeter.greet@2", "type Color@4", "method Color.paint@6"},
		},
		{
			name:    "csharp struct method",
			adapter: adapters.NewCSharpAdapter(),
			code: "struct Point {\n" +
				"  public int Len() { return 0; }\n" +
				"}\n",
			want: []string{"type Point@1", "method Point.Len@2"},
		},
		{
			name:    "rust trait default method and generic impl",
			adapter: adapters.NewRustAdapter(),
			code: "trait Speak {\n" +
				"    fn speak(&self) {}\n" +
				"}\n" +
				"impl<T> Stack<T> {\n" +
				"    fn push(&mut self) {}\n" +
				"}\n",
			want: []string{"interface Speak@1", "method Speak.speak@2", "class Stack@4", "method Stack.push@5"},
		},
		{
			name:    "kotlin class and object methods",
			adapter: adapters.NewKotlinAdapter(),
			code: "class Repo {\n" +
				"    fun find() {}\n" +
				"}\n" +
				"object Registry {\n" +
				"    fun lookup() {}\n" +
				"}\n",
			want: []string{"class Repo@1", "method Repo.find@2", "class Registry@4", "method Registry.lookup@5"},
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := extractSymbolKeys(t, tt.adapter, tt.code)
			for _, w := range tt.want {
				assert.Contains(t, got, w)
			}
		})
	}
}

// TestExtractSymbols_SameNameSymbolsKept checks that same-name symbols in one
// file are all extracted, one per start line (M1's extractor half; the store
// keeps them since migration 012).
func TestExtractSymbols_SameNameSymbolsKept(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		adapter LanguageAdapter
		code    string
		want    []string
	}{
		{
			name:    "go multiple init",
			adapter: adapters.NewGoAdapter(),
			code:    "package p\nfunc init() {}\nfunc init() {}\n",
			want:    []string{"function init@2", "function init@3"},
		},
		{
			name:    "java overloads",
			adapter: adapters.NewJavaAdapter(),
			code: "class A {\n" +
				"  void f(int x) {}\n" +
				"  void f(String s) {}\n" +
				"}\n",
			want: []string{"method A.f@2", "method A.f@3"},
		},
		{
			name:    "kotlin overloads",
			adapter: adapters.NewKotlinAdapter(),
			code: "class A {\n" +
				"    fun f(x: Int) {}\n" +
				"    fun f(s: String) {}\n" +
				"}\n",
			want: []string{"method A.f@2", "method A.f@3"},
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := extractSymbolKeys(t, tt.adapter, tt.code)
			for _, w := range tt.want {
				assert.Contains(t, got, w)
			}
		})
	}
}
