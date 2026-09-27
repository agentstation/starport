package openai

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"testing"

	"github.com/agentstation/starport/internal/inference"
	"github.com/stretchr/testify/require"
)

func TestEmbeddingUsageProvenance(t *testing.T) {
	for _, test := range []struct {
		name    string
		usage   inference.Usage
		present bool
	}{
		{"unknown", inference.Usage{TokensUnknown: true, InputTokens: 8, TotalTokens: 8}, false},
		{"estimated", inference.Usage{Estimated: true, InputTokens: 8, TotalTokens: 8}, false},
		{"measured zero", inference.Usage{}, true},
		{"measured positive", inference.Usage{InputTokens: 8, TotalTokens: 8}, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			encoded, err := json.Marshal(EncodeEmbedding(inference.EmbeddingResponse{Model: "provider/model", Usage: test.usage}))
			require.NoError(t, err)
			var wire map[string]jsontext.Value
			require.NoError(t, json.Unmarshal(encoded, &wire))
			raw, present := wire["usage"]
			require.Equal(t, test.present, present)
			if present {
				var counts struct {
					PromptTokens int `json:"prompt_tokens"`
					TotalTokens  int `json:"total_tokens"`
				}
				require.NoError(t, json.Unmarshal(raw, &counts))
				require.Equal(t, test.usage.InputTokens, counts.PromptTokens)
				require.Equal(t, test.usage.TotalTokens, counts.TotalTokens)
			}
		})
	}
}
