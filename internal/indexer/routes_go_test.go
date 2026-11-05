package indexer

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/context-link-mcp/context-link/internal/indexer/adapters"
	"github.com/context-link-mcp/context-link/internal/store"
	"github.com/context-link-mcp/context-link/pkg/models"
)

// goRouteSnapshot is a stored route as the golden file records it.
type goRouteSnapshot struct {
	Method         string `json:"method"`
	Path           string `json:"path"`
	NormalizedPath string `json:"normalized_path"`
	Framework      string `json:"framework"`
	File           string `json:"file"`
	Line           int    `json:"line"`
	HandlerName    string `json:"handler_name,omitempty"`
	// Handler is the linked symbol as file:qualified_name, empty if unlinked.
	Handler string `json:"handler,omitempty"`
}

// indexGoRoutes indexes root as a repo with the Go adapter and returns the
// stored route definitions and the DB.
func indexGoRoutes(t *testing.T, root string) ([]models.Route, *store.DB) {
	t.Helper()
	ctx := context.Background()
	db := setupTestDB(t)
	registry := NewLanguageRegistry()
	require.NoError(t, registry.Register(adapters.NewGoAdapter()))
	idx := NewIndexer(registry, db, 2, nil)

	stats, err := idx.IndexRepo(ctx, "repo", root)
	require.NoError(t, err)
	require.Equal(t, 0, stats.Errors)

	routes, err := store.FindRoutes(ctx, db, "repo", store.RouteFilter{Kind: "definition", Unlimited: true})
	require.NoError(t, err)
	assert.Equal(t, len(routes), stats.RoutesExtracted)
	return routes, db
}

// snapshotGoRoutes converts routes to golden entries, sorted by file, line,
// method and path.
func snapshotGoRoutes(t *testing.T, db *store.DB, routes []models.Route) []goRouteSnapshot {
	t.Helper()
	var ids []int64
	for _, r := range routes {
		if r.HandlerSymbolID != nil {
			ids = append(ids, *r.HandlerSymbolID)
		}
	}
	syms, err := store.GetSymbolsByIDs(context.Background(), db, "repo", ids)
	require.NoError(t, err)

	out := make([]goRouteSnapshot, 0, len(routes))
	for _, r := range routes {
		s := goRouteSnapshot{
			Method: r.Method, Path: r.PathPattern, NormalizedPath: r.NormalizedPath,
			Framework: r.Framework, File: r.FilePath, Line: r.StartLine, HandlerName: r.HandlerName,
		}
		if r.HandlerSymbolID != nil {
			sym := syms[*r.HandlerSymbolID]
			s.Handler = sym.FilePath + ":" + sym.QualifiedName
		}
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.File != b.File {
			return a.File < b.File
		}
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		if a.Method != b.Method {
			return a.Method < b.Method
		}
		return a.Path < b.Path
	})
	return out
}

// TestGoRoutesGolden indexes testdata/langs/go/routes (Gin, Echo, chi,
// net/http and client calls) and compares every stored route with
// testdata/golden/go_routes.json. On master Go yields no routes at all.
func TestGoRoutesGolden(t *testing.T) {
	t.Parallel()
	projectRoot := findProjectRoot(t)
	routes, db := indexGoRoutes(t, filepath.Join(projectRoot, "testdata", "langs", "go", "routes"))
	got := snapshotGoRoutes(t, db, routes)

	goldenPath := filepath.Join(projectRoot, "testdata", "golden", "go_routes.json")
	if *updateGolden {
		data, err := json.MarshalIndent(got, "", "  ")
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(goldenPath, append(data, '\n'), 0o644))
		t.Logf("Updated golden file: %s", goldenPath)
		return
	}
	data, err := os.ReadFile(goldenPath)
	require.NoError(t, err, "golden file missing — run with -update-golden")
	var want []goRouteSnapshot
	require.NoError(t, json.Unmarshal(data, &want))
	assert.Equal(t, want, got, "route snapshot mismatch — run with -update-golden to update")
}

// TestGoRoutesForms checks the forms the golden file covers one by one, so a
// regression names the form that broke.
func TestGoRoutesForms(t *testing.T) {
	t.Parallel()
	projectRoot := findProjectRoot(t)
	routes, db := indexGoRoutes(t, filepath.Join(projectRoot, "testdata", "langs", "go", "routes"))
	snaps := snapshotGoRoutes(t, db, routes)

	type key struct{ file, method, path string }
	got := make(map[key]goRouteSnapshot, len(snaps))
	for _, s := range snaps {
		got[key{s.File, s.Method, s.Path}] = s
	}
	tests := []struct {
		name      string
		file      string
		method    string
		path      string
		framework string
		handler   string // linked symbol, "" for none
	}{
		{"gin verb", "gin/server.go", "GET", "/health", "gin", "gin/server.go:health"},
		{"gin Any is ALL, func literal is inline", "gin/server.go", "ALL", "/ping", "gin", ""},
		{"gin Handle with http.Method constant", "gin/server.go", "PATCH", "/books/:id", "gin", "gin/server.go:getBook"},
		{"gin Match list (literal)", "gin/server.go", "PUT", "/books/:id", "gin", "gin/server.go:getBook"},
		{"gin Match list (constant)", "gin/server.go", "DELETE", "/books/:id", "gin", "gin/server.go:getBook"},
		{"gin wrapped handler, catch-all", "gin/server.go", "GET", "/static/*filepath", "gin", ""},
		{"gin chained group", "gin/server.go", "GET", "/api/v1/books", "gin", "gin/server.go:listBooks"},
		{"gin middleware before handler", "gin/server.go", "POST", "/api/v1/books", "gin", "gin/server.go:listBooks"},
		{"gin handler in imported package, not the local List", "gin/server.go", "GET", "/api/v1/users", "gin", "handlers/users.go:List"},
		{"gin inline group", "gin/server.go", "DELETE", "/admin/cache", "gin", "gin/server.go:health"},
		{"gin router parameter has no prefix", "gin/server.go", "GET", "/extra", "gin", "gin/server.go:health"},
		{"gin router struct field", "gin/server.go", "POST", "/login", "gin", "gin/server.go:health"},

		{"echo verb", "echo/server.go", "GET", "/orders", "echo", "echo/server.go:listOrders"},
		{"echo handler before middleware", "echo/server.go", "POST", "/orders", "echo", "echo/server.go:listOrders"},
		{"echo Any", "echo/server.go", "ALL", "/any", "echo", "echo/server.go:getOrder"},
		{"echo Add", "echo/server.go", "PATCH", "/orders/:id", "echo", "echo/server.go:getOrder"},
		{"echo Match", "echo/server.go", "HEAD", "/files/*", "echo", "echo/server.go:getOrder"},
		{"echo group", "echo/server.go", "GET", "/admin/orders/:id", "echo", "echo/server.go:getOrder"},
		{"echo wrapped handler", "echo/server.go", "DELETE", "/admin/users/:id", "echo", ""},
		{"echo router parameter", "echo/server.go", "PUT", "/orders/:id", "echo", ""},

		{"chi verb", "chi/server.go", "GET", "/", "chi", "chi/server.go:listArticles"},
		{"chi HandleFunc is ALL", "chi/server.go", "ALL", "/metrics", "chi", "chi/server.go:listArticles"},
		{"chi Method, http.HandlerFunc unwrapped", "chi/server.go", "OPTIONS", "/articles", "chi", "chi/server.go:listArticles"},
		{"chi Handle with method pattern", "chi/server.go", "POST", "/webhook", "chi", "chi/server.go:getArticle"},
		{"chi r.Route", "chi/server.go", "GET", "/api/articles", "chi", "chi/server.go:listArticles"},
		{"chi nested r.Route", "chi/server.go", "GET", "/api/articles/{articleID:[0-9]+}/", "chi", "chi/server.go:getArticle"},
		{"chi r.With", "chi/server.go", "PUT", "/api/articles/{articleID:[0-9]+}/", "chi", "chi/server.go:getArticle"},
		{"chi r.Group keeps the prefix, cross-package handler", "chi/server.go", "POST", "/api/users", "chi", "handlers/users.go:Create"},
		{"chi method value of an imported type", "chi/server.go", "GET", "/users/{id}", "chi", "handlers/users.go:UserHandler.Show"},

		{"net/http default mux", "nethttp/server.go", "ALL", "/", "net/http", "nethttp/server.go:index"},
		{"net/http wrapped handler", "nethttp/server.go", "ALL", "/assets/", "net/http", ""},
		{"go 1.22 method pattern", "nethttp/server.go", "GET", "/items/{id}", "net/http", "nethttp/server.go:getItem"},
		{"go 1.22 inline handler", "nethttp/server.go", "POST", "/items", "net/http", ""},
		{"go 1.22 wildcard", "nethttp/server.go", "ALL", "/files/{path...}", "net/http", "nethttp/server.go:index"},
		{"go 1.22 exact match", "nethttp/server.go", "GET", "/{$}", "net/http", "nethttp/server.go:index"},
		{"host pattern, aliased import", "nethttp/server.go", "ALL", "/about", "net/http", "handlers/users.go:List"},
		{"http.DefaultServeMux", "nethttp/server.go", "ALL", "/debug", "net/http", "nethttp/server.go:index"},
		{"ServeMux struct field", "nethttp/server.go", "DELETE", "/items/{id}", "net/http", "nethttp/server.go:getItem"},
	}
	for _, tt := range tests {
		r, ok := got[key{tt.file, tt.method, tt.path}]
		if !assert.True(t, ok, "%s: no route %s %s in %s", tt.name, tt.method, tt.path, tt.file) {
			continue
		}
		assert.Equal(t, tt.framework, r.Framework, tt.name)
		assert.Equal(t, tt.handler, r.Handler, tt.name)
	}

	for _, s := range snaps {
		assert.NotEqual(t, "clients/clients.go", s.File, "client calls are not routes: %+v", s)
		assert.NotEqual(t, "/wrong", s.Path, "a string handler is not a route")
		assert.NotEqual(t, "/user", s.Path, "echo.Context.Get is not a route")
	}
}

// TestGoRoutesNoRouterImport checks that a file without a router import has
// no routes, even with calls shaped like registrations.
func TestGoRoutesNoRouterImport(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	src := `package x

import "example.com/other/router"

func h() {}

func main() {
	r := router.New()
	r.GET("/x", h)
	r.HandleFunc("/y", h)
}
`
	require.NoError(t, os.WriteFile(filepath.Join(root, "main.go"), []byte(src), 0o600))
	routes, _ := indexGoRoutes(t, root)
	assert.Empty(t, routes)
}

// TestRouteHandlerRelinkedAfterHandlerReindex checks that a route whose
// handler lives in another file keeps its link when only the handler file
// changes: its re-index deletes the handler symbol (the link becomes NULL)
// and the run relinks it by the stored handler name.
func TestRouteHandlerRelinkedAfterHandlerReindex(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	root := t.TempDir()
	write := func(rel, src string) {
		t.Helper()
		p := filepath.Join(root, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte(src), 0o600))
	}
	write("go.mod", "module example.com/app\n")
	write("main.go", `package main

import (
	"github.com/gin-gonic/gin"

	"example.com/app/handlers"
)

func main() {
	r := gin.New()
	r.GET("/users", handlers.List)
}
`)
	write("handlers/users.go", "package handlers\n\nfunc List() {}\n")

	db := setupTestDB(t)
	registry := NewLanguageRegistry()
	require.NoError(t, registry.Register(adapters.NewGoAdapter()))
	idx := NewIndexer(registry, db, 1, nil)

	handlerOf := func() string {
		t.Helper()
		routes, err := store.FindRoutes(ctx, db, "repo", store.RouteFilter{Kind: "definition"})
		require.NoError(t, err)
		require.Len(t, routes, 1)
		if routes[0].HandlerSymbolID == nil {
			return ""
		}
		syms, err := store.GetSymbolsByIDs(ctx, db, "repo", []int64{*routes[0].HandlerSymbolID})
		require.NoError(t, err)
		s := syms[*routes[0].HandlerSymbolID]
		return fmt.Sprintf("%s:%s:%d", s.FilePath, s.Name, s.StartLine)
	}

	_, err := idx.IndexRepo(ctx, "repo", root)
	require.NoError(t, err)
	assert.Equal(t, "handlers/users.go:List:3", handlerOf())

	// Only the handler file changes (List moves down a line).
	write("handlers/users.go", "package handlers\n\n\nfunc List() {}\n")
	stats, err := idx.IndexRepo(ctx, "repo", root)
	require.NoError(t, err)
	require.Equal(t, 1, stats.FilesIndexed, "main.go is unchanged")
	assert.Equal(t, "handlers/users.go:List:4", handlerOf(), "the route links to the new symbol")

	// The handler goes away: no link, and none to anything else.
	write("handlers/users.go", "package handlers\n\nfunc Other() {}\n")
	_, err = idx.IndexRepo(ctx, "repo", root)
	require.NoError(t, err)
	assert.Equal(t, "", handlerOf())
}
