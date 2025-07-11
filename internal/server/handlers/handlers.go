package handlers

import (
	"github.com/agentstation/starport/internal/providers"
	"github.com/agentstation/starport/internal/proxy"
	"github.com/agentstation/starport/internal/storage"
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
}

// Config holds configuration for creating handlers
type Config struct {
	Service     proxy.Service
	KeyManager  providers.KeyManager
	Store       storage.KVStore
	ServiceName string
	Version     string
}

// NewCollection creates a new handler collection
func NewCollection(cfg Config) *Collection {
	return &Collection{
		Health:       NewHealthHandler(cfg.ServiceName, cfg.Version),
		Chat:         NewChatHandler(cfg.Service),
		Embeddings:   NewEmbeddingsHandler(cfg.Service),
		Models:       NewModelsHandler(cfg.Service),
		Providers:    NewProvidersHandler(cfg.Service),
		ProviderKeys: NewProviderKeysHandler(cfg.KeyManager),
		Admin:        NewAdminHandler(cfg.Store),
	}
}
