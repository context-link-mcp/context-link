// Package chi registers chi routes: verbs, Handle/HandleFunc/Method, nested
// r.Route callbacks, r.Group and r.With, and handlers in another package.
package chi

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"example.com/shop/handlers"
)

func listArticles(w http.ResponseWriter, r *http.Request) {}

func getArticle(w http.ResponseWriter, r *http.Request) {}

func logger(next http.Handler) http.Handler { return next }

func NewRouter() http.Handler {
	r := chi.NewRouter()
	r.Get("/", listArticles)
	r.HandleFunc("/metrics", listArticles)
	r.Method("OPTIONS", "/articles", http.HandlerFunc(listArticles))
	r.Handle("POST /webhook", http.HandlerFunc(getArticle))

	r.Route("/api", func(r chi.Router) {
		r.Get("/articles", listArticles)
		r.Route("/articles/{articleID:[0-9]+}", func(r chi.Router) {
			r.Get("/", getArticle)
			r.With(logger).Put("/", getArticle)
		})
		r.Group(func(r chi.Router) {
			r.Post("/users", handlers.Create)
		})
	})

	uh := handlers.NewUserHandler()
	r.Get("/users/{id}", uh.Show)
	r.Delete("/users/{id}", uh.Archive)
	return r
}
