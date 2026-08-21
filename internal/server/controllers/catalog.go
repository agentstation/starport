package controllers

import (
	"context"
	"errors"
	"net/http"

	"github.com/rs/zerolog/log"

	"github.com/agentstation/starport/internal/catalog"
	"github.com/agentstation/starport/internal/server/dto"
)

const catalogNotConfiguredMessage = "Catalog operations are not configured"

// CatalogOperations supplies snapshot freshness, generation diffs, and forced
// catalog acquisition.
type CatalogOperations interface {
	CatalogMetadata(context.Context) (catalog.SnapshotMetadata, error)
	CatalogChanges(context.Context) (catalog.Diff, error)
	RefreshCatalog(context.Context) (catalog.RefreshReport, error)
}

// CatalogController serves catalog freshness and refresh operations.
type CatalogController struct {
	operations CatalogOperations
}

// NewCatalogController creates the catalog operations adapter.
func NewCatalogController(operations CatalogOperations) *CatalogController {
	return &CatalogController{operations: operations}
}

// Metadata handles GET /api/v1/catalog.
func (h *CatalogController) Metadata(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if h.operations == nil {
		dto.WriteError(w, http.StatusServiceUnavailable, dto.ErrorTypeServerError, catalogNotConfiguredMessage)
		return
	}
	metadata, err := h.operations.CatalogMetadata(r.Context())
	if err != nil {
		writeCatalogError(w, err, "Catalog metadata is unavailable.")
		return
	}
	if err := dto.WriteJSON(w, http.StatusOK, metadata); err != nil {
		log.Error().Err(err).Msg("failed to write catalog metadata")
	}
}

// Changes handles GET /api/v1/catalog/changes.
func (h *CatalogController) Changes(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if h.operations == nil {
		dto.WriteError(w, http.StatusServiceUnavailable, dto.ErrorTypeServerError, catalogNotConfiguredMessage)
		return
	}
	diff, err := h.operations.CatalogChanges(r.Context())
	if err != nil {
		writeCatalogError(w, err, "Catalog changes are unavailable.")
		return
	}
	if err := dto.WriteJSON(w, http.StatusOK, diff); err != nil {
		log.Error().Err(err).Msg("failed to write catalog changes")
	}
}

// Refresh handles POST /api/v1/admin/catalog/refresh.
func (h *CatalogController) Refresh(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if h.operations == nil {
		dto.WriteError(w, http.StatusServiceUnavailable, dto.ErrorTypeServerError, catalogNotConfiguredMessage)
		return
	}
	report, err := h.operations.RefreshCatalog(r.Context())
	if err != nil {
		writeCatalogError(w, err, "Catalog refresh failed.")
		return
	}
	if err := dto.WriteJSON(w, http.StatusOK, report); err != nil {
		log.Error().Err(err).Msg("failed to write catalog refresh result")
	}
}

func writeCatalogError(w http.ResponseWriter, err error, message string) {
	status := http.StatusInternalServerError
	errorType := dto.ErrorTypeServerError
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		status = http.StatusGatewayTimeout
		message = "Catalog operation timed out."
	case errors.Is(err, context.Canceled):
		status = http.StatusRequestTimeout
		errorType = dto.ErrorTypeInvalidRequest
		message = "Catalog operation was canceled."
	}
	log.Error().Err(err).Int("status", status).Msg("catalog operation failed")
	dto.WriteError(w, status, errorType, message)
}
