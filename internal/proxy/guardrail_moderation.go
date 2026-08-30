package proxy

import (
	"context"
	"errors"
	"fmt"

	"github.com/agentstation/starport/internal/guardrails"
	"github.com/agentstation/starport/internal/inference"
)

// GatewayModerator adapts the gateway's own moderation surface to the
// guardrail Moderator seam. The classification rides the account's own
// routing: it runs under the calling request's identity, so credential
// selection, usage capture, and limits treat it as the account's own
// moderation request. The gateway is late-bound because composition
// finishes after the guardrail pipeline is built.
type GatewayModerator struct {
	model   string
	gateway func() Proxy
}

// NewGatewayModerator builds the adapter around a catalog moderation
// model and a late-bound gateway.
func NewGatewayModerator(model string, gateway func() Proxy) *GatewayModerator {
	return &GatewayModerator{model: model, gateway: gateway}
}

// errNoGuardrailIdentity reports a moderation call outside a guarded
// request. The pipeline turns it into a refusal.
var errNoGuardrailIdentity = errors.New("no gateway identity for the guardrail moderation call")

// Moderate implements guardrails.Moderator.
func (m *GatewayModerator) Moderate(ctx context.Context, text string) ([]guardrails.CategoryScore, error) {
	identity, ok := guardrailIdentityFrom(ctx)
	if !ok {
		return nil, errNoGuardrailIdentity
	}
	canonical, err := inference.NewModerationRequest(m.model, []string{text})
	if err != nil {
		return nil, err
	}
	response, err := m.gateway().ProcessModerations(ctx, &ModerationRequest{
		Request:   canonical,
		AccountID: identity.AccountID,
		KeyID:     identity.KeyID,
		// The classification draws its own usage record beside the turn
		// that asked for it, so the suffix keeps the two apart while the
		// shared stem joins them.
		RequestID: identity.RequestID + "-guardrail",
		Protocol:  identity.Protocol,
	})
	if err != nil {
		return nil, err
	}
	if len(response.Response.Results) != 1 {
		return nil, fmt.Errorf("moderation answered %d results for one input", len(response.Response.Results))
	}
	categories := response.Response.Results[0].Categories
	scores := make([]guardrails.CategoryScore, 0, len(categories))
	for _, category := range categories {
		scores = append(scores, guardrails.CategoryScore{
			Category: category.Name,
			Score:    category.Score,
		})
	}
	return scores, nil
}
