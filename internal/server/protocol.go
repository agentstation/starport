package server

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/agentstation/starport/internal/protocol/openai"
	"github.com/agentstation/starport/internal/protocol/openrouter"
)

type protocolContextKey struct{}

type wireProtocol string

const (
	openAIProtocol     wireProtocol = "openai"
	openRouterProtocol wireProtocol = "openrouter"

	// openRouterPathPrefix is the route prefix the OpenRouter-compatible
	// surface is mounted under.
	openRouterPathPrefix = "/api/v1/"
)

func selectProtocol(protocol wireProtocol) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := context.WithValue(r.Context(), protocolContextKey{}, protocol)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func writeProtocolError(w http.ResponseWriter, r *http.Request, status int, errorType, message string) {
	if requestProtocol(r) == openRouterProtocol {
		openrouter.WriteError(w, status, message, map[string]any{"error_type": errorType})
		return
	}
	openai.WriteError(w, status, errorType, message, nil)
}

// requestProtocol names the wire protocol one request answers in. The route
// group states it, but middleware that refuses a request runs before the
// route is chosen, so the path answers when the context has not been set.
func requestProtocol(r *http.Request) wireProtocol {
	if protocol, ok := r.Context().Value(protocolContextKey{}).(wireProtocol); ok {
		return protocol
	}
	if strings.HasPrefix(r.URL.Path, openRouterPathPrefix) {
		return openRouterProtocol
	}
	return openAIProtocol
}

// writeRequestTooLarge refuses a body above the limit. The message states both
// numbers: a caller who only learns that the body was too large has to guess
// how much to cut, and the guess is the difference between one attachment that
// works and one that does not.
func writeRequestTooLarge(w http.ResponseWriter, r *http.Request, limit, received int64) {
	message := fmt.Sprintf(
		"Request body is %d bytes, above the %d byte limit", received, limit)
	writeProtocolError(w, r, http.StatusRequestEntityTooLarge, "invalid_request_error", message)
}
