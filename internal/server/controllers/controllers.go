package controllers

import (
	"github.com/agentstation/starport/internal/chatui"
	"github.com/agentstation/starport/internal/providers/byok"
	"github.com/agentstation/starport/internal/proxy"
	"github.com/agentstation/starport/internal/storage"
	"github.com/rs/zerolog"
)

// Controllers holds all HTTP Controllers
type Controllers struct {
	Health       *HealthController
	Chat         *ChatController
	Embeddings   *EmbeddingsController
	Models       *ModelsController
	Providers    *ProvidersController
	ProviderKeys *ProviderKeysController
	Admin        *AdminController
	ChatUI       *chatui.Handler
}

// Config holds configuration for creating handlers
type Config struct {
	Service       proxy.Proxy
	ProviderKeys  byok.ProviderKeys
	Store         storage.KVStore
	ServiceName   string
	Version       string
	ChatUIConfig  chatui.Config
	ChatUIEnabled bool
	Logger        *zerolog.Logger
}

// NewControllers creates a new controller collection
func NewControllers(cfg Config) *Controllers {
	collections := &Controllers{
		Health:       NewHealthController(cfg.ServiceName, cfg.Version),
		Chat:         NewChatController(cfg.Service),
		Embeddings:   NewEmbeddingsController(cfg.Service),
		Models:       NewModelsController(cfg.Service),
		Providers:    NewProvidersController(cfg.Service),
		ProviderKeys: NewProviderKeysController(cfg.ProviderKeys),
		Admin:        NewAdminController(cfg.Store),
	}

	// Initialize ChatUI handler if enabled
	if cfg.ChatUIEnabled && cfg.Logger != nil {
		// Add store to ChatUI config
		chatUIConfig := cfg.ChatUIConfig
		chatUIConfig.Store = cfg.Store

		handler, err := chatui.NewHandler(cfg.Logger, chatUIConfig)
		if err != nil {
			cfg.Logger.Error().Err(err).Msg("failed to initialize ChatUI handler")
		} else {
			collections.ChatUI = handler
		}
	}

	return collections
}
