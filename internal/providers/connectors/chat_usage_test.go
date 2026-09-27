package connectors

import (
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
}
