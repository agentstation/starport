package controllers

import (
	"context"
	"errors"
	"net/http"

	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog/log"

	"github.com/agentstation/starport/internal/protocol/openrouter"
	"github.com/agentstation/starport/internal/providers"
	providerstate "github.com/agentstation/starport/internal/providers/state"
	"github.com/agentstation/starport/internal/providers/statuspage"
	"github.com/agentstation/starport/internal/server/dto"
)

// ProviderOperations supplies safe provider state and forced reconciliation.
type ProviderOperations interface {
	ProviderStates() providerstate.Snapshot
	RefreshProviders(context.Context) (providers.ReconcileReport, error)
	// ProviderIncidentLog answers one provider's published incident log;
	// the bool reports whether the catalog knows the provider at all.
	ProviderIncidentLog(context.Context, catalogs.ProviderID) (statuspage.History, bool)
	// ProviderIncidentTransitions answers the durable record of indicator
	// changes this gateway observed for one provider, newest first.
	ProviderIncidentTransitions(context.Context, catalogs.ProviderID) ([]providerstate.IncidentTransition, error)
}

// ProviderOperationsController handles authenticated provider operations.
type ProviderOperationsController struct {
	operations ProviderOperations
}

// NewProviderOperationsController creates the provider operations adapter.
func NewProviderOperationsController(operations ProviderOperations) *ProviderOperationsController {
	return &ProviderOperationsController{operations: operations}
}

// Status handles GET /api/v1/admin/providers.
func (h *ProviderOperationsController) Status(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if err := dto.WriteJSON(w, http.StatusOK, h.operations.ProviderStates()); err != nil {
		log.Error().Err(err).Msg("failed to write provider status")
	}
}

// Incidents handles GET /api/v1/admin/providers/{provider}/incidents. The
// response keeps the two provenances apart: `log` is what the provider's
// own status page publishes about itself, and `observed` is what this
// gateway saw the live indicator do, on this deployment's clock.
func (h *ProviderOperationsController) Incidents(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	providerID := catalogs.ProviderID(chi.URLParam(r, "provider"))
	history, known := h.operations.ProviderIncidentLog(r.Context(), providerID)
	if !known {
		dto.WriteError(w, http.StatusNotFound, dto.ErrorTypeNotFound, "Provider not found")
		return
	}
	observed, err := h.operations.ProviderIncidentTransitions(r.Context(), providerID)
	if err != nil {
		log.Error().Err(err).Str("provider", string(providerID)).Msg("failed to read incident transitions")
		dto.WriteError(w, http.StatusInternalServerError, dto.ErrorTypeServerError, "Incident record read failed")
		return
	}
	response := providerIncidentsResponse{
		ProviderID: string(providerID),
		Log:        history,
		Observed:   observed,
	}
	if err := dto.WriteJSON(w, http.StatusOK, response); err != nil {
		log.Error().Err(err).Msg("failed to write provider incidents")
	}
}

// providerIncidentsResponse is the incident surface for one provider.
type providerIncidentsResponse struct {
	ProviderID string                             `json:"provider_id"`
	Log        statuspage.History                 `json:"log"`
	Observed   []providerstate.IncidentTransition `json:"observed,omitempty"`
}

// Refresh handles POST /api/v1/admin/providers/refresh.
func (h *ProviderOperationsController) Refresh(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	before := h.operations.ProviderStates()
	report, err := h.operations.RefreshProviders(r.Context())
	if err != nil {
		writeProviderRefreshError(w, err)
		return
	}
	after := h.operations.ProviderStates()
	response := providerRefreshResponse{
		ReconciliationRevision:        report.Revision,
		Changed:                       report.Changed,
		ConfiguredProviders:           providerIDs(report.ConfiguredProviders),
		FailureCount:                  len(report.Failures),
		Failures:                      refreshFailures(report.Failures),
		PreviousProviderStateRevision: before.Revision,
		ProviderStateRevision:         after.Revision,
	}
	if err := dto.WriteJSON(w, http.StatusOK, response); err != nil {
		log.Error().Err(err).Msg("failed to write provider refresh result")
	}
}

type providerRefreshResponse struct {
	ReconciliationRevision        uint64                   `json:"reconciliation_revision"`
	Changed                       bool                     `json:"changed"`
	ConfiguredProviders           []string                 `json:"configured_providers"`
	FailureCount                  int                      `json:"failure_count"`
	Failures                      []providerRefreshFailure `json:"failures,omitempty"`
	PreviousProviderStateRevision uint64                   `json:"previous_provider_state_revision"`
	ProviderStateRevision         uint64                   `json:"provider_state_revision"`
}

// providerRefreshFailure names one provider whose credential source failed
// during refresh. Reason is the classified secret-free description — never
// the raw source error, which may reference internal resources.
type providerRefreshFailure struct {
	ProviderID string `json:"provider_id"`
	Reason     string `json:"reason"`
}

func refreshFailures(failures []providers.ReconcileFailure) []providerRefreshFailure {
	if len(failures) == 0 {
		return nil
	}
	result := make([]providerRefreshFailure, len(failures))
	for index, failure := range failures {
		result[index] = providerRefreshFailure{
			ProviderID: string(failure.ProviderID),
			Reason:     providers.CredentialFailureDetail(failure.Err),
		}
	}
	return result
}

func providerIDs(values []catalogs.ProviderID) []string {
	result := make([]string, len(values))
	for index, value := range values {
		result[index] = string(value)
	}
	return result
}

func writeProviderRefreshError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	errorType := errorTypeServer
	message := "Provider reconciliation failed."
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		status = http.StatusGatewayTimeout
		errorType = errorTypeServiceUnavailable
		message = "Provider reconciliation timed out."
	case errors.Is(err, context.Canceled):
		status = http.StatusRequestTimeout
		errorType = errorTypeInvalidRequest
		message = "Provider reconciliation was canceled."
	}
	log.Error().Int("status", status).Msg("provider reconciliation failed")
	openrouter.WriteError(w, status, message, map[string]any{
		openRouterErrorTypeField: errorType,
	})
}
