package server

import (
	"fmt"

	"github.com/agentstation/starport/internal/cache"
	"github.com/agentstation/starport/internal/connectors"
)

// ConvertToCacheChatRequest converts connector request to cache request
func ConvertToCacheChatRequest(req *connectors.ChatRequest) cache.ChatCompletionRequest {
	msgs := make([]cache.Message, len(req.Messages))
	for i, msg := range req.Messages {
		msgs[i] = cache.Message{
			Role:    msg.Role,
			Content: fmt.Sprintf("%v", msg.Content), // Handle string or complex content
		}
	}

	// Convert LogitBias from map[string]int to map[string]float32
	var logitBias map[string]float32
	if req.LogitBias != nil {
		logitBias = make(map[string]float32)
		for k, v := range req.LogitBias {
			logitBias[k] = float32(v)
		}
	}

	// Convert user to pointer
	var user *string
	if req.User != "" {
		user = &req.User
	}

	// Convert tools to interface slice
	var tools []interface{}
	if req.Tools != nil {
		tools = make([]interface{}, len(req.Tools))
		for i, tool := range req.Tools {
			tools[i] = tool
		}
	}

	return cache.ChatCompletionRequest{
		Model:            req.Model,
		Messages:         msgs,
		Temperature:      req.Temperature,
		MaxTokens:        req.MaxTokens,
		TopP:             req.TopP,
		N:                nil, // ChatRequest doesn't have N field
		Stop:             req.Stop,
		PresencePenalty:  req.PresencePenalty,
		FrequencyPenalty: req.FrequencyPenalty,
		LogitBias:        logitBias,
		User:             user,
		Seed:             req.Seed,
		Tools:            tools,
		ToolChoice:       req.ToolChoice,
		ResponseFormat:   req.ResponseFormat,
	}
}