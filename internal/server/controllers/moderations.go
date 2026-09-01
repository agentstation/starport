package controllers

import (
	"net/http"

	"github.com/agentstation/starport/internal/protocol/openai"
	"github.com/agentstation/starport/internal/proxy"
)

// ModerationsController serves POST /v1/moderations. OpenAI publishes the
// one moderation route a 2026 SDK expects, and OpenRouter publishes none,
// so this controller owns a single protocol.
type ModerationsController struct {
	*BaseHandler
}

// NewModerationsController creates an OpenAI-protocol moderations
// controller.
func NewModerationsController(service proxy.Proxy) *ModerationsController {
	return &ModerationsController{BaseHandler: NewProtocolBaseHandler(service, ProtocolOpenAI)}
}

// Create handles POST /v1/moderations.
func (h *ModerationsController) Create(w http.ResponseWriter, r *http.Request) {
	request, err := openai.DecodeModerations(r.Body)
	if err != nil {
		h.writeBodyRefusal(w, err)
		return
	}

	ctx := r.Context()
	req := &proxy.ModerationRequest{Request: request}
	req.APIKey = h.getAPIKey(ctx)
	req.AccountID = h.getAccountID(ctx)
	req.KeyID = h.getAPIKeyID(ctx)
	req.TeamID = h.getTeamID(ctx)
	req.APIKeyConfig, err = h.getAPIKeyRoutingConfig(ctx)
	if err != nil {
		h.writeCredentialStrategyError(w, err)
		return
	}
	req.RequestID = h.getRequestID(ctx)
	req.Protocol = string(h.protocol)

	resp, err := h.service.ProcessModerations(ctx, req)
	if err != nil {
		h.logError(ctx, err, "moderation failed")
		h.writeError(w, err)
		return
	}
	// The encoder can refuse the answer: a result list that answers a
	// different number of inputs, or a score outside the unit interval,
	// produces output that reads as correct. That refusal is a gateway
	// failure rather than a body fault, so it reaches the caller through the
	// same error path a failed route reaches it through.
	encoded, err := openai.EncodeModerations(resp.Response, request)
	if err != nil {
		h.logError(ctx, err, "failed to write response")
		h.writeError(w, err)
		return
	}
	if err := openai.WriteJSON(w, http.StatusOK, encoded); err != nil {
		h.logError(ctx, err, "failed to write response")
	}
}
