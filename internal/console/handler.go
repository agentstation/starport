// Package console provides the embedded web console for operating a Starport
// gateway: playground chat, the Starmap model catalog, provider status, key
// management, and gateway settings. Every asset is embedded in the binary so
// the console works offline with a strict same-origin content security policy.
package console

import (
	"crypto/rand"
	"embed"
	"encoding/base64"
	"fmt"
	"hash/fnv"
	"html/template"
	"io/fs"
	"net/http"
	"sort"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog"
)

//go:embed templates/index.html
var indexHTML string

//go:embed static
var staticFiles embed.FS

// contentSecurityPolicy keeps the console same-origin only. No CDN hosts:
// every script, style, and font ships inside the binary. The nonce admits
// one inline script in the shell that restores the saved theme before the
// first paint.
const contentSecurityPolicy = "default-src 'self'; script-src 'self' 'nonce-%s'; " +
	"style-src 'self'; img-src 'self' data:; font-src 'self'; " +
	"connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; " +
	"form-action 'self'"

// PagePaths lists every console page route. The console serves the same
// shell for each path and the client router renders the matching page.
var PagePaths = []string{"/", "/chat", "/models", "/providers", "/keys", "/usage", "/settings"}

// Config holds console configuration.
type Config struct {
	Title string
	Theme string
}

// Handler serves the console shell and its embedded static assets.
type Handler struct {
	logger       *zerolog.Logger
	template     *template.Template
	config       Config
	staticFS     fs.FS
	assetVersion string
}

// NewHandler creates a console handler and computes the asset version used
// for cache revalidation.
func NewHandler(logger *zerolog.Logger, config Config) (*Handler, error) {
	tmpl, err := template.New("index").Parse(indexHTML)
	if err != nil {
		return nil, err
	}
	staticFS, err := fs.Sub(staticFiles, "static")
	if err != nil {
		return nil, err
	}
	version, err := hashAssets(staticFS)
	if err != nil {
		return nil, err
	}
	return &Handler{
		logger:       logger,
		template:     tmpl,
		config:       config,
		staticFS:     staticFS,
		assetVersion: version,
	}, nil
}

// hashAssets fingerprints the embedded static tree so clients revalidate
// assets after a gateway upgrade instead of serving a stale console.
func hashAssets(files fs.FS) (string, error) {
	paths := make([]string, 0, 64)
	err := fs.WalkDir(files, ".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			paths = append(paths, path)
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Strings(paths)
	digest := fnv.New64a()
	for _, path := range paths {
		content, err := fs.ReadFile(files, path)
		if err != nil {
			return "", err
		}
		_, _ = digest.Write([]byte(path))
		_, _ = digest.Write(content)
	}
	return fmt.Sprintf("%016x", digest.Sum64()), nil
}

// AssetVersion returns the fingerprint of the embedded static assets.
func (h *Handler) AssetVersion() string { return h.assetVersion }

// Index serves the console shell.
func (h *Handler) Index(w http.ResponseWriter, _ *http.Request) {
	nonce, err := scriptNonce()
	if err != nil {
		h.logger.Error().Err(err).Msg("failed to generate console script nonce")
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Security-Policy", fmt.Sprintf(contentSecurityPolicy, nonce))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")

	data := struct {
		Title        string
		Theme        string
		AssetVersion string
		Nonce        string
	}{
		Title:        h.config.Title,
		Theme:        h.config.Theme,
		AssetVersion: h.assetVersion,
		Nonce:        nonce,
	}
	if err := h.template.Execute(w, data); err != nil {
		h.logger.Error().Err(err).Msg("failed to render console template")
		http.Error(w, "Internal server error", http.StatusInternalServerError)
	}
}

// scriptNonce returns a fresh nonce for the shell's inline theme script.
// URL-safe base64 keeps the nonce free of characters html/template escapes.
func scriptNonce() (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// Static serves embedded assets with ETag revalidation so a local gateway
// stays fast while an upgraded binary always delivers fresh assets.
func (h *Handler) Static(w http.ResponseWriter, r *http.Request) {
	path := chi.URLParam(r, "*")
	if path == "" || strings.Contains(path, "..") {
		http.NotFound(w, r)
		return
	}
	file, err := h.staticFS.Open(path)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer func() { _ = file.Close() }()

	etag := `"` + h.assetVersion + `"`
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "no-cache")
	if match := r.Header.Get("If-None-Match"); match == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}

	w.Header().Set("Content-Type", contentTypeFor(path))
	content, err := fs.ReadFile(h.staticFS, path)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	// #nosec G705 -- content is from the read-only embedded FS; traversal is rejected above
	if _, err := w.Write(content); err != nil {
		h.logger.Error().Err(err).Str("path", path).Msg("failed to write console asset")
	}
}

func contentTypeFor(path string) string {
	switch {
	case strings.HasSuffix(path, ".js"):
		return "application/javascript; charset=utf-8"
	case strings.HasSuffix(path, ".css"):
		return "text/css; charset=utf-8"
	case strings.HasSuffix(path, ".woff2"):
		return "font/woff2"
	case strings.HasSuffix(path, ".svg"):
		return "image/svg+xml"
	case strings.HasSuffix(path, ".png"):
		return "image/png"
	default:
		return "text/plain; charset=utf-8"
	}
}

// Register mounts the console page routes and static assets on the router.
func (h *Handler) Register(r chi.Router) {
	for _, path := range PagePaths {
		r.Get(path, h.Index)
	}
	r.Get("/static/*", h.Static)
}
