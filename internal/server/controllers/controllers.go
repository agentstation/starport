package controllers

import (
	"github.com/agentstation/starport/internal/authmode"
	"github.com/agentstation/starport/internal/console"
	"github.com/agentstation/starport/internal/identity"
	"github.com/agentstation/starport/internal/presets"
	"github.com/agentstation/starport/internal/providers/keyring"
	"github.com/agentstation/starport/internal/proxy"
	"github.com/agentstation/starport/internal/tenant"
	"github.com/agentstation/starport/internal/usage"
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
	Authors              *AuthorsController
	Logos                *LogosController
	ProviderCredentials  *ProviderCredentialsController
	Activity             *ActivityController
	Admin                *AdminController
	Tenants              *TenantsController
	ProviderOperations   *ProviderOperationsController
	Catalog              *CatalogController
	Presets              *PresetsController
	Auth                 *AuthController
	Console              console.PageServer
}

// Config holds configuration for creating handlers
type Config struct {
	Service            proxy.Proxy
	ProviderKeys       keyring.ProviderKeys
	Identities         identity.Repository
	Tenants            tenant.Repository
	Usage              usage.Repository
	ProviderOperations ProviderOperations
	Catalog            CatalogOperations
	Presets            presets.Repository
	ServiceName        string
	Version            string
	// AuthPolicy is the running authentication mode. It is a pointer to the
	// live policy and not a copy of the mode, because the console can change
	// the mode while the router stands.
	AuthPolicy *authmode.Policy
	// AuthModeStore persists a console change so it outlives the process. A nil
	// store leaves the mode readable and refuses to change it.
	AuthModeStore authmode.Repository
	// AuthModeBindHost and AllowRemoteNoAuth are the two values the exposure
	// tripwire reads. They travel together because either alone answers the
	// wrong question.
	AuthModeBindHost  string
	AllowRemoteNoAuth bool
	Console           console.PageServer
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
		Authors:              NewAuthorsController(cfg.Service),
		Logos:                NewLogosController(cfg.Service),
		ProviderCredentials:  NewProviderCredentialsController(cfg.ProviderKeys),
		Activity:             NewActivityController(cfg.Usage),
		Admin:                NewAdminController(cfg.Identities, cfg.Tenants, cfg.Usage),
		Tenants:              NewTenantsController(cfg.Tenants, cfg.Identities),
		ProviderOperations:   NewProviderOperationsController(cfg.ProviderOperations),
		Catalog:              NewCatalogController(cfg.Catalog),
		Presets:              NewPresetsController(cfg.Presets),
		Auth:                 NewAuthController(cfg.AuthPolicy, cfg.AuthModeStore, cfg.AuthModeBindHost, cfg.AllowRemoteNoAuth),
		Console:              cfg.Console,
	}

	return collections
}
