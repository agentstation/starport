package controllers

import (
	"context"
	"mime/multipart"
	"net/http"

	"github.com/agentstation/starport/internal/inference"
	"github.com/agentstation/starport/internal/protocol/openai"
	"github.com/agentstation/starport/internal/protocol/openrouter"
	"github.com/agentstation/starport/internal/proxy"
)

// maxMediaUploadBytes bounds one media request body. An upload arrives whole
// and is held for replay across route attempts, so an unbounded body is an
// unbounded allocation. The limit sits above the largest audio file the
// OpenAI-compatible providers accept.
const maxMediaUploadBytes = 64 << 20

// maxMediaMemoryBytes is how much of a multipart body is held in memory before
// the rest spills to a temporary file.
const maxMediaMemoryBytes = 8 << 20

// MediaController serves the three dedicated media operations. One controller
// holds all of them, because they share their identity handling, their upload
// reading, and their protocol switch, and the only part that differs is which
// proxy method the handler calls.
type MediaController struct {
	*BaseHandler
}

// NewMediaController creates an OpenAI-protocol media controller.
func NewMediaController(service proxy.Proxy) *MediaController {
	return &MediaController{BaseHandler: NewProtocolBaseHandler(service, ProtocolOpenAI)}
}

// NewOpenRouterMediaController creates an OpenRouter-protocol media controller.
func NewOpenRouterMediaController(service proxy.Proxy) *MediaController {
	return &MediaController{BaseHandler: NewProtocolBaseHandler(service, ProtocolOpenRouter)}
}

// GenerateImages handles POST /v1/images/generations and POST /api/v1/images.
func (h *MediaController) GenerateImages(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxMediaUploadBytes)
	request, err := h.decodeImages(r)
	if err != nil {
		h.writeBodyRefusal(w, err)
		return
	}
	h.serveImages(w, r, request)
}

// EditImages handles POST /v1/images/edits. An edit carries its source image,
// so the body is multipart form data rather than JSON. OpenRouter publishes no
// edit path, so this handler serves the OpenAI family alone.
func (h *MediaController) EditImages(w http.ResponseWriter, r *http.Request) {
	form, err := h.parseUpload(w, r)
	if err != nil {
		h.writeBodyRefusal(w, err)
		return
	}
	request, err := openai.DecodeImagesForm(form)
	if err != nil {
		h.writeBodyRefusal(w, err)
		return
	}
	h.serveImages(w, r, request)
}

func (h *MediaController) serveImages(
	w http.ResponseWriter,
	r *http.Request,
	request inference.ImagesRequest,
) {
	ctx := r.Context()
	req, err := mediaGatewayRequest(ctx, h.BaseHandler, request)
	if err != nil {
		h.writeCredentialStrategyError(w, err)
		return
	}
	resp, err := h.service.ProcessImages(ctx, req)
	if err != nil {
		h.logError(ctx, err, "image generation failed")
		h.writeError(w, err)
		return
	}
	if err := h.writeImages(w, resp.Response); err != nil {
		h.logError(ctx, err, "failed to write response")
	}
}

// Speech handles POST /v1/audio/speech and POST /api/v1/audio/speech. The
// answer is an encoded audio file rather than JSON, so the handler writes the
// provider's bytes and repeats the media type the provider stated.
func (h *MediaController) Speech(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxMediaUploadBytes)
	request, err := h.decodeSpeech(r)
	if err != nil {
		h.writeBodyRefusal(w, err)
		return
	}
	ctx := r.Context()
	req, err := mediaGatewayRequest(ctx, h.BaseHandler, request)
	if err != nil {
		h.writeCredentialStrategyError(w, err)
		return
	}
	resp, err := h.service.ProcessSpeech(ctx, req)
	if err != nil {
		h.logError(ctx, err, "speech synthesis failed")
		h.writeError(w, err)
		return
	}
	contentType := resp.Response.ContentType
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	w.Header().Set("Content-Type", contentType)
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write(resp.Response.Audio); err != nil {
		h.logError(ctx, err, "failed to write response")
	}
}

// Transcribe handles POST /v1/audio/transcriptions and
// POST /api/v1/audio/transcriptions.
func (h *MediaController) Transcribe(w http.ResponseWriter, r *http.Request) {
	h.serveTranscription(w, r, false)
}

// Translate handles POST /v1/audio/translations. It asks for an English
// transcript of speech in another language. OpenRouter publishes no
// translation path, so this handler serves the OpenAI family alone.
func (h *MediaController) Translate(w http.ResponseWriter, r *http.Request) {
	h.serveTranscription(w, r, true)
}

func (h *MediaController) serveTranscription(w http.ResponseWriter, r *http.Request, translate bool) {
	form, err := h.parseUpload(w, r)
	if err != nil {
		h.writeBodyRefusal(w, err)
		return
	}
	request, err := h.decodeTranscription(form, translate)
	if err != nil {
		h.writeBodyRefusal(w, err)
		return
	}
	ctx := r.Context()
	req, err := mediaGatewayRequest(ctx, h.BaseHandler, request)
	if err != nil {
		h.writeCredentialStrategyError(w, err)
		return
	}
	resp, err := h.service.ProcessTranscription(ctx, req)
	if err != nil {
		h.logError(ctx, err, "transcription failed")
		h.writeError(w, err)
		return
	}
	if err := h.writeTranscription(w, resp.Response); err != nil {
		h.logError(ctx, err, "failed to write response")
	}
}

// parseUpload reads one multipart body. The size limit is applied before
// parsing, so an oversized upload is refused rather than buffered.
func (h *MediaController) parseUpload(w http.ResponseWriter, r *http.Request) (*multipart.Form, error) {
	r.Body = http.MaxBytesReader(w, r.Body, maxMediaUploadBytes)
	// #nosec G120 -- the line above bounds the body at maxMediaUploadBytes, so
	// the parse below reads a bounded reader rather than an unbounded one.
	if err := r.ParseMultipartForm(maxMediaMemoryBytes); err != nil {
		return nil, err
	}
	return r.MultipartForm, nil
}

// mediaGatewayRequest carries the caller identity onto one media request. All
// three operations read the same identity a chat request reads, so it is
// written once over the request type.
func mediaGatewayRequest[Request any](
	ctx context.Context,
	h *BaseHandler,
	request Request,
) (*proxy.MediaRequest[Request], error) {
	config, err := h.getAPIKeyRoutingConfig(ctx)
	if err != nil {
		return nil, err
	}
	return &proxy.MediaRequest[Request]{
		Request:      request,
		APIKey:       h.getAPIKey(ctx),
		TenantID:     h.getTenantID(ctx),
		KeyID:        h.getAPIKeyID(ctx),
		APIKeyConfig: config,
		RequestID:    h.getRequestID(ctx),
		Protocol:     string(h.protocol),
	}, nil
}

func (h *MediaController) decodeImages(r *http.Request) (inference.ImagesRequest, error) {
	if h.protocol == ProtocolOpenRouter {
		return openrouter.DecodeImages(r.Body)
	}
	return openai.DecodeImages(r.Body)
}

func (h *MediaController) decodeSpeech(r *http.Request) (inference.SpeechRequest, error) {
	if h.protocol == ProtocolOpenRouter {
		return openrouter.DecodeSpeech(r.Body)
	}
	return openai.DecodeSpeech(r.Body)
}

func (h *MediaController) decodeTranscription(
	form *multipart.Form,
	translate bool,
) (inference.TranscriptionRequest, error) {
	if h.protocol == ProtocolOpenRouter {
		return openrouter.DecodeTranscriptionForm(form)
	}
	return openai.DecodeTranscriptionForm(form, translate)
}

func (h *MediaController) writeImages(w http.ResponseWriter, response inference.ImagesResponse) error {
	if h.protocol == ProtocolOpenRouter {
		return openrouter.WriteJSON(w, http.StatusOK, openrouter.EncodeImages(response))
	}
	return openai.WriteJSON(w, http.StatusOK, openai.EncodeImages(response))
}

func (h *MediaController) writeTranscription(
	w http.ResponseWriter,
	response inference.TranscriptionResponse,
) error {
	if h.protocol == ProtocolOpenRouter {
		return openrouter.WriteJSON(w, http.StatusOK, openrouter.EncodeTranscription(response))
	}
	return openai.WriteJSON(w, http.StatusOK, openai.EncodeTranscription(response))
}
