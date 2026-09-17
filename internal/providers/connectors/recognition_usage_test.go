package connectors

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestRecognitionUsageRetainsPresenceAndDimensions(t *testing.T) {
	missing, err := RecognitionResponseToInference(&RecognitionResponse{})
	require.NoError(t, err)
	require.Nil(t, missing.Usage)
	zero, err := RecognitionResponseToInference(&RecognitionResponse{Usage: &MediaUsage{}})
	require.NoError(t, err)
	require.NotNil(t, zero.Usage)
	measured, err := RecognitionResponseToInference(&RecognitionResponse{Usage: &MediaUsage{
		InputTokens: 100, OutputTokens: 30, TotalTokens: 130, ReasoningTokens: 10, CacheReadTokens: 20, CacheWriteTokens: 5, AudioInputTokens: 10, AudioOutputTokens: 4,
	}})
	require.NoError(t, err)
	require.Equal(t, 100, measured.Usage.InputTokens)
	require.Equal(t, 30, measured.Usage.OutputTokens)
	require.Equal(t, 10, measured.Usage.ReasoningTokens)
	require.Equal(t, 20, measured.Usage.CacheReadTokens)
	require.Equal(t, 5, measured.Usage.CacheWriteTokens)
	require.Equal(t, 10, measured.Usage.AudioInputTokens)
	require.Equal(t, 4, measured.Usage.AudioOutputTokens)
	cloned := measured.Clone()
	cloned.Usage.InputTokens = 1
	require.Equal(t, 100, measured.Usage.InputTokens)
}

func TestGeminiBilledOutputIncludesThinkingOnce(t *testing.T) {
	measured := convertGeminiUsage(geminiUsageMetadata{
		PromptTokenCount: 100, CandidatesTokenCount: 20, ThoughtsTokenCount: 10, TotalTokenCount: 130, CachedContentTokenCount: 15,
	})
	require.Equal(t, 30, measured.CompletionTokens)
	require.Equal(t, 10, measured.CompletionTokensDetails.ReasoningTokens)
	require.Equal(t, 130, measured.TotalTokens)
	require.Equal(t, 15, measured.PromptTokensDetails.CachedTokens)
}
