package controllers

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/agentstation/starport/internal/inference"
	"github.com/agentstation/starport/internal/protocol/openai"
	"github.com/agentstation/starport/internal/protocol/openrouter"
	"github.com/agentstation/starport/internal/proxy"
)

// ChatController handles chat completion endpoints
type ChatController struct {
	*BaseHandler
}

// NewChatController creates a new chat controller
func NewChatController(service proxy.Proxy) *ChatController {
	return newChatController(service, ProtocolOpenAI)
}

// NewOpenRouterChatController creates an OpenRouter chat controller.
func NewOpenRouterChatController(service proxy.Proxy) *ChatController {
	return newChatController(service, ProtocolOpenRouter)
}

func newChatController(service proxy.Proxy, protocol Protocol) *ChatController {
	return &ChatController{
		BaseHandler: NewProtocolBaseHandler(service, protocol),
	}
}

// Create handles POST /v1/chat/completions and /api/v1/chat/completions
func (h *ChatController) Create(w http.ResponseWriter, r *http.Request) {
	req, unenforced, err := h.decodeRequest(r)
	if err != nil {
		h.writeInvalidRequest(w, "Invalid request body: "+err.Error())
		return
	}
	if len(unenforced) > 0 {
		// Documented provider fields Starport accepts but cannot yet enforce
		// are reported loudly instead of silently dropped.
		w.Header().Set("X-Starport-Unenforced-Provider-Fields", strings.Join(unenforced, ","))
	}

	// Add context from HTTP request
	ctx := r.Context()
	req.APIKey = h.getAPIKey(ctx)
	req.TenantID = h.getTenantID(ctx)
	req.APIKeyConfig, err = h.getAPIKeyRoutingConfig(ctx)
	if err != nil {
		h.writeInvalidRequest(w, "Invalid provider credential strategy")
		return
	}
	req.RequestID = h.getRequestID(ctx)
	req.Protocol = string(h.protocol)

	// Handle streaming vs non-streaming
	if req.Request.Stream {
		h.handleStream(w, r, req)
	} else {
		h.handleNonStream(w, r, req)
	}
}

func (h *ChatController) decodeRequest(r *http.Request) (*proxy.ChatCompletionRequest, []string, error) {
	if h.protocol == ProtocolOpenRouter {
		decoded, err := openrouter.DecodeChat(r.Body)
		if err != nil {
			return nil, nil, err
		}
		request := &proxy.ChatCompletionRequest{Request: decoded.Inference, Route: decoded.Route, Preset: decoded.Preset}
		if decoded.Provider != nil {
			allowFallback := true
			if decoded.Provider.AllowFallbacks != nil {
				allowFallback = *decoded.Provider.AllowFallbacks
			}
			request.Provider = &proxy.ProviderPreferences{
				Order:         append([]string(nil), decoded.Provider.Order...),
				Only:          append([]string(nil), decoded.Provider.Only...),
				Ignore:        append([]string(nil), decoded.Provider.Ignore...),
				AllowFallback: allowFallback,
				Sort:          decoded.Provider.Sort,
			}
			if decoded.Provider.MaxPrice != nil {
				request.Provider.MaxPromptPricePer1M = decoded.Provider.MaxPrice.Prompt
				request.Provider.MaxCompletionPricePer1M = decoded.Provider.MaxPrice.Completion
			}
		}
		return request, decoded.UnenforcedProviderFields, nil
	}
	decoded, err := openai.DecodeChat(r.Body)
	if err != nil {
		return nil, nil, err
	}
	return &proxy.ChatCompletionRequest{Request: decoded}, nil, nil
}

// handleNonStream handles non-streaming chat completions
func (h *ChatController) handleNonStream(w http.ResponseWriter, r *http.Request, req *proxy.ChatCompletionRequest) {
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
		if resp.CacheStatus == proxy.CacheStatusHit && resp.CacheAge > 0 {
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

	// Set cache pricing headers if available
	if resp.CacheCost != nil {
		w.Header().Set("X-Cache-Write-Cost", fmt.Sprintf("%.6f", resp.CacheCost.WriteTokens))
		w.Header().Set("X-Cache-Read-Cost", fmt.Sprintf("%.6f", resp.CacheCost.ReadTokens))
		if resp.CacheCost.TotalCost > 0 {
			w.Header().Set("X-Cache-Total-Cost", fmt.Sprintf("%.6f", resp.CacheCost.TotalCost))
		}
	}

	if err := h.writeChatResponse(w, resp.Response); err != nil {
		h.logError(r.Context(), err, "failed to write response")
	}
}

func (h *ChatController) writeChatResponse(w http.ResponseWriter, response inference.ChatResponse) error {
	if h.protocol == ProtocolOpenRouter {
		return openrouter.WriteJSON(w, http.StatusOK, openrouter.EncodeChat(response))
	}
	return openai.WriteJSON(w, http.StatusOK, openai.EncodeChat(response))
}

// handleStream handles streaming chat completions
func (h *ChatController) handleStream(w http.ResponseWriter, r *http.Request, req *proxy.ChatCompletionRequest) {
	// Process the streaming request
	stream, err := h.service.ProcessChatCompletionStream(r.Context(), req)
	if err != nil {
		h.logError(r.Context(), err, "chat stream failed")
		h.writeError(w, err)
		return
	}
	defer func() { _ = stream.Close() }()

	// http.Server.WriteTimeout applies to the whole response by default. Clear
	// the write deadline for SSE so healthy long-running streams are not cut off.
	_ = http.NewResponseController(w).SetWriteDeadline(time.Time{})

	// Check if stream provides cache status
	if cacheProvider, ok := stream.(proxy.CacheStatusProvider); ok {
		if cacheStatus := cacheProvider.GetCacheStatus(); cacheStatus != "" {
			w.Header().Set("X-Cache", cacheStatus)
			// Also set cache age if it's a hit
			if cacheStatus == "HIT" {
				if cacheAge := cacheProvider.GetCacheAge(); cacheAge > 0 {
					w.Header().Set("X-Cache-Age", fmt.Sprintf("%d", cacheAge))
				}
			}
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

	var lastEvent inference.StreamEvent
	for {
		event, err := stream.Read()
		if err == io.EOF {
			// End of stream
			_, _ = fmt.Fprintf(w, "data: [DONE]\n\n")
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}
			break
		}

		if err != nil {
			h.logError(r.Context(), err, "stream read error")
			if h.protocol == ProtocolOpenRouter {
				h.writeOpenRouterStreamError(w, lastEvent)
			}
			break
		}
		if event == nil {
			err = io.ErrUnexpectedEOF
			h.logError(r.Context(), err, "stream returned an empty event")
			if h.protocol == ProtocolOpenRouter {
				h.writeOpenRouterStreamError(w, lastEvent)
			}
			break
		}
		lastEvent = event.Clone()

		data, err := h.encodeStreamEvent(*event)
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

func (h *ChatController) encodeStreamEvent(event inference.StreamEvent) ([]byte, error) {
	if h.protocol == ProtocolOpenRouter {
		return json.Marshal(openrouter.EncodeStream(event))
	}
	return json.Marshal(openai.EncodeStream(event))
}

func (h *ChatController) writeOpenRouterStreamError(w http.ResponseWriter, event inference.StreamEvent) {
	chunk := openrouter.EncodeStreamError(event, http.StatusBadGateway, "Provider stream failed", map[string]any{
		openRouterErrorTypeField: errorTypeProvider,
	})
	data, err := json.Marshal(chunk)
	if err != nil {
		return
	}
	_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
}
