package server

import "github.com/go-chi/chi/v5"

// ProxyHandlerInterface defines the interface for proxy handlers
type ProxyHandlerInterface interface {
	// RegisterRoutes adds OpenAI-compatible routes to the router
	RegisterRoutes(r chi.Router)
	// RegisterOpenRouterRoutes adds OpenRouter-compatible routes under /api/v1
	RegisterOpenRouterRoutes(r chi.Router)
}