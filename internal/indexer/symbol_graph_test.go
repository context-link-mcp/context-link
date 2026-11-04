package indexer

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/context-link-mcp/context-link/internal/indexer/adapters"
)

// TestLocalVariablesDoNotStealEdges guards against function-local variables
// being symbols (M4): as the smallest span around a call they became its
// caller, giving cfg -> Load instead of Run -> Load.
func TestLocalVariablesDoNotStealEdges(t *testing.T) {
	t.Parallel()
	r := newResolutionRepo(t, map[string]string{
		"app/run.go": "package app\n\n" +
			"func Load() int { return 1 }\n\n" +
			"func Run() {\n" +
			"\tvar cfg = Load()\n" +
			"\t_ = cfg\n" +
			"}\n",
		"web/app.ts": "function load() { return 1; }\n" +
			"export function start() {\n" +
			"  const handler = () => load();\n" +
			"  return handler;\n" +
			"}\n",
	})
	r.index()

	assert.Equal(t, []string{"app/run.go:Load"}, r.callees("app/run.go", "Run"))
	assert.Empty(t, r.callees("app/run.go", "cfg"))
	assert.Equal(t, []string{"web/app.ts:load"}, r.callees("web/app.ts", "start"))

	var locals int
	require.NoError(t, r.db.QueryRowContext(r.ctx,
		`SELECT COUNT(*) FROM symbols WHERE name IN ('cfg', 'handler')`).Scan(&locals))
	assert.Zero(t, locals)
}

// TestOverloadsAreKeptAndResolveDeterministically checks that overloads in one
// file are all stored (M1) and that a call to the overloaded name resolves to
// the first one by line instead of becoming ambiguous.
func TestOverloadsAreKeptAndResolveDeterministically(t *testing.T) {
	t.Parallel()
	r := newResolutionRepo(t, nil)
	registry := NewLanguageRegistry()
	require.NoError(t, registry.Register(adapters.NewJavaAdapter()))
	r.idx = NewIndexer(registry, r.db, 1, nil)
	r.write("A.java", "class A {\n"+
		"  void f(int x) {}\n"+
		"  void f(String s) {}\n"+
		"  void run() { f(1); }\n"+
		"}\n")
	r.index()

	rows, err := r.db.QueryContext(r.ctx,
		`SELECT start_line FROM symbols WHERE qualified_name = 'A.f' ORDER BY start_line`)
	require.NoError(t, err)
	var lines []int
	for rows.Next() {
		var l int
		require.NoError(t, rows.Scan(&l))
		lines = append(lines, l)
	}
	require.NoError(t, rows.Err())
	rows.Close()
	assert.Equal(t, []int{2, 3}, lines, "both overloads are stored")

	var calleeLine int
	require.NoError(t, r.db.QueryRowContext(r.ctx, `
		SELECT callee.start_line FROM dependencies d
		JOIN symbols caller ON caller.id = d.caller_id
		JOIN symbols callee ON callee.id = d.callee_id
		WHERE caller.qualified_name = 'A.run'`).Scan(&calleeLine))
	assert.Equal(t, 2, calleeLine, "the call resolves to the first overload")
}
