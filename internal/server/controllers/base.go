// Package controllers contains HTTP handlers for the Starport API.
package controllers

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5/middleware"
	"github.com/rs/zerolog/log"

	"github.com/agentstation/starport/internal/catalog"
	"github.com/agentstation/starport/internal/failure"
	"github.com/agentstation/starport/internal/protocol/openai"
	"github.com/agentstation/starport/internal/protocol/openrouter"
	"github.com/agentstation/starport/internal/providers/keyring"
	"github.com/agentstation/starport/internal/proxy"
	"github.com/agentstation/starport/internal/server/requestctx"
)

// Protocol identifies one external HTTP dialect.
type Protocol string

const (
	// ProtocolOpenAI selects the OpenAI v1 contract.
	ProtocolOpenAI Protocol = "openai"
	// ProtocolOpenRouter selects the OpenRouter v1 contract.
	ProtocolOpenRouter Protocol = "openrouter"

	errorTypeInvalidRequest     = "invalid_request_error"
	errorTypeServer             = "server_error"
	errorTypeRateLimit          = "rate_limit_error"
	errorTypePermission         = "permission_error"
	errorTypeServiceUnavailable = "service_unavailable"
	errorTypeProvider           = "provider_error"
	errorTypeNotFound           = "not_found_error"
	errorCodeNotFound           = "not_found"
	openRouterErrorTypeField    = "error_type"
	providerField               = "provider"
	responseCountField          = "count"
	responseMessageField        = "message"
	fieldLimit                  = "limit"
	fieldCreatedAt              = "created_at"
	fieldError                  = "error"
	fieldRequests               = "requests"
	fieldTokens                 = "tokens"
	// fieldAccountID is the one wire name for an account: the URL parameter
	// that addresses one, the response field that reports one, and the log
	// field that records one.
	fieldAccountID = "account_id"
)

// BaseHandler provides common functionality for all handlers
type BaseHandler struct {
	service  proxy.Proxy
	protocol Protocol
}

// NewBaseHandler creates a new base handler
func NewBaseHandler(service proxy.Proxy) *BaseHandler {
	return NewProtocolBaseHandler(service, ProtocolOpenAI)
}

// NewProtocolBaseHandler creates a handler for one explicit wire dialect.
func NewProtocolBaseHandler(service proxy.Proxy, protocol Protocol) *BaseHandler {
	return &BaseHandler{
		service:  service,
		protocol: protocol,
	}
}

// getRequestID extracts the request ID from the context
func (h *BaseHandler) getRequestID(ctx context.Context) string {
	return middleware.GetReqID(ctx)
}

// getAPIKey extracts the API key from the context
func (h *BaseHandler) getAPIKey(ctx context.Context) string {
	if apiKey, ok := requestctx.GetAPIKey(ctx); ok {
		return apiKey
	}
	return ""
}

// getAccountID extracts the account the request runs under. Many keys can
// belong to one account, so this is not the API key ID.
func (h *BaseHandler) getAccountID(ctx context.Context) string {
	return requestctx.AccountIDOrDefault(ctx)
}

// getAPIKeyID extracts the authenticated key. Usage attribution and per-key
// limits read this, not the account.
func (h *BaseHandler) getAPIKeyID(ctx context.Context) string {
	if keyID, ok := requestctx.GetAPIKeyID(ctx); ok {
		return keyID
	}
	return ""
}

// writeCredentialStrategyError separates a malformed strategy from a forbidden
// one. A value the gateway cannot parse is the caller's mistake and answers
// 400. A value the gateway understands but the account is not permitted to use
// answers 403, because the caller is authenticated and the operator withheld
// the credential deliberately.
func (h *BaseHandler) writeCredentialStrategyError(w http.ResponseWriter, err error) {
	if errors.Is(err, keyring.ErrStrategyWidens) {
		h.writePermissionDenied(w,
			"This account may use only its own provider credentials.")
		return
	}
	h.writeInvalidRequest(w, "Invalid provider credential strategy")
}

func (h *BaseHandler) writePermissionDenied(w http.ResponseWriter, message string) {
	if h.protocol == ProtocolOpenRouter {
		openrouter.WriteError(w, http.StatusForbidden, message, map[string]any{openRouterErrorTypeField: errorTypePermission})
		return
	}
	openai.WriteError(w, http.StatusForbidden, errorTypePermission, message, nil)
}

// writeBodyRefusal answers a request body the gateway would not read.
//
// A body above the size limit answers 413 rather than 400. The two statuses
// tell the caller different things: 400 says the request was malformed, and a
// caller who reads that about a valid request with one large attachment has no
// reason to try a smaller one. The message states the limit, which the reader
// itself reports, so the caller learns the number to cut to.
func (h *BaseHandler) writeBodyRefusal(w http.ResponseWriter, err error) {
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		message := fmt.Sprintf("Request body is above the %d byte limit", tooLarge.Limit)
		if h.protocol == ProtocolOpenRouter {
			openrouter.WriteError(w, http.StatusRequestEntityTooLarge, message,
				map[string]any{openRouterErrorTypeField: errorTypeInvalidRequest})
			return
		}
		openai.WriteError(w, http.StatusRequestEntityTooLarge, errorTypeInvalidRequest, message, nil)
		return
	}
	h.writeInvalidRequest(w, "Invalid request body: "+err.Error())
}

func (h *BaseHandler) writeInvalidRequest(w http.ResponseWriter, message string) {
	if h.protocol == ProtocolOpenRouter {
		openrouter.WriteError(w, http.StatusBadRequest, message, map[string]any{openRouterErrorTypeField: errorTypeInvalidRequest})
		return
	}
	openai.WriteError(w, http.StatusBadRequest, errorTypeInvalidRequest, message, nil)
}

// getAPIKeyRoutingConfig extracts routing restrictions from the authenticated API key.
func (h *BaseHandler) getAPIKeyRoutingConfig(ctx context.Context) (*proxy.APIKeyRoutingConfig, error) {
	apiKey, ok := requestctx.GetAPIKeyModel(ctx)
	if !ok || apiKey == nil {
		return nil, nil
	}

	// The operator sets the account's strategy. The key may narrow it, so a
	// account denied every operator credential cannot buy one back by putting a
	// wider value in its own key metadata.
	governing, err := keyring.ParseStrategy(
		string(requestctx.AccountCredentialStrategyOrDefault(ctx)),
	)
	if err != nil {
		return nil, err
	}
	strategy, err := keyring.EffectiveStrategy(governing, apiKey.Metadata)
	if err != nil {
		return nil, err
	}

	return &proxy.APIKeyRoutingConfig{
		AllowedModels:      append([]string(nil), apiKey.AllowedModels...),
		CredentialStrategy: strategy,
	}, nil
}

// logError logs an error with context
func (h *BaseHandler) logError(ctx context.Context, err error, msg string) {
	event := log.Error().
		Err(err).
		Str("request_id", h.getRequestID(ctx))
	var providerFailure *failure.Failure
	if errors.As(err, &providerFailure) {
		details := providerFailure.ProviderDetails()
		event = event.
			Str("provider", details.Provider).
			Int("provider_status", details.StatusCode).
			Str("provider_message", details.Message)
		if cause := providerFailure.Unwrap(); cause != nil {
			event = event.Str("cause", cause.Error())
		}
	}
	event.Msg(msg)
}

// writeError writes an error response based on the error type
func (h *BaseHandler) writeError(w http.ResponseWriter, err error) {
	status, errorType, message, param := errorShape(err)
	if h.protocol == ProtocolOpenRouter {
		metadata := map[string]any{openRouterErrorTypeField: errorType}
		if param != nil {
			metadata["param"] = *param
		}
		openrouter.WriteError(w, status, message, metadata)
		return
	}
	openai.WriteError(w, status, errorType, message, param)
}

func errorShape(err error) (status int, errorType, message string, param *string) {
	var normalized *failure.Failure
	if errors.As(err, &normalized) {
		status, errorType = normalizedFailureShape(normalized.Kind())
		return status, errorType, normalized.SafeMessage(), nil
	}

	// An unknown preset reference is a caller error against a named gateway
	// resource, so the message keeps the reference it could not resolve.
	if errors.Is(err, proxy.ErrPresetNotFound) {
		return http.StatusNotFound, errorTypeNotFound, err.Error(), nil
	}

	// A file_id this account does not hold is the same caller error, and it
	// reads the same whether the identifier is unknown or belongs elsewhere.
	if errors.Is(err, proxy.ErrStoredFileNotFound) {
		return http.StatusNotFound, errorTypeNotFound, err.Error(), nil
	}

	// A model name the retained catalog generation does not hold is a caller
	// error about a name. It answers 404 rather than the 503 every other
	// routing failure answers, because "try again later" is wrong advice for a
	// model that no wait will produce.
	if errors.Is(err, catalog.ErrModelNotCatalogued) {
		return http.StatusNotFound, errorTypeNotFound, err.Error(), nil
	}

	switch e := err.(type) {
	case *proxy.ValidationError:
		return http.StatusBadRequest, errorTypeInvalidRequest, e.Message, &e.Field

	case *proxy.ProviderError:
		// Map provider errors to appropriate HTTP status codes
		status := http.StatusInternalServerError
		errType := errorTypeServer

		switch e.Code {
		case "rate_limit":
			status = http.StatusTooManyRequests
			errType = errorTypeRateLimit
		case "auth_error":
			status = http.StatusUnauthorized
			errType = "authentication_error"
		case errorTypePermission:
			status = http.StatusForbidden
			errType = errorTypePermission
		case errorCodeNotFound:
			status = http.StatusNotFound
			errType = errorTypeNotFound
		}
		return status, errType, e.Message, nil

	case *proxy.RoutingError:
		return http.StatusServiceUnavailable, errorTypeServiceUnavailable, e.Error(), nil

	default:
		// Handle specific sentinel errors
		switch err {
		case proxy.ErrNoValidModel:
			return http.StatusBadRequest, errorTypeInvalidRequest, "No valid model specified", nil
		case proxy.ErrNoAvailableProvider:
			return http.StatusServiceUnavailable, errorTypeServiceUnavailable, "No available provider for the requested model", nil
		case proxy.ErrRateLimitExceeded:
			return http.StatusTooManyRequests, errorTypeRateLimit, "Rate limit exceeded", nil
		case proxy.ErrInsufficientQuota:
			return http.StatusPaymentRequired, errorTypePermission, "Insufficient quota", nil
		case proxy.ErrStreamingNotSupported:
			return http.StatusBadRequest, errorTypeInvalidRequest, "Streaming not supported for this request", nil
		case proxy.ErrEmbeddingsNotSupported:
			return http.StatusBadRequest, errorTypeInvalidRequest, "Embeddings not supported by this provider", nil
		default:
			return http.StatusInternalServerError, errorTypeServer, "Internal server error", nil
		}
	}
}

func normalizedFailureShape(kind failure.Kind) (int, string) {
	switch kind {
	case failure.Validation:
		return http.StatusBadRequest, errorTypeInvalidRequest
	case failure.Authentication:
		return http.StatusUnauthorized, "authentication_error"
	case failure.Permission:
		return http.StatusForbidden, errorTypePermission
	case failure.RateLimit:
		return http.StatusTooManyRequests, errorTypeRateLimit
	case failure.NotFound:
		return http.StatusNotFound, errorTypeNotFound
	case failure.Timeout:
		return http.StatusGatewayTimeout, errorTypeServiceUnavailable
	case failure.Canceled:
		return http.StatusRequestTimeout, errorTypeInvalidRequest
	case failure.ProviderUnavailable:
		return http.StatusServiceUnavailable, errorTypeServiceUnavailable
	default:
		return http.StatusInternalServerError, errorTypeServer
	}
}
