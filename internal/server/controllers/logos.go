package controllers

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/agentstation/starport/internal/catalog/logos"
	"github.com/agentstation/starport/internal/proxy"
)

// LogosController serves catalog identity marks from the bundled SVG set.
// The route is public: logos are static brand assets the console loads
// without credentials, like the health probes.
type LogosController struct {
	*BaseHandler
}

// NewLogosController creates the logos controller. It reads embedded
// bytes only, so it takes no service.
func NewLogosController() *LogosController {
	return &LogosController{BaseHandler: NewBaseHandler(nil)}
}

// Get handles GET /api/v1/logos/{kind}/{id}.svg.
func (h *LogosController) Get(w http.ResponseWriter, r *http.Request) {
	svg, etag, ok := logos.Bytes(logos.Kind(chi.URLParam(r, "kind")), chi.URLParam(r, "id"))
	if !ok {
		h.writeError(w, &proxy.ProviderError{Code: errorCodeNotFound, Message: "Logo not found"})
		return
	}
	validator := `"` + etag + `"`
	w.Header().Set("ETag", validator)
	w.Header().Set("Cache-Control", "public, max-age=86400")
	if r.Header.Get("If-None-Match") == validator {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Type", "image/svg+xml")
	_, _ = w.Write(svg) // #nosec G705 -- bytes come from the embedded, license-audited bundle keyed by a validated ID, never from the request.
}
