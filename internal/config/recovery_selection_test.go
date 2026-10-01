package config

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agentstation/starmap/pkg/productfiles"
	"github.com/agentstation/starmap/pkg/productpaths"
	"github.com/agentstation/starport/internal/credentials"
	"github.com/agentstation/starport/internal/kvconnection/kvtest"
	"github.com/agentstation/starport/internal/localauth"
	"github.com/stretchr/testify/require"
)

func recoverySelectionFixture(t *testing.T, primary, dotenv string) *Config {
	t.Helper()
	parent := filepath.Join(t.TempDir(), "selection")
	_, err := productfiles.CreateDirectory(parent)
	require.NoError(t, err)
	paths := PathsForConfigDir(parent)
	if primary != "" {
		require.NoError(t, os.WriteFile(paths.ConfigFile, []byte(primary), 0600))
	}
	loader := NewLoader().WithPaths(paths).WithEnvironment(map[string]string{})
	if dotenv != "" {
		path := filepath.Join(parent, "custom.env")
		require.NoError(t, os.WriteFile(path, []byte(dotenv), 0600))
		loader.WithEnvFiles(path)
	}
	cfg, err := loader.Load(t.Context())
	require.NoError(t, err)
	store, err := localauth.NewStore(paths.LocalTokenFile)
	require.NoError(t, err)
	_, err = store.Rotate(t.Context(), time.Now())
	require.NoError(t, err)
	require.NoError(t, cfg.InitializeInferencePolicy(t.Context(), false))
	return cfg
}

func TestRecoverySelectionBindsLoadedConfiguration(t *testing.T) {
	for _, kind := range []string{"primary", "dotenv"} {
		t.Run(kind, func(t *testing.T) {
			primary, dotenv := "", ""
			if kind == "primary" {
				primary = "STARPORT_SERVER_PORT=7777\n"
			} else {
				dotenv = "STARPORT_SERVER_PORT=7777\n"
			}
			cfg := recoverySelectionFixture(t, primary, dotenv)
			first, err := cfg.InspectRecoverySelection(t.Context())
			require.NoError(t, err)
			require.NotContains(t, string(first.PrivateEvidence()), "STARPORT_SERVER_PORT")
			var file string
			for _, input := range cfg.fileInputs {
				if input.loaded {
					file = input.location.Path
				}
			}
			require.NoError(t, os.WriteFile(file, []byte("STARPORT_SERVER_PORT=7778\n"), 0600))
			_, err = cfg.InspectRecoverySelection(t.Context())
			require.ErrorContains(t, err, "differs from its loaded values")
		})
	}
}

func TestRecoverySelectionRejectsNewDefaultAndMissingPolicy(t *testing.T) {
	cfg := recoverySelectionFixture(t, "", "")
	first, err := cfg.InspectRecoverySelection(t.Context())
	require.NoError(t, err)
	require.Equal(t, 2, first.FileCount())
	require.NoError(t, os.WriteFile(cfg.EffectivePaths().ConfigFile, []byte("STARPORT_SERVER_PORT=7777\n"), 0600))
	_, err = cfg.InspectRecoverySelection(t.Context())
	require.ErrorContains(t, err, "changed after loading")
	require.NoError(t, os.Remove(cfg.EffectivePaths().ConfigFile))
	require.NoError(t, os.Remove(filepath.Join(cfg.InferenceCredentialPolicyDirectory(), "policy.json")))
	_, err = cfg.InspectRecoverySelection(t.Context())
	require.ErrorContains(t, err, "selection policy is absent or invalid")
}

func TestRecoverySelectionBindsCanonicalPrivateValues(t *testing.T) {
	cfg := recoverySelectionFixture(t, "", "")
	before, err := cfg.InspectRecoverySelection(t.Context())
	require.NoError(t, err)
	cfg.Catalog.canonicalValues["STARMAP_SOURCE_TOKEN"] = "private-selection-token"
	after, err := cfg.InspectRecoverySelection(t.Context())
	require.NoError(t, err)
	require.NotEqual(t, before.PrivateEvidence(), after.PrivateEvidence())
	require.NotContains(t, string(after.PrivateEvidence()), "private-selection-token")
	require.NotContains(t, fmt.Sprintf("%#v", after), "private-selection-token")
	cfg.Identity.OAuth.Google.ClientID = "half-configured"
	_, err = cfg.InspectRecoverySelection(t.Context())
	require.ErrorContains(t, err, "identity settings are incomplete")
}

func TestRecoverySelectionChecksOnlySelectedTrust(t *testing.T) {
	cfg := recoverySelectionFixture(t, "", "")
	cfg.Storage.Valkey.CAFile = filepath.Join(cfg.EffectivePaths().ConfigDir, "inactive.pem")
	cfg.Security.TLSCertPath = filepath.Join(cfg.EffectivePaths().ConfigDir, "inactive-cert.pem")
	_, err := cfg.InspectRecoverySelection(t.Context())
	require.NoError(t, err)
	cfg.Cache.Enabled = true
	cfg.Cache.Backend = "valkey"
	cfg.Cache.URL = "valkeys://127.0.0.1:6380"
	cfg.Cache.CAFile = filepath.Join(cfg.EffectivePaths().ConfigDir, "cache.pem")
	require.NoError(t, os.WriteFile(cfg.Cache.CAFile, []byte("private malformed trust"), 0600))
	_, err = cfg.InspectRecoverySelection(t.Context())
	require.Error(t, err)
	require.NotContains(t, err.Error(), "private malformed trust")
	_, ca := kvtest.Certificate(t, false)
	body, err := os.ReadFile(ca)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(cfg.Cache.CAFile, body, 0600))
	_, err = cfg.InspectRecoverySelection(t.Context())
	require.NoError(t, err)
}

func TestRecoverySelectionBindsEncryptionSelection(t *testing.T) {
	cfg := recoverySelectionFixture(t, "", "")
	cfg.Security.MasterKey = strings.Repeat("private-encryption-key", 2)
	before, err := cfg.InspectRecoverySelection(t.Context())
	require.NoError(t, err)
	require.NotContains(t, string(before.PrivateEvidence()), cfg.Security.MasterKey)
	cfg.Security.MasterKey = strings.Repeat("different-encryption-key", 2)
	after, err := cfg.InspectRecoverySelection(t.Context())
	require.NoError(t, err)
	require.NotEqual(t, before.PrivateEvidence(), after.PrivateEvidence())
}

func mustRecoveryPrimaryEntry(t *testing.T, cfg *Config) productpaths.FileEntry {
	t.Helper()
	manifest, err := cfg.FileManifest("")
	require.NoError(t, err)
	for _, entry := range manifest.Files {
		if entry.ID == pathRoleConfiguration {
			return entry
		}
	}
	t.Fatal("primary role absent")
	return productpaths.FileEntry{}
}

type unboundRecoverySource struct{}

func (unboundRecoverySource) ResolveMaterial(context.Context) (credentials.Material, error) {
	panic("passive recovery must not resolve a source")
}

func TestRecoverySelectionRefusesCustomSourceWithoutResolving(t *testing.T) {
	cfg := recoverySelectionFixture(t, "", "")
	cfg.Providers = ProvidersConfig{"openai": ProviderConfig{Timeout: time.Second, MaxConnections: 1, CredentialSource: unboundRecoverySource{}}}
	_, err := cfg.InspectRecoverySelection(t.Context())
	require.ErrorContains(t, err, "owner binding for a custom credential source")
}
