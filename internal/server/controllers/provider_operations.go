package controllers

import (
	"context"
	"errors"
	"net/http"

	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/rs/zerolog/log"

	"github.com/agentstation/starport/internal/protocol/openrouter"
	"github.com/agentstation/starport/internal/providers"
	"github.com/agentstation/starport/internal/providerstate"
	"github.com/agentstation/starport/internal/server/dto"
)

// ProviderOperations supplies safe provider state and forced reconciliation.
type ProviderOperations interface {
	ProviderStates() providerstate.Snapshot
	RefreshProviders(context.Context) (providers.ReconcileReport, error)
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
		PreviousProviderStateRevision: before.Revision,
		ProviderStateRevision:         after.Revision,
	}
	if err := dto.WriteJSON(w, http.StatusOK, response); err != nil {
		log.Error().Err(err).Msg("failed to write provider refresh result")
	}
}

type providerRefreshResponse struct {
	ReconciliationRevision        uint64   `json:"reconciliation_revision"`
	Changed                       bool     `json:"changed"`
	ConfiguredProviders           []string `json:"configured_providers"`
	FailureCount                  int      `json:"failure_count"`
	PreviousProviderStateRevision uint64   `json:"previous_provider_state_revision"`
	ProviderStateRevision         uint64   `json:"provider_state_revision"`
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
