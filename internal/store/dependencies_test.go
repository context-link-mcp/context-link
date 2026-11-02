package store

import (
	"context"
	"testing"

	"github.com/context-link-mcp/context-link/pkg/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInsertDependency(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	ctx := context.Background()

	// Insert two symbols to reference.
	insertTestSymbol(t, db, "repo", "caller", "caller", "function", "src/a.ts")
	insertTestSymbol(t, db, "repo", "callee", "callee", "function", "src/b.ts")

	callerSym, err := GetSymbolByName(ctx, db, "repo", "caller")
	require.NoError(t, err)
	calleeSym, err := GetSymbolByName(ctx, db, "repo", "callee")
	require.NoError(t, err)

	dep := &models.Dependency{
		CallerID: callerSym.ID,
		CalleeID: calleeSym.ID,
		Kind:     "call",
	}
	require.NoError(t, InsertDependency(ctx, db, dep))

	deps, err := GetDependenciesByCaller(ctx, db, callerSym.ID)
	require.NoError(t, err)
	assert.Len(t, deps, 1)
	assert.Equal(t, calleeSym.ID, deps[0].CalleeID)
	assert.Equal(t, "call", deps[0].Kind)
}

func TestInsertDependency_DuplicateIgnored(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	ctx := context.Background()

	insertTestSymbol(t, db, "repo", "a", "a", "function", "src/a.ts")
	insertTestSymbol(t, db, "repo", "b", "b", "function", "src/b.ts")

	aSym, _ := GetSymbolByName(ctx, db, "repo", "a")
	bSym, _ := GetSymbolByName(ctx, db, "repo", "b")

	dep := &models.Dependency{CallerID: aSym.ID, CalleeID: bSym.ID, Kind: "call"}
	require.NoError(t, InsertDependency(ctx, db, dep))
	require.NoError(t, InsertDependency(ctx, db, dep)) // duplicate — should be ignored

	deps, err := GetDependenciesByCaller(ctx, db, aSym.ID)
	require.NoError(t, err)
	assert.Len(t, deps, 1)
}

func TestBatchInsertDependencies(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	ctx := context.Background()

	insertTestSymbol(t, db, "repo", "caller", "caller", "function", "src/a.ts")
	insertTestSymbol(t, db, "repo", "dep1", "dep1", "function", "src/b.ts")
	insertTestSymbol(t, db, "repo", "dep2", "dep2", "function", "src/c.ts")

	callerSym, _ := GetSymbolByName(ctx, db, "repo", "caller")
	dep1Sym, _ := GetSymbolByName(ctx, db, "repo", "dep1")
	dep2Sym, _ := GetSymbolByName(ctx, db, "repo", "dep2")

	deps := []models.Dependency{
		{CallerID: callerSym.ID, CalleeID: dep1Sym.ID, Kind: "call"},
		{CallerID: callerSym.ID, CalleeID: dep2Sym.ID, Kind: "import"},
	}
	require.NoError(t, BatchInsertDependencies(ctx, db, deps))

	got, err := GetDependenciesByCaller(ctx, db, callerSym.ID)
	require.NoError(t, err)
	assert.Len(t, got, 2)
}

func TestBatchInsertDependencies_Empty(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	ctx := context.Background()

	err := BatchInsertDependencies(ctx, db, nil)
	assert.NoError(t, err)
}

func TestGetDependenciesByCallee(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	ctx := context.Background()

	insertTestSymbol(t, db, "repo", "a", "a", "function", "src/a.ts")
	insertTestSymbol(t, db, "repo", "b", "b", "function", "src/b.ts")

	aSym, _ := GetSymbolByName(ctx, db, "repo", "a")
	bSym, _ := GetSymbolByName(ctx, db, "repo", "b")

	require.NoError(t, InsertDependency(ctx, db, &models.Dependency{
		CallerID: aSym.ID, CalleeID: bSym.ID, Kind: "call",
	}))

	// Query reverse: who calls b?
	deps, err := GetDependenciesByCallee(ctx, db, bSym.ID)
	require.NoError(t, err)
	assert.Len(t, deps, 1)
	assert.Equal(t, aSym.ID, deps[0].CallerID)
}

func TestGetDirectDependencies(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	ctx := context.Background()

	insertTestSymbol(t, db, "repo", "a", "a", "function", "src/a.ts")
	insertTestSymbol(t, db, "repo", "b", "b", "function", "src/b.ts")
	insertTestSymbol(t, db, "repo", "c", "c", "function", "src/c.ts")
	insertTestSymbol(t, db, "repo", "lonely", "lonely", "function", "src/d.ts")
	insertTestSymbol(t, db, "other", "x", "x", "function", "src/x.ts")

	id := func(repo, name string) int64 {
		s, err := GetSymbolByName(ctx, db, repo, name)
		require.NoError(t, err)
		return s.ID
	}
	a, b, c, lonely, x := id("repo", "a"), id("repo", "b"), id("repo", "c"), id("repo", "lonely"), id("other", "x")

	require.NoError(t, BatchInsertDependencies(ctx, db, []models.Dependency{
		{CallerID: a, CalleeID: c, Kind: "call"},
		{CallerID: a, CalleeID: b, Kind: "call"},
		{CallerID: a, CalleeID: b, Kind: "import"}, // same callee, second kind
		{CallerID: c, CalleeID: b, Kind: "call"},   // b shared by a and c
		{CallerID: a, CalleeID: a, Kind: "call"},   // recursion
		{CallerID: a, CalleeID: x, Kind: "call"},   // callee in another repo
	}))

	names := func(syms []models.Symbol) []string {
		out := make([]string, len(syms))
		for i, s := range syms {
			out[i] = s.Name
		}
		return out
	}

	tests := []struct {
		name    string
		callers []int64
		want    map[int64][]string
	}{
		{"no callers", nil, map[int64][]string{}},
		{"caller without dependencies", []int64{lonely}, map[int64][]string{}},
		{"dedup, self-edge and other repo skipped", []int64{a}, map[int64][]string{a: {"b", "c"}}},
		{"shared callee", []int64{a, c}, map[int64][]string{a: {"b", "c"}, c: {"b"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := GetDirectDependencies(ctx, db, "repo", tt.callers)
			require.NoError(t, err)
			gotNames := make(map[int64][]string, len(got))
			for k, v := range got {
				gotNames[k] = names(v)
			}
			assert.Equal(t, tt.want, gotNames)
		})
	}
}

func TestDeleteDependenciesByCallerFile(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	ctx := context.Background()

	insertTestSymbol(t, db, "repo", "a", "a", "function", "src/a.ts")
	insertTestSymbol(t, db, "repo", "b", "b", "function", "src/b.ts")
	insertTestSymbol(t, db, "repo", "c", "c", "function", "src/c.ts")

	aSym, _ := GetSymbolByName(ctx, db, "repo", "a")
	bSym, _ := GetSymbolByName(ctx, db, "repo", "b")
	cSym, _ := GetSymbolByName(ctx, db, "repo", "c")

	// a calls b (from src/a.ts), c calls b (from src/c.ts)
	require.NoError(t, InsertDependency(ctx, db, &models.Dependency{CallerID: aSym.ID, CalleeID: bSym.ID, Kind: "call"}))
	require.NoError(t, InsertDependency(ctx, db, &models.Dependency{CallerID: cSym.ID, CalleeID: bSym.ID, Kind: "call"}))

	// Delete deps from src/a.ts.
	require.NoError(t, DeleteDependenciesByCallerFile(ctx, db, "repo", "src/a.ts"))

	// a's deps should be gone.
	aDeps, err := GetDependenciesByCaller(ctx, db, aSym.ID)
	require.NoError(t, err)
	assert.Empty(t, aDeps)

	// c's deps should remain.
	cDeps, err := GetDependenciesByCaller(ctx, db, cSym.ID)
	require.NoError(t, err)
	assert.Len(t, cDeps, 1)
}

func TestDetachFileDependencies(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	ctx := context.Background()

	insertTestSymbol(t, db, "repo", "caller", "caller", "function", "src/a.ts")
	insertTestSymbol(t, db, "repo", "callee", "Svc.callee", "method", "src/b.ts")
	insertTestSymbol(t, db, "repo", "inner", "inner", "function", "src/b.ts")
	insertTestSymbol(t, db, "repo", "out", "out", "function", "src/c.ts")
	insertTestSymbol(t, db, "other", "callee", "callee", "function", "src/b.ts")

	id := func(repo, qn string) int64 {
		s, err := GetSymbolByQualifiedName(ctx, db, repo, qn)
		require.NoError(t, err)
		return s.ID
	}
	caller, callee, inner, out := id("repo", "caller"), id("repo", "Svc.callee"), id("repo", "inner"), id("repo", "out")
	otherCallee := id("other", "callee")
	require.NoError(t, BatchInsertDependencies(ctx, db, []models.Dependency{
		{CallerID: caller, CalleeID: callee, Kind: "call"},           // inbound: a.ts marked
		{CallerID: inner, CalleeID: callee, Kind: "call"},            // same file: dropped
		{CallerID: inner, CalleeID: out, Kind: "call"},               // outbound: dropped
		{CallerID: caller, CalleeID: out, Kind: "call"},              // unrelated: kept
		{CallerID: otherCallee, CalleeID: otherCallee, Kind: "call"}, // other repo, same path: kept
	}))
	// Call sites by name: d.ts calls "added" (gained by b.ts), e.ts calls
	// "inner" (kept), f.ts imports "added" (not a call), k.ts calls "added"
	// but is re-indexed anyway, h.ts calls "out" (c.ts's), and the other
	// repo's g.ts calls "added".
	for _, cs := range []struct{ repo, file, kind, name string }{
		{"repo", "src/d.ts", "call", "added"},
		{"repo", "src/e.ts", "call", "inner"},
		{"repo", "src/f.ts", "import", "added"},
		{"repo", "src/k.ts", "call", "added"},
		{"repo", "src/h.ts", "call", "out"},
		{"other", "src/g.ts", "call", "added"},
	} {
		_, err := db.ExecContext(ctx, `INSERT INTO call_sites (repo_name, file_path, line, kind, name) VALUES (?, ?, 1, ?, ?)`,
			cs.repo, cs.file, cs.kind, cs.name)
		require.NoError(t, err)
	}

	// b.ts keeps inner and Svc.callee, loses nothing, gains "added".
	require.NoError(t, MarkNameCallers(ctx, db, "repo",
		map[string][]string{"src/b.ts": {"inner", "callee", "Svc.callee", "added"}},
		map[string]bool{"src/k.ts": true}))
	require.NoError(t, DetachFileDependencies(ctx, db, "repo", "src/b.ts"))

	pending, err := GetPendingResolution(ctx, db, "repo")
	require.NoError(t, err)
	assert.Equal(t, []string{"src/a.ts", "src/d.ts"}, pending)
	otherPending, err := GetPendingResolution(ctx, db, "other")
	require.NoError(t, err)
	assert.Empty(t, otherPending, "other repos are never marked")

	var remaining int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM dependencies`).Scan(&remaining))
	assert.Equal(t, 2, remaining, "caller->out and the other repo's edge survive")
	deps, err := GetDependenciesByCaller(ctx, db, caller)
	require.NoError(t, err)
	require.Len(t, deps, 1)
	assert.Equal(t, out, deps[0].CalleeID)

	// A deleted file (no new names) marks callers of every name it had.
	require.NoError(t, MarkNameCallers(ctx, db, "repo", map[string][]string{"src/c.ts": nil}, nil))
	require.NoError(t, DetachFileDependencies(ctx, db, "repo", "src/c.ts"))
	pending, err = GetPendingResolution(ctx, db, "repo")
	require.NoError(t, err)
	assert.Equal(t, []string{"src/a.ts", "src/d.ts", "src/h.ts"}, pending)

	// Writing the resolved edges clears exactly the given marks, atomically.
	require.NoError(t, ReplaceResolvedDependencies(ctx, db, "repo", []string{"src/a.ts"},
		[]models.Dependency{{CallerID: caller, CalleeID: inner, Kind: "call"}}, []string{"src/a.ts", "src/c.ts"}))
	pending, err = GetPendingResolution(ctx, db, "repo")
	require.NoError(t, err)
	assert.Equal(t, []string{"src/d.ts", "src/h.ts"}, pending)
	deps, err = GetDependenciesByCaller(ctx, db, caller)
	require.NoError(t, err)
	require.Len(t, deps, 1, "a.ts's old edges are replaced")
	assert.Equal(t, inner, deps[0].CalleeID)
}
