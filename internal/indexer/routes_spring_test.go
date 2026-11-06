package indexer

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/context-link-mcp/context-link/internal/indexer/adapters"
	"github.com/context-link-mcp/context-link/internal/store"
	"github.com/context-link-mcp/context-link/pkg/models"
)

// indexSpringRoutes indexes root as a repo with the Java and Kotlin adapters
// and returns the stored route definitions and the DB.
func indexSpringRoutes(t *testing.T, root string) ([]models.Route, *store.DB) {
	t.Helper()
	ctx := context.Background()
	db := setupTestDB(t)
	registry := NewLanguageRegistry()
	require.NoError(t, registry.Register(adapters.NewJavaAdapter()))
	require.NoError(t, registry.Register(adapters.NewKotlinAdapter()))
	idx := NewIndexer(registry, db, 2, nil)

	stats, err := idx.IndexRepo(ctx, "repo", root)
	require.NoError(t, err)
	require.Equal(t, 0, stats.Errors)

	routes, err := store.FindRoutes(ctx, db, "repo", store.RouteFilter{Kind: "definition", Unlimited: true})
	require.NoError(t, err)
	assert.Equal(t, len(routes), stats.RoutesExtracted)
	return routes, db
}

// TestSpringRoutesGolden indexes testdata/langs/{java,kotlin}/routes and
// compares every stored route with testdata/golden/{java,kotlin}_routes.json.
// On master Java and Kotlin yield no routes at all.
func TestSpringRoutesGolden(t *testing.T) {
	t.Parallel()
	projectRoot := findProjectRoot(t)
	for _, lang := range []string{"java", "kotlin"} {
		t.Run(lang, func(t *testing.T) {
			t.Parallel()
			routes, db := indexSpringRoutes(t, filepath.Join(projectRoot, "testdata", "langs", lang, "routes"))
			got := snapshotGoRoutes(t, db, routes)

			goldenPath := filepath.Join(projectRoot, "testdata", "golden", lang+"_routes.json")
			if *updateGolden {
				data, err := json.MarshalIndent(got, "", "  ")
				require.NoError(t, err)
				require.NoError(t, os.WriteFile(goldenPath, append(data, '\n'), 0o644))
				t.Logf("Updated golden file: %s", goldenPath)
				return
			}
			data, err := os.ReadFile(goldenPath)
			require.NoError(t, err, "golden file missing â€” run with -update-golden")
			var want []goRouteSnapshot
			require.NoError(t, json.Unmarshal(data, &want))
			assert.Equal(t, want, got, "route snapshot mismatch â€” run with -update-golden to update")
		})
	}
}

// TestSpringRoutesForms checks the forms the golden files cover one by one,
// in both languages, so a regression names the form and language that broke.
func TestSpringRoutesForms(t *testing.T) {
	t.Parallel()
	projectRoot := findProjectRoot(t)

	tests := []struct {
		name    string
		method  string
		path    string
		handler string // linked symbol as Class.method:line, "" for none
	}{
		{"marker annotation, class prefix only", "GET", "/api/users", "UserController.list"},
		{"positional path, regex parameter", "GET", `/api/users/{id:\d+}`, "UserController.get:L1"},
		{"value =, overload linked by line", "GET", "/api/users/{id}/avatar", "UserController.get:L2"},
		{"path = with other arguments", "POST", "/api/users/", "UserController.create"},
		{"path array (first)", "PUT", "/api/users/{id}", "UserController.update"},
		{"path array (second)", "PUT", "/api/users/{id}/replace", "UserController.update"},
		{"positional array (first)", "DELETE", "/api/users/{id}", "UserController.delete"},
		{"positional array (second)", "DELETE", "/api/users/remove/{id}", "UserController.delete"},
		{"fully qualified annotation", "PATCH", "/api/users/{id}", "UserController.patch"},
		{"RequestMapping with method", "GET", "/api/users/search", "UserController.search"},
		{"RequestMapping with method array (first)", "POST", "/api/users/bulk", "UserController.bulk"},
		{"RequestMapping with method array (second)", "PUT", "/api/users/bulk", "UserController.bulk"},
		{"RequestMapping without method is ALL", "ALL", "/api/users/legacy", "UserController.legacy"},
		{"@Controller without prefix or path", "GET", "/", "PageController.home"},
		{"@Controller without prefix", "GET", "/about", "PageController.about"},
		{"class path array (first)", "GET", "/v1/orders/{orderId}", "OrderController.get"},
		{"class path array (second)", "GET", "/v2/orders/{orderId}", "OrderController.get"},
		{"class path array, no method path", "DELETE", "/v1/orders", "OrderController.clear"},
		{"class path array, no method path (second)", "DELETE", "/v2/orders", "OrderController.clear"},
	}
	// Paths that must not be routes in either language.
	absent := []string{"/inner", "/svc/users", "/users", "/svc/all", "/all", "/users/{id}", "/plain", "/top"}

	for _, lang := range []string{"java", "kotlin"} {
		t.Run(lang, func(t *testing.T) {
			t.Parallel()
			routes, db := indexSpringRoutes(t, filepath.Join(projectRoot, "testdata", "langs", lang, "routes"))

			var ids []int64
			for _, r := range routes {
				if r.HandlerSymbolID != nil {
					ids = append(ids, *r.HandlerSymbolID)
				}
			}
			syms, err := store.GetSymbolsByIDs(context.Background(), db, "repo", ids)
			require.NoError(t, err)

			type key struct{ method, path string }
			got := make(map[key]string, len(routes))
			for _, r := range routes {
				assert.Equal(t, "spring", r.Framework)
				ext := ".java"
				if lang == "kotlin" {
					ext = ".kt"
				}
				assert.Equal(t, "UserController"+ext, r.FilePath, "only controllers have routes: %+v", r)
				assert.NotContains(t, absent, r.PathPattern)
				h := ""
				if r.HandlerSymbolID != nil {
					s := syms[*r.HandlerSymbolID]
					h = s.QualifiedName
					assert.Equal(t, r.FilePath, s.FilePath)
					assert.Equal(t, s.QualifiedName, r.HandlerName, "the handler name is Class.method")
					if s.QualifiedName == "UserController.get" {
						h += ":" + overloadLabel(syms, s)
					}
				}
				got[key{r.Method, r.PathPattern}] = h
			}

			for _, tt := range tests {
				h, ok := got[key{tt.method, tt.path}]
				if !assert.True(t, ok, "%s: no route %s %s", tt.name, tt.method, tt.path) {
					continue
				}
				assert.Equal(t, tt.handler, h, tt.name)
			}
			assert.Len(t, routes, len(tests), "no other routes (constants, templates, helpers, nested classes)")
		})
	}
}

// overloadLabel names an overload of s by its order among the symbols of the
// same qualified name in syms: L1 for the first by line, L2 for the second.
func overloadLabel(syms map[int64]models.Symbol, s models.Symbol) string {
	n := 1
	for _, o := range syms {
		if o.QualifiedName == s.QualifiedName && o.FilePath == s.FilePath && o.StartLine < s.StartLine {
			n++
		}
	}
	return "L" + string(rune('0'+n))
}

// TestSpringRoutesHandlerNeverResolvedElsewhere checks that a Spring route
// whose method is not a symbol (an abstract method has no body) stays without
// a handler, and is not linked to a same-named method of another class, in
// its file or in another one (not even a class of the same name in another
// package), on the first run or on a later one. Without the Spring skip in
// linkRouteHandlers, /base/item links to legacy/BaseController.java.
func TestSpringRoutesHandlerNeverResolvedElsewhere(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	root := t.TempDir()
	write := func(rel, src string) {
		t.Helper()
		require.NoError(t, os.WriteFile(filepath.Join(root, rel), []byte(src), 0o600))
	}
	write("BaseController.java", `package x;

@RestController
@RequestMapping("/base")
abstract class BaseController {

    @GetMapping("/item")
    public abstract Item get();

    @GetMapping("/ok")
    public Item ok() {
        return null;
    }
}

class Other {
    public Item get() {
        return null;
    }
}
`)
	// A class of the same name in another package: BaseController.get is
	// unique in the repo, but it is another method.
	require.NoError(t, os.MkdirAll(filepath.Join(root, "legacy"), 0o755))
	write(filepath.Join("legacy", "BaseController.java"), `package x.legacy;

class BaseController {
    public Item get() {
        return null;
    }
}
`)
	write("Repo.java", `package x;

class Repo {
    public Item get() {
        return null;
    }
}
`)
	db := setupTestDB(t)
	registry := NewLanguageRegistry()
	require.NoError(t, registry.Register(adapters.NewJavaAdapter()))
	idx := NewIndexer(registry, db, 1, nil)

	handlers := func() map[string]string {
		t.Helper()
		routes, err := store.FindRoutes(ctx, db, "repo", store.RouteFilter{Kind: "definition", Unlimited: true})
		require.NoError(t, err)
		out := make(map[string]string, len(routes))
		for _, r := range routes {
			out[r.PathPattern] = ""
			if r.HandlerSymbolID != nil {
				syms, err := store.GetSymbolsByIDs(ctx, db, "repo", []int64{*r.HandlerSymbolID})
				require.NoError(t, err)
				s := syms[*r.HandlerSymbolID]
				out[r.PathPattern] = s.FilePath + ":" + s.QualifiedName
			}
		}
		return out
	}
	want := map[string]string{"/base/item": "", "/base/ok": "BaseController.java:BaseController.ok"}

	_, err := idx.IndexRepo(ctx, "repo", root)
	require.NoError(t, err)
	assert.Equal(t, want, handlers())

	// Another file changes: the unlinked route is still not resolved by name.
	write("Repo.java", "package x;\n\n\nclass Repo {\n    public Item get() {\n        return null;\n    }\n}\n")
	_, err = idx.IndexRepo(ctx, "repo", root)
	require.NoError(t, err)
	assert.Equal(t, want, handlers())
}

// TestUnescapeJVMString covers the escapes of Java and Kotlin string literals
// a path can contain.
func TestUnescapeJVMString(t *testing.T) {
	t.Parallel()
	tests := []struct{ name, in, want string }{
		{"plain", "/users/{id}", "/users/{id}"},
		{"backslash", `/users/{id:\\d+}`, `/users/{id:\d+}`},
		{"quote", `/a\"b`, `/a"b`},
		{"kotlin dollar", `/price/\$`, "/price/$"},
		{"unicode", "/caf\\u00e9", "/café"},
		{"java multi-u unicode", `/\uuu0041`, "/A"},
		{"bad unicode", `/\u12`, "/u12"},
		{"newline", `a\nb`, "a\nb"},
		{"trailing backslash", `a\`, `a\`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, unescapeJVMString(tt.in))
		})
	}
}
