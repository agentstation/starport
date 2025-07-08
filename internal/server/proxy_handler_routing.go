package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/agentstation/starport/internal/connectors"
	"github.com/agentstation/starport/internal/routing"
	"github.com/rs/zerolog/log"
)

// handleChatCompletionsWithRouting handles chat completion requests with model routing
func (h *ProxyHandler) handleChatCompletionsWithRouting(w http.ResponseWriter, r *http.Request) {
	// Parse request
	var req connectors.ChatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid_request", "Invalid request body: "+err.Error())
		return
	}

	// Validate request
	if err := h.validateChatRequest(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}

	// Check if routing is needed
	if len(req.Models) > 0 || req.Model == routing.AutoModelID {
		// Use routing system
		h.handleRoutedChatRequest(w, r, &req)
	} else {
		// Single model request - use existing logic
		h.handleSingleModelChatRequest(w, r, &req)
	}
}

// handleRoutedChatRequest handles requests that need model routing
func (h *ProxyHandler) handleRoutedChatRequest(w http.ResponseWriter, r *http.Request, req *connectors.ChatRequest) {
	ctx := r.Context()

	// Create routing request
	routingReq := &routing.Request{
		ChatRequest: req,
		Models:      req.Models,
		// TODO: Get these from headers or API key config
		ProviderPreferences: h.extractProviderPreferences(r),
		APIKeyConfig:       h.extractAPIKeyConfig(r),
		Metadata:           h.extractRequestMetadata(req),
	}

	// If no models array but model is auto, create models array
	if len(routingReq.Models) == 0 && req.Model == routing.AutoModelID {
		routingReq.Models = []string{routing.AutoModelID}
	}

	// Get router (assuming it's available in handler)
	router := h.getRouter()

	// Route with fallback
	resp, err := router.RouteWithFallback(ctx, routingReq)
	if err != nil {
		// Check if it's a partial failure with metadata
		if resp != nil && resp.Metadata != nil {
			// Send error with routing metadata
			h.writeRoutingError(w, err, resp.Metadata)
			return
		}
		h.handleConnectorError(w, err)
		return
	}

	// Handle streaming vs non-streaming
	if req.Stream {
		// For streaming with routing, we need special handling
		// For now, return error as streaming with fallback is complex
		h.writeError(w, http.StatusNotImplemented, "streaming_routing_not_implemented", 
			"Streaming with model routing is not yet implemented")
		return
	}

	// Add model_used to response
	enhancedResp := h.enhanceResponseWithRouting(resp)

	// Write response
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(enhancedResp); err != nil {
		log.Error().Err(err).Msg("failed to encode response")
	}
}

// handleSingleModelChatRequest handles traditional single-model requests
func (h *ProxyHandler) handleSingleModelChatRequest(w http.ResponseWriter, r *http.Request, req *connectors.ChatRequest) {
	// Get connector based on model
	connector, model, err := h.connectorRegistry.GetByModel(req.Model)
	if err != nil {
		h.writeError(w, http.StatusBadRequest, "model_not_found", "Model not found: "+req.Model)
		return
	}

	// Store original model ID for response
	originalModelID := req.Model

	// Update model to remove provider prefix for provider APIs
	req.Model = model

	// Handle streaming vs non-streaming
	if req.Stream {
		h.handleStreamingChatWithModelUsed(w, r, connector, req, originalModelID)
	} else {
		h.handleNonStreamingChatWithModelUsed(w, r, connector, req, originalModelID)
	}
}

// handleNonStreamingChatWithModelUsed handles non-streaming chat with model_used field
func (h *ProxyHandler) handleNonStreamingChatWithModelUsed(w http.ResponseWriter, r *http.Request, 
	connector connectors.Connector, req *connectors.ChatRequest, modelUsed string) {
	
	ctx := r.Context()

	// Call connector
	resp, err := connector.Chat(ctx, req)
	if err != nil {
		h.handleConnectorError(w, err)
		return
	}

	// Transform response to include full model ID and model_used
	resp.Model = modelUsed
	
	// Create enhanced response with model_used field
	enhancedResp := map[string]interface{}{
		"id":                resp.ID,
		"object":            resp.Object,
		"created":           resp.Created,
		"model":             resp.Model,
		"model_used":        modelUsed, // Add model_used field
		"choices":           resp.Choices,
		"usage":             resp.Usage,
		"system_fingerprint": resp.SystemFingerprint,
	}

	// Write response
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(enhancedResp); err != nil {
		log.Error().Err(err).Msg("failed to encode response")
	}
}

// handleStreamingChatWithModelUsed handles streaming chat with model_used in first chunk
func (h *ProxyHandler) handleStreamingChatWithModelUsed(w http.ResponseWriter, r *http.Request,
	connector connectors.Connector, req *connectors.ChatRequest, modelUsed string) {
	
	ctx := r.Context()

	// Call connector for streaming
	stream, err := connector.ChatStream(ctx, req)
	if err != nil {
		h.handleConnectorError(w, err)
		return
	}
	defer func() {
		_ = stream.Close()
	}()

	// Set headers for SSE
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	// Flush headers
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}

	firstChunk := true

	// Stream chunks
	for {
		chunk, err := stream.Recv()
		if err != nil {
			if err == io.EOF {
				// Send done marker
				_, _ = fmt.Fprintf(w, "data: %s\n\n", connectors.SSEDone)
				if f, ok := w.(http.Flusher); ok {
					f.Flush()
				}
				return
			}
			// Log error but don't send to client in stream
			log.Error().Err(err).Msg("streaming error")
			return
		}

		// Transform model in chunk
		chunk.Model = modelUsed

		// Marshal chunk - add model_used to first chunk
		var data []byte
		if firstChunk {
			// Create enhanced first chunk with model_used
			enhancedChunk := map[string]interface{}{
				"id":                chunk.ID,
				"object":            chunk.Object,
				"created":           chunk.Created,
				"model":             chunk.Model,
				"model_used":        modelUsed, // Add model_used to first chunk
				"choices":           chunk.Choices,
				"system_fingerprint": chunk.SystemFingerprint,
			}
			data, err = json.Marshal(enhancedChunk)
			firstChunk = false
		} else {
			data, err = json.Marshal(chunk)
		}

		if err != nil {
			log.Error().Err(err).Msg("failed to marshal chunk")
			continue
		}

		// Write SSE data
		_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}
}

// enhanceResponseWithRouting adds routing metadata to response
func (h *ProxyHandler) enhanceResponseWithRouting(resp *routing.Response) map[string]interface{} {
	result := map[string]interface{}{
		"id":                resp.ID,
		"object":            resp.Object,
		"created":           resp.Created,
		"model":             resp.Model,
		"model_used":        resp.ModelUsed,
		"choices":           resp.Choices,
		"usage":             resp.Usage,
		"system_fingerprint": resp.SystemFingerprint,
	}

	// Optionally add routing metadata if verbose mode
	if h.isVerboseMode() && resp.Metadata != nil {
		result["routing_metadata"] = resp.Metadata
	}

	return result
}

// extractProviderPreferences extracts provider preferences from request
func (h *ProxyHandler) extractProviderPreferences(_ *http.Request) *routing.ProviderPreferences {
	// TODO: Extract from headers or request context
	// For now, return nil to use defaults
	return nil
}

// extractAPIKeyConfig extracts API key configuration
func (h *ProxyHandler) extractAPIKeyConfig(_ *http.Request) *routing.APIKeyConfig {
	// TODO: Extract from authenticated API key
	// For now, return nil to allow all providers
	return nil
}

// extractRequestMetadata extracts metadata for routing decisions
func (h *ProxyHandler) extractRequestMetadata(req *connectors.ChatRequest) *routing.RequestMetadata {
	// Basic metadata extraction
	metadata := &routing.RequestMetadata{
		EstimatedTokens:  h.estimateTokens(req),
		RequiredFeatures: []string{},
	}

	// Check for vision
	if h.hasVisionContent(req) {
		metadata.RequiredFeatures = append(metadata.RequiredFeatures, "vision")
	}

	// Check for functions
	if len(req.Tools) > 0 {
		metadata.RequiredFeatures = append(metadata.RequiredFeatures, "functions")
	}

	// Check for streaming
	if req.Stream {
		metadata.RequiredFeatures = append(metadata.RequiredFeatures, "streaming")
	}

	return metadata
}

// hasVisionContent checks if request contains vision content
func (h *ProxyHandler) hasVisionContent(req *connectors.ChatRequest) bool {
	for _, msg := range req.Messages {
		if parts, ok := msg.Content.([]interface{}); ok {
			for _, part := range parts {
				if p, ok := part.(map[string]interface{}); ok {
					if p["type"] == "image_url" {
						return true
					}
				}
			}
		}
	}
	return false
}

// estimateTokens provides rough token estimate
func (h *ProxyHandler) estimateTokens(req *connectors.ChatRequest) int {
	// Very rough: 4 chars = 1 token
	chars := 0
	for _, msg := range req.Messages {
		switch content := msg.Content.(type) {
		case string:
			chars += len(content)
		}
	}
	return chars / 4
}

// getRouter returns the router instance
func (h *ProxyHandler) getRouter() routing.ModelRouter {
	// TODO: Store router in handler struct
	// For now, create new instance
	return routing.NewRouter(h.connectorRegistry)
}

// isVerboseMode checks if verbose mode is enabled
func (h *ProxyHandler) isVerboseMode() bool {
	// TODO: Check from config or headers
	return false
}

// writeRoutingError writes an error response with routing metadata
func (h *ProxyHandler) writeRoutingError(w http.ResponseWriter, err error, metadata *routing.Metadata) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadGateway)
	
	errorResp := map[string]interface{}{
		"error": map[string]interface{}{
			"type":    "routing_failed",
			"message": err.Error(),
		},
	}

	if metadata != nil {
		errorResp["error"].(map[string]interface{})["routing_metadata"] = metadata
	}

	_ = json.NewEncoder(w).Encode(errorResp)
}