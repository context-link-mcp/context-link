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

// indexPythonRoutes indexes root as a repo with the Python adapter and
// returns the stored route definitions and the DB.
func indexPythonRoutes(t *testing.T, root string) ([]models.Route, *store.DB) {
	t.Helper()
	ctx := context.Background()
	db := setupTestDB(t)
	registry := NewLanguageRegistry()
	require.NoError(t, registry.Register(adapters.NewPythonAdapter()))
	idx := NewIndexer(registry, db, 2, nil)

	stats, err := idx.IndexRepo(ctx, "repo", root)
	require.NoError(t, err)
	require.Equal(t, 0, stats.Errors)

	routes, err := store.FindRoutes(ctx, db, "repo", store.RouteFilter{Kind: "definition", Unlimited: true})
	require.NoError(t, err)
	assert.Equal(t, len(routes), stats.RoutesExtracted)
	return routes, db
}

// writeTree writes files (slash paths relative to root) under root.
func writeTree(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for rel, src := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte(src), 0o600))
	}
}

// TestDjangoRoutesGolden indexes testdata/langs/python/django and compares
// every stored route with testdata/golden/django_routes.json. On master
// Django files yield no routes at all.
func TestDjangoRoutesGolden(t *testing.T) {
	t.Parallel()
	projectRoot := findProjectRoot(t)
	routes, db := indexPythonRoutes(t, filepath.Join(projectRoot, "testdata", "langs", "python", "django"))
	got := snapshotGoRoutes(t, db, routes)

	goldenPath := filepath.Join(projectRoot, "testdata", "golden", "django_routes.json")
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

// TestDjangoRoutesForms checks the forms the golden file covers one by one,
// so a regression names the form that broke.
func TestDjangoRoutesForms(t *testing.T) {
	t.Parallel()
	projectRoot := findProjectRoot(t)
	routes, db := indexPythonRoutes(t, filepath.Join(projectRoot, "testdata", "langs", "python", "django"))
	snaps := snapshotGoRoutes(t, db, routes)

	type key struct{ file, path string }
	got := make(map[key]goRouteSnapshot, len(snaps))
	for _, s := range snaps {
		assert.Equal(t, "ALL", s.Method, "Django routes declare no method: %+v", s)
		got[key{s.File, s.Path}] = s
	}
	tests := []struct {
		name       string
		file       string
		path       string
		normalized string
		framework  string
		handler    string // linked symbol, "" for none
	}{
		{"same-file function", "mysite/urls.py", "/healthz/", "/healthz/", "django", "mysite/urls.py:healthz"},
		{"lambda is inline", "mysite/urls.py", "/ping/", "/ping/", "django", ""},
		{"empty path is the root", "blog/urls.py", "/", "/", "django", "blog/views.py:index"},
		{"module attribute, converter", "blog/urls.py", "/articles/<int:year>/", "/articles/:param/", "django", "blog/views.py:year_archive"},
		{"as_view links the class", "blog/urls.py", "/articles/<int:year>/<slug:slug>/", "/articles/:param/:param/", "django", "blog/views.py:ArticleDetail"},
		{"module.Class.as_view(kwargs)", "blog/urls.py", "/archive/", "/archive/", "django", "blog/views.py:ArchiveView"},
		{"view= keyword, path converter", "blog/urls.py", "/files/<path:name>", "/files/*", "django", "blog/views.py:serve_file"},
		{"re_path named group", "blog/urls.py", `/^feeds/(?P<feed>[\w-]+)/$`, "/feeds/:param/", "django", "blog/views.py:feed"},
		{"re_path catch-all group", "blog/urls.py", `/^raw/(?P<rest>.*)$`, "/raw/*", "django", "blog/views.py:serve_file"},
		{"re_path unnamed group kept as written", "blog/urls.py", `/^legacy/(\d+)/$`, `/^legacy/(\d+)/$`, "django", "blog/views.py:legacy"},
		{"external class-based view", "blog/urls.py", "/about/", "/about/", "django", ""},
		{"wrapped view has no handler", "blog/urls.py", "/private/", "/private/", "django", ""},
		{"inline include gets the prefix", "blog/urls.py", "/drafts/", "/drafts/", "django", "blog/views.py:drafts"},
		{"inline include, converter", "blog/urls.py", "/drafts/<int:pk>/", "/drafts/:param/", "django", "blog/views.py:draft_detail"},
		{"legacy url() in urlpatterns +=", "blog/urls.py", "/^old-feed/$", "/old-feed/", "django", "blog/views.py:feed"},
		{"function next to a router", "api/urls.py", "/status/", "/status/", "django", "api/views.py:status"},
		{"DRF list route, include prefix", "api/urls.py", "/v1/users/", "/v1/users/", "django-rest-framework", "api/views.py:UserViewSet"},
		{"DRF detail route", "api/urls.py", "/v1/users/<pk>/", "/v1/users/:param/", "django-rest-framework", "api/views.py:UserViewSet"},
		{"DRF basename keyword", "api/urls.py", "/v1/groups/<pk>/", "/v1/groups/:param/", "django-rest-framework", "api/views.py:GroupViewSet"},
	}
	for _, tt := range tests {
		r, ok := got[key{tt.file, tt.path}]
		if !assert.True(t, ok, "%s: no route %s in %s", tt.name, tt.path, tt.file) {
			continue
		}
		assert.Equal(t, tt.normalized, r.NormalizedPath, tt.name)
		assert.Equal(t, tt.framework, r.Framework, tt.name)
		assert.Equal(t, tt.handler, r.Handler, tt.name)
	}

	for _, s := range snaps {
		assert.NotEqual(t, "blog/utils.py", s.File, "path() outside urlpatterns is not a route: %+v", s)
		assert.NotEqual(t, "api/admin.py", s.File, "admin.site.register is not a DRF route: %+v", s)
		assert.NotContains(t, []string{"/admin/", "/blog/", "/api/", "/v1/"}, s.Path, "an include is not a route: %+v", s)
	}
}

// TestDjangoRoutesOnlyInURLPatterns checks that path() calls outside a
// module-level urlpatterns yield nothing, even in a urls.py.
func TestDjangoRoutesOnlyInURLPatterns(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"urls.py": `from django.urls import path

from . import views

patterns = [path("a/", views.a)]
extra = path("b/", views.b)


def build():
    urlpatterns = [path("c/", views.c)]
    return urlpatterns


class Conf:
    urlpatterns = [path("d/", views.d)]


urlpatterns = [
    views.helper("e/"),
    path(some_variable, views.f),
    path(),
]
`,
		"views.py": "def a(r): pass\n",
		// register() on something that isn't a DRF router.
		"registry.py": `from fastapi import APIRouter

router = APIRouter()
router.register("x", Foo)
plugins = Registry()
plugins.register("y", Bar)
`,
	})
	routes, _ := indexPythonRoutes(t, root)
	assert.Empty(t, routes)
}

// TestDjangoClassHandlerOnlyForDjango checks that the class allowance is
// narrow: a Flask route links the function, never a class of the same name
// (also not one that comes first in the file), and a Django view the class.
func TestDjangoClassHandlerOnlyForDjango(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"app.py": `from flask import Flask

app = Flask(__name__)


class Health:
    pass


@app.get("/health")
def Health():
    pass
`,
		"web/urls.py": `from django.urls import path

from .views import Health

urlpatterns = [path("health/", Health.as_view())]
`,
		"web/views.py": "class Health:\n    pass\n",
	})
	routes, db := indexPythonRoutes(t, root)
	require.Len(t, routes, 2)
	var ids []int64
	for _, r := range routes {
		require.NotNil(t, r.HandlerSymbolID, "%s route is linked", r.Framework)
		ids = append(ids, *r.HandlerSymbolID)
	}
	syms, err := store.GetSymbolsByIDs(context.Background(), db, "repo", ids)
	require.NoError(t, err)
	for _, r := range routes {
		s := syms[*r.HandlerSymbolID]
		if r.Framework == "flask" {
			assert.Equal(t, "function", s.Kind)
		} else {
			assert.Equal(t, "class", s.Kind)
			assert.Equal(t, "web/views.py", s.FilePath)
		}
	}
}

// TestDjangoRouteRelinkedAfterViewsReindex checks that a Django route whose
// class-based view lives in views.py is relinked when only views.py changes.
func TestDjangoRouteRelinkedAfterViewsReindex(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"app/urls.py": `from django.urls import path

from . import views

urlpatterns = [path("x/", views.XView.as_view())]
`,
		"app/views.py": "class XView:\n    pass\n",
	})
	db := setupTestDB(t)
	registry := NewLanguageRegistry()
	require.NoError(t, registry.Register(adapters.NewPythonAdapter()))
	idx := NewIndexer(registry, db, 1, nil)

	handlerLine := func() int {
		t.Helper()
		routes, err := store.FindRoutes(ctx, db, "repo", store.RouteFilter{Kind: "definition"})
		require.NoError(t, err)
		require.Len(t, routes, 1)
		if routes[0].HandlerSymbolID == nil {
			return 0
		}
		syms, err := store.GetSymbolsByIDs(ctx, db, "repo", []int64{*routes[0].HandlerSymbolID})
		require.NoError(t, err)
		return syms[*routes[0].HandlerSymbolID].StartLine
	}

	_, err := idx.IndexRepo(ctx, "repo", root)
	require.NoError(t, err)
	assert.Equal(t, 1, handlerLine())

	writeTree(t, root, map[string]string{"app/views.py": "\n\nclass XView:\n    pass\n"})
	stats, err := idx.IndexRepo(ctx, "repo", root)
	require.NoError(t, err)
	require.Equal(t, 1, stats.FilesIndexed, "urls.py is unchanged")
	assert.Equal(t, 3, handlerLine())
}

func TestDjangoRegexPath(t *testing.T) {
	t.Parallel()
	tests := []struct {
		raw, want string
	}{
		{`^$`, ``},
		{`^articles/$`, `articles/`},
		{`^articles/(?P<year>[0-9]{4})/$`, `articles/:param/`},
		{`^(?P<slug>[-\w]+)/(?P<pk>\d+)/$`, `:param/:param/`},
		{`^users/(?P<pk>[^/.]+)/$`, `users/:param/`},
		{`^files/(?P<path>.*)$`, `files/*`},
		{`^files/(?P<path>[\w/]+)$`, `files/*`},
		{`^robots\.txt$`, `robots.txt`},
		{`articles/`, `articles/`},
		{`^price\$$`, `^price\$$`},
		{`^legacy/(\d+)/$`, `^legacy/(\d+)/$`},
		{`^(?:en|de)/home/$`, `^(?:en|de)/home/$`},
		{`^a|b$`, `^a|b$`},
		{`^page/\d+/$`, `^page/\d+/$`},
		{`^x/(?P<id>\d+$`, `^x/(?P<id>\d+$`},
		{`^x/(?P<1d>\d+)/$`, `^x/(?P<1d>\d+)/$`},
		{`^x/.*$`, `^x/.*$`},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.want, djangoRegexPath(tt.raw), tt.raw)
	}
}
