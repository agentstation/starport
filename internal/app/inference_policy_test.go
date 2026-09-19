package app

import (
	"context"
	"encoding/json/v2"
	"os"
	"path/filepath"
	"strings"
	"testing"

	runtimecatalog "github.com/agentstation/starport/internal/catalog"
	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/credentials"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

func TestInferencePolicyIsRecordedBeforeCatalogStartup(t *testing.T) {
	for _, retained := range []bool{false, true} {
		name := "fresh"
		if retained {
			name = "retained"
		}
		t.Run(name, func(t *testing.T) {
			cfg, err := config.NewLoader().WithEnvironment(map[string]string{
				"STARPORT_HOME":                        filepath.Join(t.TempDir(), "gateway"),
				"STARPORT_SECURITY_MASTER_KEY":         strings.Repeat("m", 32),
				"STARPORT_CATALOG_NETWORK_MODE":        "offline",
				"STARPORT_CATALOG_ACQUISITION_ENABLED": "false",
			}).WithEnvFiles().Load(t.Context())
			require.NoError(t, err)
			if retained {
				require.NoError(t, os.MkdirAll(cfg.EffectivePaths().BaselineDir, 0700))
			}
			factories := explicitTestFactories()
			openCatalog := factories.openCatalog
			observed := false
			factories.openCatalog = func(ctx context.Context, store storage.KVStore, settings runtimecatalog.Settings, lookup runtimecatalog.DeploymentLookup) (catalogRuntime, error) {
				data, err := os.ReadFile(filepath.Join(cfg.InferenceCredentialPolicyDirectory(), "policy.json"))
				require.NoError(t, err)
				var record struct {
					Policy credentials.EnvironmentPolicy `json:"policy"`
				}
				require.NoError(t, json.Unmarshal(data, &record))
				want := credentials.InferencePolicyCurrent
				if retained {
					want = credentials.InferencePolicyLegacy
				}
				require.Equal(t, want, record.Policy)
				observed = true
				return openCatalog(ctx, store, settings, lookup)
			}
			application, err := New(cfg, withRuntimeFactories(factories))
			require.NoError(t, err)
			require.True(t, observed)
			require.NoError(t, application.Close(t.Context()))
		})
	}
}
