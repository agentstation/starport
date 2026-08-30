package proxy

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/agentstation/starport/internal/guardrails"
	"github.com/agentstation/starport/internal/inference"
)

// moderationCoreStub is the wrapped core plus a scripted moderation
// provider: it records the moderation request the guardrail routed and
// answers fixed category scores.
type moderationCoreStub struct {
	Proxy
	chatCalls          int
	moderationRequests []*ModerationRequest
	scores             []inference.ModerationCategory
	response           *ChatCompletionResponse
}

func (s *moderationCoreStub) ProcessChatCompletion(_ context.Context, _ *ChatCompletionRequest) (*ChatCompletionResponse, error) {
	s.chatCalls++
	return s.response, nil
}

func (s *moderationCoreStub) ProcessModerations(_ context.Context, req *ModerationRequest) (*ModerationResponse, error) {
	s.moderationRequests = append(s.moderationRequests, req)
	return &ModerationResponse{
		Response: inference.ModerationResponse{
			Model:   req.Request.Model,
			Results: []inference.ModerationResult{{Categories: s.scores}},
		},
	}, nil
}

// moderatedService wires the moderation check over the guardrail-wrapped
// service itself, late-bound exactly as composition does it.
func moderatedService(t *testing.T, core *moderationCoreStub, settings guardrails.Settings) Proxy {
	t.Helper()
	var service Proxy
	settings.Moderator = NewGatewayModerator("openai/omni-moderation-latest", func() Proxy { return service })
	pipeline, err := guardrails.BuildPipeline([]string{"moderation"}, settings)
	require.NoError(t, err)
	service = NewGuardrails(guardrails.StaticPolicy{Pipeline: pipeline}).Wrap(core)
	return service
}

func moderatedChatRequest(text string) *ChatCompletionRequest {
	request := guardrailChatRequest(text)
	request.KeyID = "key-1"
	request.RequestID = "req-1"
	request.Protocol = "openai"
	return request
}

// TestModerationGuardrailRidesTheAccountIdentity holds the routing
// contract: the moderation call carries the calling request's account,
// key, and protocol, and a request scored above threshold never reaches
// the core planner.
func TestModerationGuardrailRidesTheAccountIdentity(t *testing.T) {
	core := &moderationCoreStub{
		scores:   []inference.ModerationCategory{{Name: "violence", Score: 0.9}},
		response: guardrailChatResponse("fine"),
	}
	service := moderatedService(t, core, guardrails.Settings{})

	_, err := service.ProcessChatCompletion(context.Background(), moderatedChatRequest("threats"))
	require.ErrorIs(t, err, guardrails.ErrRefused)
	var refusal *guardrails.RefusalError
	require.ErrorAs(t, err, &refusal)
	require.Equal(t, "moderation", refusal.Check)
	require.Equal(t, 0, core.chatCalls, "a refused request must never reach planning")

	require.Len(t, core.moderationRequests, 1)
	routed := core.moderationRequests[0]
	require.Equal(t, "acct", routed.AccountID)
	require.Equal(t, "key-1", routed.KeyID)
	require.Equal(t, "req-1-guardrail", routed.RequestID)
	require.Equal(t, "openai", routed.Protocol)
	require.Equal(t, "openai/omni-moderation-latest", routed.Request.Model)
	require.Equal(t, []string{"threats"}, routed.Request.Inputs)
}

// TestModerationGuardrailAllowsBelowThreshold holds the pass side
// against the stub provider: low scores let the turn run, and both the
// request and the answer draw one classification each.
func TestModerationGuardrailAllowsBelowThreshold(t *testing.T) {
	core := &moderationCoreStub{
		scores:   []inference.ModerationCategory{{Name: "violence", Score: 0.1}},
		response: guardrailChatResponse("calm answer"),
	}
	service := moderatedService(t, core, guardrails.Settings{})

	response, err := service.ProcessChatCompletion(context.Background(), moderatedChatRequest("hello"))
	require.NoError(t, err)
	require.Equal(t, 1, core.chatCalls)
	require.Len(t, core.moderationRequests, 2, "the request and the answer each classify once")
	require.Equal(t, string(guardrails.VerdictAllow), response.GuardrailVerdict)
}

// TestGatewayModeratorNeedsTheGatewayIdentity holds the fail-closed edge:
// a moderation call outside a guarded request errors instead of running
// unattributed, and the pipeline turns that into a refusal.
func TestGatewayModeratorNeedsTheGatewayIdentity(t *testing.T) {
	moderator := NewGatewayModerator("openai/omni-moderation-latest", func() Proxy { return nil })
	_, err := moderator.Moderate(context.Background(), "text")
	require.ErrorIs(t, err, errNoGuardrailIdentity)
}
