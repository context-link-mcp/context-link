// Package nethttp registers net/http routes: the default mux, a ServeMux,
// Go 1.22 method and wildcard patterns, a host pattern, and a mux field.
package nethttp

import (
	"net/http"

	h "example.com/shop/handlers"
)

type app struct {
	mux *http.ServeMux
}

func index(w http.ResponseWriter, r *http.Request) {}

func getItem(w http.ResponseWriter, r *http.Request) {}

func main() {
	http.HandleFunc("/", index)
	http.Handle("/assets/", http.StripPrefix("/assets/", http.FileServer(http.Dir("static"))))

	mux := http.NewServeMux()
	mux.HandleFunc("GET /items/{id}", getItem)
	mux.HandleFunc("POST /items", func(w http.ResponseWriter, r *http.Request) {})
	mux.HandleFunc("/files/{path...}", index)
	mux.HandleFunc("GET /{$}", index)
	mux.Handle("example.com/about", http.HandlerFunc(h.List))
	http.DefaultServeMux.HandleFunc("/debug", index)

	a := &app{mux: mux}
	a.mux.HandleFunc("DELETE /items/{id}", getItem)

	http.ListenAndServe(":8080", mux)
}
