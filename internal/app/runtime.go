package app

import (
	"context"
	"time"

	"github.com/agentstation/starmap"
	"github.com/agentstation/starmap/pkg/catalogs"

	"github.com/agentstation/starport/internal/blob"
	"github.com/agentstation/starport/internal/cache"
	runtimecatalog "github.com/agentstation/starport/internal/catalog"
	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/providers/connectors"
	"github.com/agentstation/starport/internal/server"
	"github.com/agentstation/starport/internal/server/controllers"
	"github.com/agentstation/starport/internal/sqlstore"
	"github.com/agentstation/starport/internal/storage"
)

type httpRuntime interface {
	Start() error
	Shutdown(context.Context) error
}

type catalogRuntime interface {
	ControlPlane() *runtimecatalog.ControlPlane
	RefreshCandidate(context.Context, time.Duration) (starmap.CatalogState, error)
}

type catalogUpdateRuntime interface {
	Start(context.Context) error
	CurrentCandidate() starmap.CatalogState
	Updates() <-chan starmap.CatalogState
	Accept(context.Context, starmap.CatalogState) error
	Close(context.Context) error
}

type runtimeFactories struct {
	openStorage  func(config.StorageConfig) (storage.KVStore, error)
	openSQL      func(config.StorageConfig) (*sqlstore.DB, error)
	openBlob     func(context.Context, config.FilesConfig) (blob.Store, error)
	openCatalog  func(context.Context, storage.KVStore, config.CatalogConfig) (catalogRuntime, error)
	newConnector func(string, []catalogs.EndpointType, connectors.ProviderConfig) (connectors.Connector, error)
	newCache     func(cache.ManagerConfig, storage.KVStore) (*cache.Manager, error)
	newServer    func(*server.Config, server.Dependencies) (httpRuntime, error)
}

type buildOptions struct {
	factories runtimeFactories
	build     controllers.BuildInfo
}

// Option changes runtime factories for explicit test composition.
type Option func(*buildOptions)

// WithBuildInfo states the version, commit, and build time the linker
// stamped into this binary, so the health and admin surfaces report the
// binary that answers. A runtime composed without it reports an unstamped
// build; the start time comes from the clock either way.
func WithBuildInfo(version, commit, buildTime string) Option {
	return func(options *buildOptions) {
		options.build.Version = version
		options.build.Commit = commit
		options.build.BuildTime = buildTime
	}
}
