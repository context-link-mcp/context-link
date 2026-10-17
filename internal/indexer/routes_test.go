package indexer

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/context-link-mcp/context-link/internal/indexer/adapters"
	"github.com/context-link-mcp/context-link/internal/store"
	"github.com/context-link-mcp/context-link/pkg/models"
)

const fastAPISource = `from fastapi import APIRouter, Depends

router = APIRouter(prefix="/api/models")


@router.get("/")
async def list_models():
    return []


@router.post("/{model_id}/run", status_code=201)
def run_model(model_id: int):
    return {}


@router.get("")
def root_of_router():
    pass


@router.get(some_variable)
def dynamic_path():
    pass


@cache.get("key")
def not_a_route():
    pass
`

const flaskSource = `from flask import Flask, Blueprint

app = Flask(__name__)
bp = Blueprint("users", __name__, url_prefix="/users")


@app.route("/health")
def health():
    return "ok"


@app.route("/items", methods=["GET", "POST"])
def items():
    return "items"


@bp.get("/<int:user_id>")
def get_user(user_id):
    return "user"
`

const expressSource = `const express = require('express');
const app = express();
const router = express.Router();

function listUsers(req, res) { res.json([]); }

app.get('/users', listUsers);
app.post('/users/:id/avatar', auth, async (req, res) => { res.sendStatus(204); });
router.delete('/users/:id', controller.remove);

// HTTP client calls, not routes.
axios.get('/api/users');
axios.post('/api/users', { name: 'x' });
cache.get('/not-a-route');
`

// indexRoutes indexes a single-file repo and returns the stored routes.
func indexRoutes(t *testing.T, name, source string) []models.Route {
	t.Helper()
	ctx := context.Background()
	db := setupTestDB(t)

	registry := NewLanguageRegistry()
	require.NoError(t, registry.Register(adapters.NewPythonAdapter()))
	require.NoError(t, registry.Register(adapters.NewJavaScriptAdapter()))
	require.NoError(t, registry.Register(adapters.NewTypeScriptAdapter()))
	idx := NewIndexer(registry, db, 1, nil)

	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, name), []byte(source), 0o600))

	stats, err := idx.IndexRepo(ctx, "repo", root)
	require.NoError(t, err)
	require.Equal(t, 0, stats.Errors)

	routes, err := store.FindRoutes(ctx, db, "repo", store.RouteFilter{Kind: "definition"})
	require.NoError(t, err)
	assert.Equal(t, len(routes), stats.RoutesExtracted)
	return routes
}

type routeKey struct{ method, path string }

func routeSet(routes []models.Route) map[routeKey]models.Route {
	m := make(map[routeKey]models.Route, len(routes))
	for _, r := range routes {
		m[routeKey{r.Method, r.PathPattern}] = r
	}
	return m
}

func TestExtractRoutesFastAPI(t *testing.T) {
	t.Parallel()
	routes := indexRoutes(t, "models.py", fastAPISource)
	got := routeSet(routes)

	require.Len(t, routes, 3)
	assert.Contains(t, got, routeKey{"GET", "/api/models"}, `@router.get("") maps to the prefix`)
	list := got[routeKey{"GET", "/api/models/"}]
	assert.Equal(t, "fastapi", list.Framework)
	assert.Equal(t, "/api/models/", list.NormalizedPath)
	assert.Equal(t, 6, list.StartLine)
	require.NotNil(t, list.HandlerSymbolID, "handler should link to list_models")

	run := got[routeKey{"POST", "/api/models/{model_id}/run"}]
	assert.Equal(t, "/api/models/:param/run", run.NormalizedPath)
	require.NotNil(t, run.HandlerSymbolID)
}

func TestExtractRoutesFlask(t *testing.T) {
	t.Parallel()
	routes := indexRoutes(t, "app.py", flaskSource)
	got := routeSet(routes)

	require.Len(t, routes, 4)
	assert.Contains(t, got, routeKey{"GET", "/health"}, "route() defaults to GET")
	assert.Contains(t, got, routeKey{"GET", "/items"})
	assert.Contains(t, got, routeKey{"POST", "/items"})
	assert.Contains(t, got, routeKey{"GET", "/users/<int:user_id>"}, "blueprint url_prefix applied")
	for _, r := range routes {
		assert.Equal(t, "flask", r.Framework)
	}
}

func TestExtractRoutesExpress(t *testing.T) {
	t.Parallel()
	routes := indexRoutes(t, "server.js", expressSource)
	got := routeSet(routes)

	require.Len(t, routes, 3, "axios/cache calls must not be reported as routes")
	users := got[routeKey{"GET", "/users"}]
	assert.Equal(t, "express", users.Framework)
	require.NotNil(t, users.HandlerSymbolID, "named handler links to listUsers")

	avatar := got[routeKey{"POST", "/users/:id/avatar"}]
	assert.Nil(t, avatar.HandlerSymbolID, "inline handler has no symbol")

	assert.Contains(t, got, routeKey{"DELETE", "/users/:id"})
}

func TestExtractRoutesReplacedOnReindex(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := setupTestDB(t)
	registry := NewLanguageRegistry()
	require.NoError(t, registry.Register(adapters.NewJavaScriptAdapter()))
	idx := NewIndexer(registry, db, 1, nil)

	root := t.TempDir()
	file := filepath.Join(root, "server.js")
	require.NoError(t, os.WriteFile(file, []byte(expressSource), 0o600))
	_, err := idx.IndexRepo(ctx, "repo", root)
	require.NoError(t, err)

	// Same path, new content: old routes must be replaced, not duplicated.
	require.NoError(t, os.WriteFile(file, []byte("app.get('/only', h);\nfunction h(req, res) {}\n"), 0o600))
	_, err = idx.IndexRepo(ctx, "repo", root)
	require.NoError(t, err)

	routes, err := store.FindRoutes(ctx, db, "repo", store.RouteFilter{})
	require.NoError(t, err)
	require.Len(t, routes, 1)
	assert.Equal(t, "/only", routes[0].PathPattern)

	// Removing the file removes its routes.
	require.NoError(t, os.Remove(file))
	_, err = idx.IndexRepo(ctx, "repo", root)
	require.NoError(t, err)
	routes, err = store.FindRoutes(ctx, db, "repo", store.RouteFilter{})
	require.NoError(t, err)
	assert.Empty(t, routes)
}
