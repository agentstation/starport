package controllers

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/agentstation/starport/internal/catalog/logos"
	"github.com/agentstation/starport/internal/catalog/view"
	"github.com/agentstation/starport/internal/proxy"
)

// LogosController serves catalog identity marks. The bundled SVG set
// leads: it is curated color-first from one icon family, so marks render
// consistently, where catalog-carried bytes mix monochrome and color
// glyphs. Catalog bytes fill the gaps the bundle does not cover. The
// route is public: logos are static brand assets the console loads
// without credentials, like the health probes.
type LogosController struct {
	*BaseHandler
}

// NewLogosController creates the logos controller.
func NewLogosController(service proxy.Proxy) *LogosController {
	return &LogosController{BaseHandler: NewBaseHandler(service)}
}

// Get handles GET /api/v1/logos/{kind}/{id}.svg.
func (h *LogosController) Get(w http.ResponseWriter, r *http.Request) {
	kind, id := chi.URLParam(r, "kind"), chi.URLParam(r, "id")
	svg, etag, ok := logos.Bytes(logos.Kind(kind), id)
	if !ok {
		svg, etag, ok = h.catalogLogo(r, kind, id)
	}
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
	_, _ = w.Write(svg) // #nosec G705 -- bytes come from the catalog payload or the embedded, license-audited bundle keyed by a validated ID, never from the request.
}

// catalogLogo returns the catalog-carried mark for this kind and ID. ok
// is false when no service is wired or the catalog has no bytes; the
// caller has already missed the bundled set, so a miss here is a 404.
func (h *LogosController) catalogLogo(r *http.Request, kind, id string) (svg []byte, etag string, ok bool) {
	if h.service == nil {
		return nil, "", false
	}
	svg, err := h.service.GetLogo(r.Context(), view.LogoKind(kind), id)
	if err != nil || len(svg) == 0 {
		return nil, "", false
	}
	sum := sha256.Sum256(svg)
	return svg, hex.EncodeToString(sum[:16]), true
}
