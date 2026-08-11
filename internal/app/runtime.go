package app

import (
	"context"
	"time"

	"github.com/agentstation/starmap/pkg/catalogs"
	pkgsync "github.com/agentstation/starmap/pkg/sync"

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

type hotReloadRuntime interface {
	Start(context.Context) error
	Stop()
}

type catalogRuntime interface {
	ControlPlane() *runtimecatalog.ControlPlane
	Refresh(context.Context, ...pkgsync.Option) (*pkgsync.Result, error)
}

type runtimeFactories struct {
	openStorage  func(config.StorageConfig) (storage.KVStore, error)
	openCatalog  func(context.Context, storage.KVStore, string) (catalogRuntime, error)
	newConnector func(string, []catalogs.EndpointType, connectors.ProviderConfig) (connectors.Connector, error)
	newCache     func(cache.ManagerConfig, storage.KVStore) (*cache.Manager, error)
	newHotReload func(string, time.Duration) (hotReloadRuntime, error)
	newServer    func(*server.Config, server.Dependencies) (httpRuntime, error)
}

type buildOptions struct{ factories runtimeFactories }

// Option changes runtime factories for explicit test composition.
type Option func(*buildOptions)
