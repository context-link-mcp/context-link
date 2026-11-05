// Package handlers holds handlers registered from other packages' routers.
package handlers

import "net/http"

// UserHandler serves the user routes.
type UserHandler struct{}

// NewUserHandler returns a UserHandler.
func NewUserHandler() *UserHandler { return &UserHandler{} }

// List lists users.
func List(w http.ResponseWriter, r *http.Request) {}

// Create creates a user.
func Create(w http.ResponseWriter, r *http.Request) {}

// Show shows one user.
func (h *UserHandler) Show(w http.ResponseWriter, r *http.Request) {}

// Archive archives a user.
func (h *UserHandler) Archive(w http.ResponseWriter, r *http.Request) {}
