package controllers

import (
	"github.com/agentstation/starport/internal/account"
	"github.com/agentstation/starport/internal/apikey"
	"github.com/agentstation/starport/internal/authmode"
	"github.com/agentstation/starport/internal/console"
	"github.com/agentstation/starport/internal/files"
	"github.com/agentstation/starport/internal/identity"
	"github.com/agentstation/starport/internal/jobs"
	"github.com/agentstation/starport/internal/localauth"
	"github.com/agentstation/starport/internal/presets"
	"github.com/agentstation/starport/internal/providers/keyring"
	"github.com/agentstation/starport/internal/proxy"
	"github.com/agentstation/starport/internal/usage"
)

// Controllers holds all HTTP Controllers
type Controllers struct {
	Health               *HealthController
	Chat                 *ChatController
	OpenRouterChat       *ChatController
	Embeddings           *EmbeddingsController
	OpenRouterEmbeddings *EmbeddingsController
	Rerank               *RerankController
	OpenRouterRerank     *RerankController
	Media                *MediaController
	OpenRouterMedia      *MediaController
	Models               *ModelsController
	OpenRouterModels     *ModelsController
	Providers            *ProvidersController
	Authors              *AuthorsController
	Logos                *LogosController
	ProviderCredentials  *ProviderCredentialsController
	Activity             *ActivityController
	Audit                *AuditController
	Admin                *AdminController
	Accounts             *AccountsController
	AccountTemplates     *AccountTemplatesController
	Members              *MembersController
	ProviderOperations   *ProviderOperationsController
	Catalog              *CatalogController
	Files                *FilesController
	Videos               *VideosController
	OpenRouterVideos     *VideosController
	Presets              *PresetsController
	Auth                 *AuthController
	Launch               *LaunchController
	ConsoleSession       *ConsoleSessionController
	ConsoleIdentity      *ConsoleIdentityController
	Console              console.PageServer
}

// Config holds configuration for creating handlers
type Config struct {
	Service            proxy.Proxy
	ProviderKeys       keyring.ProviderKeys
	APIKeys            apikey.Repository
	Accounts           account.Repository
	Usage              usage.Repository
	ProviderOperations ProviderOperations
	Catalog            CatalogOperations
	Presets            presets.Repository
	// Templates serves the account-template surface. A nil repository
	// degrades those routes to 503, the way an absent preset store does.
	Templates account.TemplateRepository
	// Files serves the stored file surface. A nil service leaves the routes
	// registered and answers each one with a service-unavailable result, so a
	// deployment that configured no file storage says so instead of 404.
	Files           *files.Service
	FileUploadBound int64
	// Jobs serves work that outlives its request. A nil service leaves the
	// video routes registered and answers each one with a service-unavailable
	// result, the same way an unconfigured file store answers.
	Jobs *jobs.Service
	// FileBackend names the blob backend stored file bytes land in. It reaches
	// the admin surface rather than the file routes, because it describes the
	// deployment and not any one file.
	FileBackend string
	ServiceName string
	Version     string
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
	// LocalGate redeems console launch tickets. A nil gate refuses every
	// launch, which is what a gateway with no local admin token should do.
	LocalGate *localauth.Gate
	// IdentityAuth is the OAuth acquisition path, or nil on a deployment
	// with no identity provider configured. Nil keeps the identity routes
	// mounted and refusing with the operator's answer.
	IdentityAuth IdentityAuthenticator
	// Identity holds the durable people plane: users, teams, memberships,
	// and account grants. Zero repositories degrade the members routes to
	// 503, the way an absent template store degrades its surface.
	Identity identity.Repositories
	// Audit records admin mutations and serves the trail back. A nil trail
	// records nothing and degrades the listing route to 503, the way an
	// absent preset store degrades its surface.
	Audit AuditTrail
	// Events pushes key lifecycle events to the configured webhook
	// endpoints. A nil emitter pushes nothing.
	Events EventEmitter
}

// NewControllers creates a new controller collection
func NewControllers(cfg Config) *Controllers {
	collections := &Controllers{
		Health:               NewHealthController(cfg.ServiceName, cfg.Version),
		Chat:                 NewChatController(cfg.Service),
		OpenRouterChat:       NewOpenRouterChatController(cfg.Service),
		Embeddings:           NewEmbeddingsController(cfg.Service),
		OpenRouterEmbeddings: NewOpenRouterEmbeddingsController(cfg.Service),
		Rerank:               NewRerankController(cfg.Service),
		OpenRouterRerank:     NewOpenRouterRerankController(cfg.Service),
		Media:                NewMediaController(cfg.Service),
		OpenRouterMedia:      NewOpenRouterMediaController(cfg.Service),
		Models:               NewModelsController(cfg.Service),
		OpenRouterModels:     NewOpenRouterModelsController(cfg.Service),
		Providers:            NewProvidersController(cfg.Service),
		Authors:              NewAuthorsController(cfg.Service),
		Logos:                NewLogosController(cfg.Service),
		ProviderCredentials:  NewProviderCredentialsController(cfg.ProviderKeys, cfg.Accounts),
		Activity:             NewActivityController(cfg.Usage),
		Audit:                NewAuditController(cfg.Audit),
		Admin: NewAdminController(cfg.APIKeys, cfg.Accounts, cfg.Usage,
			WithFileStorage(cfg.FileBackend)),
		Accounts:           NewAccountsController(cfg.Accounts, cfg.APIKeys, cfg.Templates),
		AccountTemplates:   NewAccountTemplatesController(cfg.Templates),
		Members:            NewMembersController(cfg.Identity),
		ProviderOperations: NewProviderOperationsController(cfg.ProviderOperations),
		Catalog:            NewCatalogController(cfg.Catalog),
		Files:              NewFilesController(cfg.Files, cfg.FileUploadBound),
		Videos:             NewVideosController(cfg.Service, cfg.Jobs),
		OpenRouterVideos:   NewOpenRouterVideosController(cfg.Service, cfg.Jobs),
		Presets:            NewPresetsController(cfg.Presets),
		Auth:               NewAuthController(cfg.AuthPolicy, cfg.AuthModeStore, cfg.AuthModeBindHost, cfg.AllowRemoteNoAuth),
		Launch:             NewLaunchController(cfg.LocalGate),
		ConsoleSession:     NewConsoleSessionController(cfg.LocalGate),
		ConsoleIdentity:    NewConsoleIdentityController(cfg.IdentityAuth, cfg.LocalGate),
		Console:            cfg.Console,
	}

	// The recorder rides a package-private field instead of each constructor,
	// because every mutating controller shares the one trail and a nil trail
	// simply records nothing.
	collections.Admin.audit = cfg.Audit
	collections.Admin.events = cfg.Events
	collections.Accounts.audit = cfg.Audit
	collections.AccountTemplates.audit = cfg.Audit
	collections.Members.audit = cfg.Audit
	collections.ProviderCredentials.audit = cfg.Audit
	collections.Presets.audit = cfg.Audit
	collections.Auth.audit = cfg.Audit

	return collections
}
