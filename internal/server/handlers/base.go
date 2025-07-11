// Package handlers contains HTTP handlers for the Starport API
package handlers

import (
	"context"
	"net/http"

	"github.com/rs/zerolog/log"

	"github.com/agentstation/starport/internal/proxy"
	"github.com/agentstation/starport/internal/server/dto"
)

// BaseHandler provides common functionality for all handlers
type BaseHandler struct {
	service proxy.Service
}

// NewBaseHandler creates a new base handler
func NewBaseHandler(service proxy.Service) *BaseHandler {
	return &BaseHandler{
		service: service,
	}
}

// getRequestID extracts the request ID from the context
func (h *BaseHandler) getRequestID(ctx context.Context) string {
	if reqID, ok := ctx.Value("request_id").(string); ok {
		return reqID
	}
	return ""
}

// getAPIKey extracts the API key from the context
func (h *BaseHandler) getAPIKey(ctx context.Context) string {
	if apiKey, ok := ctx.Value("api_key").(string); ok {
		return apiKey
	}
	return ""
}

// logError logs an error with context
func (h *BaseHandler) logError(ctx context.Context, err error, msg string) {
	log.Error().
		Err(err).
		Str("request_id", h.getRequestID(ctx)).
		Msg(msg)
}

// writeError writes an error response based on the error type
func (h *BaseHandler) writeError(w http.ResponseWriter, err error) {
	switch e := err.(type) {
	case *proxy.ValidationError:
		dto.WriteValidationError(w, e.Field, e.Message)

	case *proxy.ProviderError:
		// Map provider errors to appropriate HTTP status codes
		status := http.StatusInternalServerError
		errType := dto.ErrorTypeServerError

		switch e.Code {
		case "rate_limit":
			status = http.StatusTooManyRequests
			errType = dto.ErrorTypeRateLimit
		case "auth_error":
			status = http.StatusUnauthorized
			errType = dto.ErrorTypeAuthenticationError
		case "permission_error":
			status = http.StatusForbidden
			errType = dto.ErrorTypePermissionError
		case "not_found":
			status = http.StatusNotFound
			errType = dto.ErrorTypeNotFound
		}

		dto.WriteError(w, status, errType, e.Message)

	case *proxy.RoutingError:
		dto.WriteError(w, http.StatusServiceUnavailable, dto.ErrorTypeServiceUnavailable, e.Error())

	default:
		// Handle specific sentinel errors
		switch err {
		case proxy.ErrNoValidModel:
			dto.WriteError(w, http.StatusBadRequest, dto.ErrorTypeInvalidRequest, "No valid model specified")
		case proxy.ErrNoAvailableProvider:
			dto.WriteError(w, http.StatusServiceUnavailable, dto.ErrorTypeServiceUnavailable, "No available provider for the requested model")
		case proxy.ErrRateLimitExceeded:
			dto.WriteError(w, http.StatusTooManyRequests, dto.ErrorTypeRateLimit, "Rate limit exceeded")
		case proxy.ErrInsufficientQuota:
			dto.WriteError(w, http.StatusPaymentRequired, dto.ErrorTypePermissionError, "Insufficient quota")
		case proxy.ErrStreamingNotSupported:
			dto.WriteError(w, http.StatusBadRequest, dto.ErrorTypeInvalidRequest, "Streaming not supported for this request")
		case proxy.ErrEmbeddingsNotSupported:
			dto.WriteError(w, http.StatusBadRequest, dto.ErrorTypeInvalidRequest, "Embeddings not supported by this provider")
		default:
			// Generic server error
			dto.WriteError(w, http.StatusInternalServerError, dto.ErrorTypeServerError, "Internal server error")
		}
	}
}
