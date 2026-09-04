package controllers

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog/log"

	"github.com/agentstation/starport/internal/catalog"
	"github.com/agentstation/starport/internal/protocol/openrouter"
	"github.com/agentstation/starport/internal/server/dto"
)

const catalogNotConfiguredMessage = "Catalog operations are not configured"

// catalogUnavailableMessage is the one sentence a reader receives when no
// catalog is available. It names no source, no address, and no failure, so a
// reader learns that the gateway cannot answer and nothing else.
const catalogUnavailableMessage = "The catalog is not available."

// catalogRetryAfterSeconds tells a reader when to ask again after a sanitized
// 503. A shell that polls the summary waits this long rather than retrying at
// its own cadence.
const catalogRetryAfterSeconds = 30

// fieldRunID names the refresh run a caller reads or ends.
const fieldRunID = "run_id"

// CatalogOperations serves the reader summary, the operator status, generation
// diffs, and the asynchronous catalog refresh.
type CatalogOperations interface {
	// CatalogSummary is the allowlisted view a reader receives.
	CatalogSummary(context.Context) (catalog.Summary, error)
	// CatalogChanges diffs the two newest accepted generations.
	CatalogChanges(context.Context) (catalog.Diff, error)
	// CatalogStatus is the operator view behind the admin scope.
	CatalogStatus(context.Context) (catalog.AdminStatus, error)
	// StartCatalogRefresh accepts one refresh. The second value reports that
	// the request joined the run in flight.
	StartCatalogRefresh(context.Context) (catalog.Operation, bool, error)
	// CatalogOperation reports one refresh run.
	CatalogOperation(context.Context, string) (catalog.Operation, error)
	// CancelCatalogOperation ends one open refresh run.
	CancelCatalogOperation(context.Context, string) (catalog.Operation, error)
}

// CatalogController serves the catalog read surface and the admin catalog
// operations.
type CatalogController struct {
	operations CatalogOperations
	audit      AuditRecorder
}

// NewCatalogController creates the catalog operations adapter.
func NewCatalogController(operations CatalogOperations) *CatalogController {
	return &CatalogController{operations: operations}
}

// Summary handles GET /api/v1/catalog. It serves the allowlisted reader view
// and nothing else. A gateway with no catalog answers a sanitized 503 that
// names no source and no failure.
func (h *CatalogController) Summary(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if h.operations == nil {
		writeCatalogUnavailable(w)
		return
	}
	summary, err := h.operations.CatalogSummary(r.Context())
	if err != nil {
		log.Warn().
			Str("reason", string(catalog.ClassifyOperationFailure(err))).
			Msg("the catalog summary is unavailable")
		writeCatalogUnavailable(w)
		return
	}
	if err := dto.WriteJSON(w, http.StatusOK, summary); err != nil {
		log.Error().Err(err).Msg("failed to write the catalog summary")
	}
}

// Changes handles GET /api/v1/catalog/changes.
func (h *CatalogController) Changes(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if h.operations == nil {
		writeCatalogNotConfigured(w)
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

// Status handles GET /api/v1/admin/catalog/status. It serves the operator view
// behind the admin scope, with candidate, accepted, rejected, and pending
// route-validation state as distinct values.
func (h *CatalogController) Status(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if h.operations == nil {
		writeCatalogNotConfigured(w)
		return
	}
	status, err := h.operations.CatalogStatus(r.Context())
	if err != nil {
		writeCatalogError(w, err, "The catalog status is unavailable.")
		return
	}
	if err := dto.WriteJSON(w, http.StatusOK, status); err != nil {
		log.Error().Err(err).Msg("failed to write the catalog status")
	}
}

// Refresh handles POST /api/v1/admin/catalog/refresh. It accepts the work and
// answers 202 with the operation that carries it. The run outlives the
// request, so the caller reads its end through the run route.
//
// Overlapping requests join one run: a second caller receives the identifier
// of the run in flight rather than starting a second one.
func (h *CatalogController) Refresh(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if h.operations == nil {
		writeCatalogNotConfigured(w)
		return
	}
	operation, joined, err := h.operations.StartCatalogRefresh(r.Context())
	if err != nil {
		writeAudit(r.Context(), h.audit, "catalog.refresh", string(catalog.KindCatalogUpdate), err)
		writeCatalogError(w, err, "The catalog refresh did not start.")
		return
	}
	writeAudit(r.Context(), h.audit, "catalog.refresh", operation.ID, nil)
	w.Header().Set("Location", "/api/v1/admin/catalog/refreshes/"+operation.ID)
	if err := dto.WriteJSON(w, http.StatusAccepted, acceptedOperation(operation, joined)); err != nil {
		log.Error().Err(err).Msg("failed to write the accepted catalog operation")
	}
}

// RefreshStatus handles GET /api/v1/admin/catalog/refreshes/{run_id}.
func (h *CatalogController) RefreshStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if h.operations == nil {
		writeCatalogNotConfigured(w)
		return
	}
	operation, err := h.operations.CatalogOperation(r.Context(), chi.URLParam(r, fieldRunID))
	if err != nil {
		writeCatalogOperationError(w, err)
		return
	}
	if err := dto.WriteJSON(w, http.StatusOK, operation); err != nil {
		log.Error().Err(err).Msg("failed to write the catalog operation")
	}
}

// CancelRefresh handles DELETE /api/v1/admin/catalog/refreshes/{run_id}. It
// ends one open run. A run that already closed answers with its own terminal
// state, so a repeated cancel changes nothing.
func (h *CatalogController) CancelRefresh(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if h.operations == nil {
		writeCatalogNotConfigured(w)
		return
	}
	runID := chi.URLParam(r, fieldRunID)
	operation, err := h.operations.CancelCatalogOperation(r.Context(), runID)
	if err != nil {
		writeAudit(r.Context(), h.audit, "catalog.refresh.cancel", runID, err)
		writeCatalogOperationError(w, err)
		return
	}
	writeAudit(r.Context(), h.audit, "catalog.refresh.cancel", operation.ID, nil)
	if err := dto.WriteJSON(w, http.StatusOK, operation); err != nil {
		log.Error().Err(err).Msg("failed to write the canceled catalog operation")
	}
}

// acceptedOperationResponse is the 202 body. It carries the operation and says
// whether the request joined a run that was already in flight.
type acceptedOperationResponse struct {
	catalog.Operation
	// Joined reports that this request joined the run in flight.
	Joined bool `json:"joined"`
}

func acceptedOperation(operation catalog.Operation, joined bool) acceptedOperationResponse {
	return acceptedOperationResponse{Operation: operation, Joined: joined}
}

// writeCatalogNotConfigured answers a deployment that composed no catalog
// surface at all. Only an operator route reaches it, so it names the missing
// composition.
func writeCatalogNotConfigured(w http.ResponseWriter) {
	openrouter.WriteError(
		w,
		http.StatusServiceUnavailable,
		catalogNotConfiguredMessage,
		map[string]any{openRouterErrorTypeField: errorTypeServer},
	)
}

// writeCatalogUnavailable answers the sanitized 503 of the reader surface.
func writeCatalogUnavailable(w http.ResponseWriter) {
	w.Header().Set("Retry-After", strconv.Itoa(catalogRetryAfterSeconds))
	openrouter.WriteError(
		w,
		http.StatusServiceUnavailable,
		catalogUnavailableMessage,
		map[string]any{openRouterErrorTypeField: errorTypeServer},
	)
}

// writeCatalogOperationError answers a run identifier the registry does not
// hold, and every other operation failure.
func writeCatalogOperationError(w http.ResponseWriter, err error) {
	if errors.Is(err, catalog.ErrOperationNotFound) {
		openrouter.WriteError(
			w,
			http.StatusNotFound,
			"The catalog refresh run is not found.",
			map[string]any{openRouterErrorTypeField: errorTypeNotFound},
		)
		return
	}
	writeCatalogError(w, err, "The catalog operation is unavailable.")
}

func writeCatalogError(w http.ResponseWriter, err error, message string) {
	status := http.StatusInternalServerError
	errorType := errorTypeServer
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		status = http.StatusGatewayTimeout
		errorType = errorTypeServiceUnavailable
		message = "Catalog operation timed out."
	case errors.Is(err, context.Canceled):
		status = http.StatusRequestTimeout
		errorType = errorTypeInvalidRequest
		message = "Catalog operation was canceled."
	}
	// The log carries the closed-set reason, never the failure text, because a
	// source failure can hold an address or a credential.
	log.Error().
		Str("reason", string(catalog.ClassifyOperationFailure(err))).
		Int("status", status).
		Msg("catalog operation failed")
	openrouter.WriteError(w, status, message, map[string]any{
		openRouterErrorTypeField: errorType,
	})
}
