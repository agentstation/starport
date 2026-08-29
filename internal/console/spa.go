// Package console provides the embedded web console for operating a
// Starport gateway: playground chat, the Starmap model catalog, provider
// status, key management, and gateway settings. The built single-page
// console is embedded in the binary so it works offline with a strict
// same-origin content security policy.
package console

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog"
)

// PageServer registers console page and asset routes on a router.
type PageServer interface {
	Register(r chi.Router)
}

// distFiles embeds the built single-page console from console/. The
// committed dist/.gitkeep keeps the directive compiling in a checkout
// that has not run the frontend build; NewSPAHandler detects the missing
// index.html and serves a plain notice instead.
//
//go:embed all:dist
var distFiles embed.FS

// spaContentSecurityPolicy keeps the SPA same-origin only. The Vite
// build emits no inline scripts, so script-src needs no nonce.
const spaContentSecurityPolicy = "default-src 'self'; script-src 'self'; " +
	"style-src 'self' 'unsafe-inline'; img-src 'self' data:; font-src 'self'; " +
	"connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; " +
	"form-action 'self'"

const notBuiltNotice = "The Starport console was not built into this " +
	"binary. Run `pnpm -C console build` before `go build`, or use a " +
	"release binary.\n"

// SPAHandler serves the built single-page console: hashed immutable
// assets under /assets/ and the SPA index for every page path, so the
// client router owns navigation (spaFallback).
type SPAHandler struct {
	logger *zerolog.Logger
	dist   fs.FS
	built  bool
}

// NewSPAHandler creates a handler over the embedded console build.
func NewSPAHandler(logger *zerolog.Logger) (*SPAHandler, error) {
	dist, err := fs.Sub(distFiles, "dist")
	if err != nil {
		return nil, err
	}
	return newSPAHandler(logger, dist), nil
}

// newSPAHandler wraps any dist tree so tests can exercise both the
// built and the not-built states.
func newSPAHandler(logger *zerolog.Logger, dist fs.FS) *SPAHandler {
	_, statErr := fs.Stat(dist, "index.html")
	return &SPAHandler{
		logger: logger,
		dist:   dist,
		built:  statErr == nil,
	}
}

// Built reports whether the embedded build contains the console.
func (h *SPAHandler) Built() bool { return h.built }

// Index serves the SPA shell for a page path. index.html stays
// no-cache so a new binary always delivers the matching hashed assets.
func (h *SPAHandler) Index(w http.ResponseWriter, _ *http.Request) {
	if !h.built {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(notBuiltNotice))
		return
	}
	index, err := fs.ReadFile(h.dist, "index.html")
	if err != nil {
		h.logger.Error().Err(err).Msg("failed to read embedded console index")
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Security-Policy", spaContentSecurityPolicy)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(index)
}

// Assets serves the hashed build outputs. The content hash in every
// asset filename makes the response immutable.
func (h *SPAHandler) Assets(w http.ResponseWriter, r *http.Request) {
	path := chi.URLParam(r, "*")
	if path == "" || strings.Contains(path, "..") {
		http.NotFound(w, r)
		return
	}
	file, err := h.dist.Open("assets/" + path)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	_ = file.Close()
	// The dist tree keeps its assets/ directory, so the request path maps
	// onto the FS unchanged.
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	http.FileServerFS(h.dist).ServeHTTP(w, r)
}

// spaPagePaths lists every console page route, including the nested
// detail routes the client router owns. The gateway serves the same
// shell for each path and the client router renders the matching page.
// A page path missing here breaks only direct loads and reloads: the
// client router still reaches the page, so the gap hides until someone
// refreshes. TestSPAPagePathsCoverClientRoutes derives this list from
// console/src/routes and fails when the two drift.
var spaPagePaths = []string{
	"/", "/auth", "/authors", "/authors/*", "/chat", "/docs",
	"/documents", "/files", "/jobs", "/keys", "/models", "/models/*",
	"/presets", "/providers", "/providers/*", "/settings", "/tenants",
	"/usage",
}

// Register mounts the SPA page routes and hashed assets on the router.
// Every console page path serves the same shell (spaFallback); the
// client router renders the matching page.
func (h *SPAHandler) Register(r chi.Router) {
	for _, path := range spaPagePaths {
		r.Get(path, h.Index)
	}
	r.Get("/assets/*", h.Assets)
}
