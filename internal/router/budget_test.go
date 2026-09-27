package router

import (
	"math"
	"testing"

	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/agentstation/starport/internal/limits/admission"
	"github.com/agentstation/starport/internal/providers/connectors"
	"github.com/stretchr/testify/require"
)

func TestDeclaredChatTokenBounds(t *testing.T) {
	unknown := &catalogs.ModelLimits{InputTokens: 100, OutputTokens: 50}
	unknown.SetUnknown(catalogs.ModelLimitOutputTokens)
	for _, test := range []struct {
		name        string
		limits      *catalogs.ModelLimits
		count, want int64
	}{
		{"separate-limits", &catalogs.ModelLimits{InputTokens: 100, OutputTokens: 50}, 1, 150},
		{"context-fallback", &catalogs.ModelLimits{ContextWindow: 100, OutputTokens: 50}, 1, 150},
		{"multiple-candidates", &catalogs.ModelLimits{InputTokens: 100, OutputTokens: 50}, 3, 450},
		{"missing", nil, 1, 0},
		{"missing-input", &catalogs.ModelLimits{OutputTokens: 50}, 1, 0},
		{"unknown-output", unknown, 1, 0},
		{"zero-candidates", &catalogs.ModelLimits{InputTokens: 100, OutputTokens: 50}, 0, 0},
		{"sum-overflow", &catalogs.ModelLimits{InputTokens: math.MaxInt64, OutputTokens: 1}, 1, 0},
		{"product-overflow", &catalogs.ModelLimits{InputTokens: math.MaxInt64 / 2, OutputTokens: 1}, 2, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			bound, err := declaredChatTokenBound(test.limits, test.count)
			if test.want == 0 {
				require.ErrorIs(t, err, admission.ErrBoundUnknown)
			} else {
				require.NoError(t, err)
			}
			require.Equal(t, test.want, bound)
		})
	}
}

func TestTextChatBillingScopeRejectsExtraChargeSurfaces(t *testing.T) {
	for _, test := range []struct {
		name    string
		request connectors.ChatRequest
		valid   bool
	}{
		{"text", connectors.ChatRequest{Messages: []connectors.Message{{Role: "user", Content: "hello"}}}, true},
		{"text parts", connectors.ChatRequest{Messages: []connectors.Message{{Content: []connectors.ContentPart{{Type: "text", Text: "hello"}}}}}, true},
		{"image", connectors.ChatRequest{Messages: []connectors.Message{{Content: []connectors.ContentPart{{Type: "image_url"}}}}}, false},
		{"provider extension", connectors.ChatRequest{ProviderOptions: map[string]any{"service_tier": "priority"}}, false},
		{"audio", connectors.ChatRequest{Modalities: []string{"audio"}}, false},
		{"hosted tool", connectors.ChatRequest{Tools: []connectors.Tool{{Type: "web_search"}}}, false},
		{"unknown content", connectors.ChatRequest{Messages: []connectors.Message{{Content: map[string]any{"type": "image_url"}}}}, false},
	} {
		t.Run(test.name, func(t *testing.T) { require.Equal(t, test.valid, textChatBillingScope(&test.request)) })
	}
}
