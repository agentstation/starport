package connectors

import (
	"encoding/json/v2"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestChatReportedUsageDoesNotRequireContentConversion(t *testing.T) {
	var missing *ChatResponse
	require.Nil(t, missing.ReportedUsage())
	require.Nil(t, (&ChatResponse{}).ReportedUsage())
	require.NotNil(t, (&ChatResponse{usageReported: true}).ReportedUsage())
	response := &ChatResponse{Usage: Usage{PromptTokens: 8, CompletionTokens: 3, TotalTokens: 11},
		Choices: []Choice{{Message: Message{Role: RoleAssistant, Content: make(chan int)}}}}
	_, err := ChatResponseToInference(response, "provider/model")
	require.Error(t, err)
	usage := response.ReportedUsage()
	require.NotNil(t, usage)
	require.Equal(t, 11, usage.TotalTokens)
	usage.TotalTokens = 1
	require.Equal(t, 11, response.Usage.TotalTokens)
	response.Usage.PromptTokensDetails = &PromptTokensDetails{CachedTokens: 4}
	response.Usage.CompletionTokensDetails = &CompletionTokensDetails{ReasoningTokens: 2}
	retained := response.ReportedUsage()
	response.Usage.PromptTokensDetails.CachedTokens = 99
	response.Usage.CompletionTokensDetails.ReasoningTokens = 99
	require.Equal(t, 4, retained.PromptTokensDetails.CachedTokens)
	require.Equal(t, 2, retained.CompletionTokensDetails.ReasoningTokens)
}

func TestReportedUsageBreakdownPresence(t *testing.T) {
	for _, test := range []struct {
		body  string
		known bool
	}{
		{`{}`, false}, {`{"prompt_tokens_details":{},"completion_tokens_details":{}}`, false},
		{`{"prompt_tokens_details":{"cached_tokens":null},"completion_tokens_details":{"reasoning_tokens":null}}`, false},
		{`{"prompt_tokens_details":{"cached_tokens":0},"completion_tokens_details":{"reasoning_tokens":0}}`, true},
	} {
		t.Run(test.body, func(t *testing.T) {
			var u Usage
			require.NoError(t, json.Unmarshal([]byte(test.body), &u))
			_, cached := u.PromptTokensDetails.ReportedCachedTokens()
			_, reasoning := u.CompletionTokensDetails.ReportedReasoningTokens()
			require.Equal(t, test.known, cached)
			require.Equal(t, test.known, reasoning)
			encoded, err := json.Marshal(u)
			require.NoError(t, err)
			var roundTrip Usage
			require.NoError(t, json.Unmarshal(encoded, &roundTrip))
			_, cached = roundTrip.PromptTokensDetails.ReportedCachedTokens()
			_, reasoning = roundTrip.CompletionTokensDetails.ReportedReasoningTokens()
			require.Equal(t, test.known, cached)
			require.Equal(t, test.known, reasoning)
		})
	}
}
