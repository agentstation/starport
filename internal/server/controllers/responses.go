package controllers

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/agentstation/starport/internal/protocol/openai"
	"github.com/agentstation/starport/internal/proxy"
)

// ResponsesController serves POST /v1/responses: the stateless subset of
// the OpenAI Responses API. The codec maps the request onto the canonical
// chat request, so routing, budgets, caching, and usage recording run the
// same pipeline the chat route runs. The surface exists on the OpenAI
// dialect alone, because OpenRouter publishes no responses route.
type ResponsesController struct {
	*BaseHandler
}

// NewResponsesController creates a responses controller.
func NewResponsesController(service proxy.Proxy) *ResponsesController {
	return &ResponsesController{
		BaseHandler: NewProtocolBaseHandler(service, ProtocolOpenAI),
	}
}

// Create handles POST /v1/responses.
func (h *ResponsesController) Create(w http.ResponseWriter, r *http.Request) {
	decoded, err := openai.DecodeResponses(r.Body)
	if err != nil {
		// A stored-state field draws a 400 whose param names the field,
		// so the caller learns which feature to drop.
		var unsupported *openai.UnsupportedError
		if errors.As(err, &unsupported) {
			param := unsupported.Param
			openai.WriteError(w, http.StatusBadRequest, errorTypeInvalidRequest,
				unsupported.Message, &param)
			return
		}
		h.writeBodyRefusal(w, err)
		return
	}

	req := &proxy.ChatCompletionRequest{Request: decoded}
	ctx := r.Context()
	req.APIKey = h.getAPIKey(ctx)
	req.AccountID = h.getAccountID(ctx)
	req.KeyID = h.getAPIKeyID(ctx)
	req.APIKeyConfig, err = h.getAPIKeyRoutingConfig(ctx)
	if err != nil {
		h.writeCredentialStrategyError(w, err)
		return
	}
	req.RequestID = h.getRequestID(ctx)
	req.Protocol = string(h.protocol)

	r = r.WithContext(proxy.StartOverhead(ctx))

	if req.Request.Stream {
		h.handleStream(w, r, req)
		return
	}
	h.handleNonStream(w, r, req)
}

func (h *ResponsesController) handleNonStream(w http.ResponseWriter, r *http.Request, req *proxy.ChatCompletionRequest) {
	resp, err := h.service.ProcessChatCompletion(r.Context(), req)
	setOverheadHeader(w, r)
	if err != nil {
		h.logError(r.Context(), err, "responses completion failed")
		h.writeError(w, err)
		return
	}
	if err := openai.WriteJSON(w, http.StatusOK, openai.EncodeResponses(resp.Response)); err != nil {
		h.logError(r.Context(), err, "failed to write response")
	}
}

// handleStream writes the named Responses event sequence. Each frame
// carries an event name beside its data, and the stream ends with the
// terminal snapshot event rather than a [DONE] marker.
func (h *ResponsesController) handleStream(w http.ResponseWriter, r *http.Request, req *proxy.ChatCompletionRequest) {
	stream, err := h.service.ProcessChatCompletionStream(r.Context(), req)
	setOverheadHeader(w, r)
	if err != nil {
		h.logError(r.Context(), err, "responses stream failed")
		h.writeError(w, err)
		return
	}
	defer func() { _ = stream.Close() }()

	// http.Server.WriteTimeout applies to the whole response by default. Clear
	// the write deadline for SSE so healthy long-running streams are not cut off.
	_ = http.NewResponseController(w).SetWriteDeadline(time.Time{})

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // Disable nginx buffering

	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}

	encoder := &openai.ResponsesStreamEncoder{}
	for {
		event, err := stream.Read()
		if err == io.EOF {
			events, err := encoder.Finish()
			if err != nil {
				h.logError(r.Context(), err, "failed to close the responses stream")
				break
			}
			writeResponsesEvents(w, events)
			break
		}
		if err != nil {
			h.logError(r.Context(), err, "stream read error")
			break
		}
		if event == nil {
			h.logError(r.Context(), io.ErrUnexpectedEOF, "stream returned an empty event")
			break
		}
		events, err := encoder.Encode(*event)
		if err != nil {
			h.logError(r.Context(), err, "failed to encode a responses event")
			break
		}
		writeResponsesEvents(w, events)
	}
}

func writeResponsesEvents(w http.ResponseWriter, events []openai.ResponsesStreamEvent) {
	for _, event := range events {
		_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event.Type, event.Data)
	}
	if len(events) == 0 {
		return
	}
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
}
