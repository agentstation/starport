// Package chatui provides a web-based chat interface for interacting with LLM models through Starport.
package chatui

import (
	_ "embed"
	"html/template"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog"
)

//go:embed templates/index.html
var indexHTML string

//go:embed static/chat.js
var chatJS string

//go:embed static/chat.css
var chatCSS string

// Handler provides HTTP handlers for the ChatUI interface.
type Handler struct {
	logger   *zerolog.Logger
	template *template.Template
	config   Config
}

// Config holds ChatUI-specific configuration.
type Config struct {
	Title      string
	Theme      string
	APIBaseURL string
}

// NewHandler creates a new ChatUI handler.
func NewHandler(logger *zerolog.Logger, config Config) (*Handler, error) {
	tmpl, err := template.New("index").Parse(indexHTML)
	if err != nil {
		return nil, err
	}

	return &Handler{
		logger:   logger,
		template: tmpl,
		config:   config,
	}, nil
}

// Index serves the main ChatUI page.
func (h *Handler) Index(w http.ResponseWriter, _ *http.Request) {
	// Override CSP for ChatUI to allow inline scripts and CDN resources for markdown/syntax highlighting
	w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self' 'unsafe-inline' https://cdn.jsdelivr.net https://cdnjs.cloudflare.com; style-src 'self' 'unsafe-inline' https://cdn.jsdelivr.net https://cdnjs.cloudflare.com; img-src 'self' data:; font-src 'self' https://cdn.jsdelivr.net https://cdnjs.cloudflare.com; connect-src 'self'; frame-ancestors 'none';")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	data := struct {
		Title      string
		Theme      string
		APIBaseURL string
	}{
		Title:      h.config.Title,
		Theme:      h.config.Theme,
		APIBaseURL: h.config.APIBaseURL,
	}

	if err := h.template.Execute(w, data); err != nil {
		h.logger.Error().Err(err).Msg("failed to render template")
		http.Error(w, "Internal server error", http.StatusInternalServerError)
	}
}

// Static serves static assets.
func (h *Handler) Static(w http.ResponseWriter, r *http.Request) {
	path := chi.URLParam(r, "*")

	var content string
	var contentType string

	switch path {
	case "chat.js":
		content = chatJS
		contentType = "application/javascript"
	case "chat.css":
		content = chatCSS
		contentType = "text/css"
	default:
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "public, max-age=3600")

	if _, err := w.Write([]byte(content)); err != nil {
		h.logger.Error().Err(err).Msg("failed to write static content")
	}
}

// Routes returns a chi router with all ChatUI routes.
func (h *Handler) Routes() chi.Router {
	r := chi.NewRouter()

	r.Get("/", h.Index)
	r.Get("/static/*", h.Static)

	return r
}
