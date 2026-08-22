package controllers

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/agentstation/starport/internal/proxy"
	"github.com/agentstation/starport/internal/server/dto"
)

// AuthorsController handles catalog author endpoints
type AuthorsController struct {
	*BaseHandler
}

// NewAuthorsController creates a new authors controller
func NewAuthorsController(service proxy.Proxy) *AuthorsController {
	return &AuthorsController{
		BaseHandler: NewBaseHandler(service),
	}
}

// List handles GET /api/v1/authors
func (h *AuthorsController) List(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	resp, err := h.service.ListAuthors(ctx)
	if err != nil {
		h.logError(ctx, err, "failed to list authors")
		h.writeError(w, err)
		return
	}

	if err := dto.WriteJSON(w, http.StatusOK, resp); err != nil {
		h.logError(ctx, err, "failed to write response")
	}
}

// Get handles GET /api/v1/authors/{author}
func (h *AuthorsController) Get(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	authorID := chi.URLParam(r, "author")
	if authorID == "" {
		h.writeInvalidRequest(w, "Author ID is required")
		return
	}

	resp, err := h.service.GetAuthor(ctx, authorID)
	if err != nil {
		h.logError(ctx, err, "failed to get author")
		h.writeError(w, err)
		return
	}

	if err := dto.WriteJSON(w, http.StatusOK, resp); err != nil {
		h.logError(ctx, err, "failed to write response")
	}
}
