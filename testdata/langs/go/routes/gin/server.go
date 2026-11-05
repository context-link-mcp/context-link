// Package gin registers Gin routes: verbs, Any/Handle/Match, chained groups,
// an inline group, a router parameter and a router struct field.
package gin

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"example.com/shop/handlers"
)

type server struct {
	engine *gin.Engine
}

func health(c *gin.Context) {}

func listBooks(c *gin.Context) {}

func getBook(c *gin.Context) {}

func auth() gin.HandlerFunc { return nil }

// List has the name of a handler in package handlers; handlers.List below
// must not link to it.
func List(c *gin.Context) {}

func main() {
	r := gin.Default()
	r.GET("/health", health)
	r.Any("/ping", func(c *gin.Context) {})
	r.Handle(http.MethodPatch, "/books/:id", getBook)
	r.Match([]string{"PUT", http.MethodDelete}, "/books/:id", getBook)
	r.GET("/static/*filepath", gin.WrapH(http.FileServer(http.Dir("."))))

	api := r.Group("/api")
	v1 := api.Group("/v1", auth())
	{
		v1.GET("/books", listBooks)
		v1.POST("/books", auth(), listBooks)
		v1.GET("/books/:id", getBook)
		v1.GET("/users", handlers.List)
	}
	r.Group("/admin").DELETE("/cache", health)

	registerExtra(api)
	s := &server{engine: r}
	s.routes()

	// Not routes: a string handler, a dynamic path, a cache lookup.
	r.GET("/wrong", "not a handler")
	r.GET(dynamicPath(), health)
	c := newCache()
	c.Get("/health")
}

// registerExtra gets a group from main: its prefix is not known here.
func registerExtra(g *gin.RouterGroup) {
	g.GET("/extra", health)
}

func (s *server) routes() {
	s.engine.POST("/login", health)
}

func dynamicPath() string { return "/x" }

type cache struct{}

func newCache() *cache { return &cache{} }

func (c *cache) Get(key string) string { return key }
