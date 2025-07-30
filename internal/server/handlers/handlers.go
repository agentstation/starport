package handlers

import (
	"github.com/agentstation/starport/internal/chatui"
	"github.com/agentstation/starport/internal/providers"
	"github.com/agentstation/starport/internal/proxy"
	"github.com/agentstation/starport/internal/storage"
	"github.com/rs/zerolog"
)

// Collection holds all HTTP handlers
type Collection struct {
	Health       *HealthHandler
	Chat         *ChatHandler
	Embeddings   *EmbeddingsHandler
	Models       *ModelsHandler
	Providers    *ProvidersHandler
	ProviderKeys *ProviderKeysHandler
	Admin        *AdminHandler
	ChatUI       *chatui.Handler
}

// Config holds configuration for creating handlers
type Config struct {
	Service       proxy.Proxy
	KeyManager    providers.KeyManager
	Store         storage.KVStore
	ServiceName   string
	Version       string
	ChatUIConfig  chatui.Config
	ChatUIEnabled bool
	Logger        *zerolog.Logger
}

// NewCollection creates a new handler collection
func NewCollection(cfg Config) *Collection {
	collection := &Collection{
		Health:       NewHealthHandler(cfg.ServiceName, cfg.Version),
		Chat:         NewChatHandler(cfg.Service),
		Embeddings:   NewEmbeddingsHandler(cfg.Service),
		Models:       NewModelsHandler(cfg.Service),
		Providers:    NewProvidersHandler(cfg.Service),
		ProviderKeys: NewProviderKeysHandler(cfg.KeyManager),
		Admin:        NewAdminHandler(cfg.Store),
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
			collection.ChatUI = handler
		}
	}

	return collection
}
