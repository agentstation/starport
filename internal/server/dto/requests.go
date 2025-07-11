// Package dto contains Data Transfer Objects for HTTP requests and responses
package dto

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/agentstation/starport/internal/proxy"
)

// ParseChatCompletionRequest parses a chat completion request from an HTTP request
func ParseChatCompletionRequest(r *http.Request) (*proxy.ChatCompletionRequest, error) {
	var req proxy.ChatCompletionRequest

	// Read body
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, err
	}

	// Parse JSON
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, err
	}

	// Extract additional context from HTTP request
	req.RequestID = r.Header.Get("X-Request-ID")
	if req.RequestID == "" {
		req.RequestID = r.Context().Value("request_id").(string)
	}

	// API key will be set by authentication middleware
	if apiKey := r.Context().Value("api_key"); apiKey != nil {
		req.APIKey = apiKey.(string)
	}

	return &req, nil
}

// ParseEmbeddingsRequest parses an embeddings request from an HTTP request
func ParseEmbeddingsRequest(r *http.Request) (*proxy.EmbeddingsRequest, error) {
	var req proxy.EmbeddingsRequest

	// Read body
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, err
	}

	// Parse JSON
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, err
	}

	// Extract additional context from HTTP request
	req.RequestID = r.Header.Get("X-Request-ID")
	if req.RequestID == "" {
		req.RequestID = r.Context().Value("request_id").(string)
	}

	// API key will be set by authentication middleware
	if apiKey := r.Context().Value("api_key"); apiKey != nil {
		req.APIKey = apiKey.(string)
	}

	return &req, nil
}
