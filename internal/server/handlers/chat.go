package handlers

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/agentstation/starport/internal/proxy"
	"github.com/agentstation/starport/internal/server/dto"
)

// ChatHandler handles chat completion endpoints
type ChatHandler struct {
	*BaseHandler
}

// NewChatHandler creates a new chat handler
func NewChatHandler(service proxy.Service) *ChatHandler {
	return &ChatHandler{
		BaseHandler: NewBaseHandler(service),
	}
}

// Create handles POST /v1/chat/completions and /api/v1/chat/completions
func (h *ChatHandler) Create(w http.ResponseWriter, r *http.Request) {
	// Parse request
	req, err := dto.ParseChatCompletionRequest(r)
	if err != nil {
		dto.WriteError(w, http.StatusBadRequest, dto.ErrorTypeInvalidRequest, "Invalid request body")
		return
	}

	// Add context from HTTP request
	ctx := r.Context()
	req.APIKey = h.getAPIKey(ctx)
	req.RequestID = h.getRequestID(ctx)

	// Handle streaming vs non-streaming
	if req.Stream {
		h.handleStream(w, r, req)
	} else {
		h.handleNonStream(w, r, req)
	}
}

// handleNonStream handles non-streaming chat completions
func (h *ChatHandler) handleNonStream(w http.ResponseWriter, r *http.Request, req *proxy.ChatCompletionRequest) {
	// Check If-None-Match header for conditional request
	ifNoneMatch := r.Header.Get("If-None-Match")
	
	// Process the request
	resp, err := h.service.ProcessChatCompletion(r.Context(), req)
	if err != nil {
		h.logError(r.Context(), err, "chat completion failed")
		h.writeError(w, err)
		return
	}

	// Set cache headers from response
	if resp.CacheStatus != "" {
		w.Header().Set("X-Cache", resp.CacheStatus)
		if resp.CacheStatus == "HIT" && resp.CacheAge > 0 {
			w.Header().Set("X-Cache-Age", fmt.Sprintf("%d", resp.CacheAge))
		}
	}
	
	// Set ETag header
	if resp.ETag != "" {
		w.Header().Set("ETag", resp.ETag)
		
		// Check for 304 Not Modified
		if ifNoneMatch != "" && ifNoneMatch == resp.ETag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
	}

	// Write response
	if err := dto.WriteJSON(w, http.StatusOK, resp); err != nil {
		h.logError(r.Context(), err, "failed to write response")
	}
}

// handleStream handles streaming chat completions
func (h *ChatHandler) handleStream(w http.ResponseWriter, r *http.Request, req *proxy.ChatCompletionRequest) {
	// Process the streaming request
	stream, err := h.service.ProcessChatCompletionStream(r.Context(), req)
	if err != nil {
		h.logError(r.Context(), err, "chat stream failed")
		h.writeError(w, err)
		return
	}
	defer func() { _ = stream.Close() }()

	// Check if stream provides cache status
	if cacheProvider, ok := stream.(proxy.CacheStatusProvider); ok {
		if cacheStatus := cacheProvider.GetCacheStatus(); cacheStatus != "" {
			w.Header().Set("X-Cache", cacheStatus)
		}
	}

	// Set SSE headers
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // Disable nginx buffering

	// Flush headers
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}

	// Stream chunks
	for {
		chunk, err := stream.Read()
		if err == io.EOF {
			// End of stream
			_, _ = fmt.Fprintf(w, "data: [DONE]\n\n")
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}
			break
		}

		if err != nil {
			// Log error but don't write it to stream
			h.logError(r.Context(), err, "stream read error")
			break
		}

		// Write chunk as SSE
		data, err := json.Marshal(chunk)
		if err != nil {
			h.logError(r.Context(), err, "failed to marshal chunk")
			continue
		}

		_, _ = fmt.Fprintf(w, "data: %s\n\n", data)

		// Flush after each chunk
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
	}
}
