// Package clients makes HTTP client calls and lookups that look like route
// registrations. None of them is a route.
package clients

import (
	"context"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

type store struct{}

func (s *store) Get(key string, fallback func()) string { return key }

func handler(w http.ResponseWriter, r *http.Request) {}

func calls(ctx context.Context, url string, c *gin.Context, client *http.Client) {
	http.Get(url)
	http.Post(url, "application/json", strings.NewReader("{}"))
	http.Head("/health")
	client.Get("/users")
	client.Post("/users", "application/json", nil)
	http.DefaultClient.Get("/users")
	req, _ := http.NewRequestWithContext(ctx, "GET", "/users", nil)
	client.Do(req)

	// A Gin context and a store are not routers.
	c.GET("/x", handler)
	c.Handle("/x", handler)
	s := &store{}
	s.Get("/users", func() {})

	// A ServeMux from another package of the same name is not net/http's.
	mux := newMux()
	mux.HandleFunc("/x", handler)

	// A client field.
	api := struct{ http *http.Client }{http: client}
	api.http.Get("/users")

	// gin.New is a constructor, not a registration.
	gin.New()
}

type fakeMux struct{}

func (m *fakeMux) HandleFunc(p string, h func(http.ResponseWriter, *http.Request)) {}

func newMux() *fakeMux { return &fakeMux{} }
