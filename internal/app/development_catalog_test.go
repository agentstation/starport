package app

import (
	"context"
	"errors"
	"os"
	"testing"

	runtimecatalog "github.com/agentstation/starport/internal/catalog"
	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

func TestDevLetsStarmapCreateCatalogState(t *testing.T) {
	cfg := validProductionConfig(t)
	cfg.Providers = config.ProvidersConfig{}
	factoryReached := false
	development, err := NewDevelopment(t.Context(), cfg, func(options *buildOptions) {
		openCatalog := options.factories.openCatalog
		options.factories.openCatalog = func(ctx context.Context, store storage.KVStore, settings runtimecatalog.Settings, lookup runtimecatalog.DeploymentLookup) (catalogRuntime, error) {
			factoryReached = true
			if _, err := os.Stat(settings.StateDirectory); !errors.Is(err, os.ErrNotExist) {
				return nil, errors.New("development must let Starmap create its catalog state directory")
			}
			return openCatalog(ctx, store, settings, lookup)
		}
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, development.Close(context.Background())) })
	require.True(t, factoryReached)
	require.DirExists(t, cfg.Catalog.StateDirectory)
}
