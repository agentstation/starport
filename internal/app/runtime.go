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
	openBlob     func(context.Context, config.FilesConfig) (blob.Store, error)
	openCatalog  func(context.Context, storage.KVStore, config.CatalogConfig) (catalogRuntime, error)
	newConnector func(string, []catalogs.EndpointType, connectors.ProviderConfig) (connectors.Connector, error)
	newCache     func(cache.ManagerConfig, storage.KVStore) (*cache.Manager, error)
	newServer    func(*server.Config, server.Dependencies) (httpRuntime, error)
}

type buildOptions struct {
	factories runtimeFactories
}

// Option changes runtime factories for explicit test composition.
type Option func(*buildOptions)
