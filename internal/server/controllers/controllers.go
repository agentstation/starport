package controllers

import (
	"github.com/agentstation/starport/internal/console"
	"github.com/agentstation/starport/internal/identity"
	"github.com/agentstation/starport/internal/providers/byok"
	"github.com/agentstation/starport/internal/proxy"
)

// Controllers holds all HTTP Controllers
type Controllers struct {
	Health               *HealthController
	Chat                 *ChatController
	OpenRouterChat       *ChatController
	Embeddings           *EmbeddingsController
	OpenRouterEmbeddings *EmbeddingsController
	Models               *ModelsController
	OpenRouterModels     *ModelsController
	Providers            *ProvidersController
	ProviderKeys         *ProviderKeysController
	Admin                *AdminController
	ProviderOperations   *ProviderOperationsController
	Console              *console.Handler
}

// Config holds configuration for creating handlers
type Config struct {
	Service            proxy.Proxy
	ProviderKeys       byok.ProviderKeys
	Identities         identity.Repository
	ProviderOperations ProviderOperations
	ServiceName        string
	Version            string
	Console            *console.Handler
}

// NewControllers creates a new controller collection
func NewControllers(cfg Config) *Controllers {
	collections := &Controllers{
		Health:               NewHealthController(cfg.ServiceName, cfg.Version),
		Chat:                 NewChatController(cfg.Service),
		OpenRouterChat:       NewOpenRouterChatController(cfg.Service),
		Embeddings:           NewEmbeddingsController(cfg.Service),
		OpenRouterEmbeddings: NewOpenRouterEmbeddingsController(cfg.Service),
		Models:               NewModelsController(cfg.Service),
		OpenRouterModels:     NewOpenRouterModelsController(cfg.Service),
		Providers:            NewProvidersController(cfg.Service),
		ProviderKeys:         NewProviderKeysController(cfg.ProviderKeys),
		Admin:                NewAdminController(cfg.Identities),
		ProviderOperations:   NewProviderOperationsController(cfg.ProviderOperations),
		Console:              cfg.Console,
	}

	return collections
}
