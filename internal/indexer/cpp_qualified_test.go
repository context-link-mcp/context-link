package indexer

import (
	"context"
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/context-link-mcp/context-link/internal/indexer/adapters"
	"github.com/context-link-mcp/context-link/internal/store"
)

// newCppRepo is a temp repo indexed with the C and C++ adapters.
func newCppRepo(t *testing.T, files map[string]string) *resolutionRepo {
	t.Helper()
	registry := NewLanguageRegistry()
	require.NoError(t, registry.Register(adapters.NewCAdapter()))
	require.NoError(t, registry.Register(adapters.NewCppAdapter()))
	r := &resolutionRepo{t: t, ctx: context.Background(), db: setupTestDB(t), root: t.TempDir()}
	r.idx = NewIndexer(registry, r.db, 1, nil)
	for name, content := range files {
		r.write(name, content)
	}
	return r
}

// qualifiedNames returns "qualified_name kind" of every symbol in a file.
func qualifiedNames(t *testing.T, db *store.DB, file string) []string {
	t.Helper()
	rows, err := db.QueryContext(context.Background(), `
		SELECT qualified_name || ' ' || kind FROM symbols
		WHERE repo_name = 'repo' AND file_path = ?`, file)
	require.NoError(t, err)
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var s string
		require.NoError(t, rows.Scan(&s))
		out = append(out, s)
	}
	require.NoError(t, rows.Err())
	sort.Strings(out)
	return out
}

// TestCppOutOfClassMethodNames guards against int Foo::bar() {} being stored
// as a bare "bar" (no class prefix), and against out-of-class destructors,
// operators, pointer/reference returns and nested scopes not being symbols.
func TestCppOutOfClassMethodNames(t *testing.T) {
	t.Parallel()
	r := newCppRepo(t, map[string]string{"src/foo.cpp": `class Foo {
public:
    int inl() { return 1; }
    ~Foo() {}
};
Foo::Foo() {}
Foo::~Foo() {}
int Foo::bar() { return 0; }
bool Foo::operator==(const Foo& o) const { return true; }
int* Foo::ptr() { return 0; }
Foo& Foo::self() { return *this; }
int ns::Foo::nsd() { return 0; }
int A::B::nested() { return 0; }
template <typename T> T Box<T>::get() { return T(); }
namespace app { int Svc::run() { return 0; } }
int util::clamp(int x) { return x; }
int helper() { return 0; }
`})
	r.index()
	assert.Equal(t, []string{
		"B.nested method",
		"Box.get method",
		"Foo class",
		"Foo.Foo method",
		"Foo.bar method",
		"Foo.inl method",
		"Foo.nsd method",
		"Foo.operator== method",
		"Foo.ptr method",
		"Foo.self method",
		"Foo.~Foo method",
		"Foo.~Foo method", // in-class and out-of-class destructor, different lines
		"Svc.run method",
		"helper function",
		"util.clamp method", // a namespace function keeps kind method (see cppScopedName)
	}, qualifiedNames(t, r.db, "src/foo.cpp"))
}

// TestCppCallsResolveThroughClassScope guards against C++ calls ignoring the
// class of out-of-class methods: Foo::bar() never matched (the call name was
// "Foo::bar"), and this->bar() from Foo::run() was ambiguous when another
// class in the file also had bar(), because Foo::run had no container.
func TestCppCallsResolveThroughClassScope(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		files  map[string]string
		file   string
		caller string
		want   []string
	}{
		{
			name: "this-> call prefers the caller's own class",
			files: map[string]string{"src/foo.cpp": `int Foo::bar() { return 1; }
int Other::bar() { return 2; }
int Foo::run() { return this->bar(); }
`},
			file: "src/foo.cpp", caller: "Foo.run",
			want: []string{"src/foo.cpp:Foo.bar"},
		},
		{
			name: "plain call from a method prefers the caller's own class",
			files: map[string]string{"src/foo.cpp": `int Foo::bar() { return 1; }
int Other::bar() { return 2; }
int Foo::run() { return bar(); }
`},
			file: "src/foo.cpp", caller: "Foo.run",
			want: []string{"src/foo.cpp:Foo.bar"},
		},
		{
			name: "qualified call resolves by class, not by bare name",
			files: map[string]string{"src/foo.cpp": `int Foo::bar() { return 1; }
int Other::bar() { return 2; }
int run() { return Other::bar(); }
`},
			file: "src/foo.cpp", caller: "run",
			want: []string{"src/foo.cpp:Other.bar"},
		},
		{
			name: "namespace-qualified call keeps the last scope segment",
			files: map[string]string{
				"src/main.cpp": "int run() { return ns::Foo::create(); }\n",
				"lib/foo.cpp":  "int ns::Foo::create() { return 1; }\n",
				"lib/bar.cpp":  "int Bar::create() { return 2; }\n",
			},
			file: "src/main.cpp", caller: "run",
			want: []string{"lib/foo.cpp:Foo.create"},
		},
		{
			name: "qualified call to an in-class definition in a header",
			files: map[string]string{
				"src/main.cpp": "#include \"foo.hpp\"\nint run() { return Foo::make(); }\n",
				"src/foo.hpp":  "class Foo {\npublic:\n    static int make() { return 1; }\n};\n",
				"src/bar.hpp":  "class Bar {\npublic:\n    static int make() { return 2; }\n};\n",
			},
			file: "src/main.cpp", caller: "run",
			want: []string{"src/foo.hpp:Foo.make"},
		},
		{
			name: "member call on an object resolves to the out-of-class definition",
			files: map[string]string{
				"src/main.cpp": "int run() { Foo f; return f.bar(); }\n",
				"src/foo.cpp":  "int Foo::bar() { return 1; }\n",
			},
			file: "src/main.cpp", caller: "run",
			want: []string{"src/foo.cpp:Foo.bar"},
		},
		{
			name: "std:: call does not link to a repo function of the same name",
			files: map[string]string{
				"src/main.cpp": "int run() { std::sort(a, b); return 0; }\n",
				"src/sort.cpp": "void sort(int a, int b) {}\n",
			},
			file: "src/main.cpp", caller: "run",
			want: []string{},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := newCppRepo(t, tc.files)
			r.index()
			assert.Equal(t, tc.want, r.callees(tc.file, tc.caller))
		})
	}
}
