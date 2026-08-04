package catalog

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestProviderScopedModelIdentity(t *testing.T) {
	tests := []struct {
		modelID  string
		provider string
		model    string
		ok       bool
	}{
		{modelID: "openai/gpt-5", provider: "openai", model: "gpt-5", ok: true},
		{modelID: "google/gemini-2.5-pro", provider: "google", model: "gemini-2.5-pro", ok: true},
		{modelID: "google/claude-3-opus@20240229", provider: "google", model: "claude-3-opus@20240229", ok: true},
		{modelID: "gpt-5", model: "gpt-5"},
	}
	for _, test := range tests {
		t.Run(test.modelID, func(t *testing.T) {
			provider, model, ok := SplitModelID(test.modelID)
			require.Equal(t, test.ok, ok)
			require.Equal(t, test.model, model)
			if test.ok {
				require.Equal(t, test.provider, ProviderFromModelID(test.modelID))
				require.NotEmpty(t, provider)
			}
		})
	}
}
