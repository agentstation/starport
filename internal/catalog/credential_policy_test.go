package catalog

import (
	"os"
	"path/filepath"
	"testing"

	starmaperrors "github.com/agentstation/starmap/pkg/errors"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

func TestAcquisitionPolicyClassifiesBeforeStartupAndRetainsAcceptance(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		name := "fresh"
		if legacy {
			name = "upgrade"
		}
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			settings := Settings{CredentialPolicyDirectory: filepath.Join(root, "policy"), StateDirectory: filepath.Join(root, "runtime"), BaselineDirectory: filepath.Join(root, "baseline")}
			if legacy {
				require.NoError(t, os.Mkdir(settings.BaselineDirectory, 0700))
			}
			values := map[string]string{"STARPORT_CATALOG_TESTPROVIDER_API_KEY": "new", "STARPORT_TESTPROVIDER_API_KEY": "old", "CHOSEN_CATALOG_KEY": "selected"}
			lookup := func(name string) (string, bool) { v, ok := values[name]; return v, ok }
			state, err := settings.credentialPolicy(t.Context(), storage.NewMockStore())
			require.NoError(t, err)
			require.Equal(t, legacy, state.LegacyInstallation)
			resolver := newAcquisitionResolver(t.Context(), lookup, state)
			require.NoError(t, resolver.err)
			provider := acquisitionTestProvider(true)
			_, err = resolver.ResolveCatalog(t.Context(), provider)
			if legacy {
				var conflict *starmaperrors.ConflictError
				require.ErrorAs(t, err, &conflict)
				values["STARPORT_CATALOG_TESTPROVIDER_API_KEY_REFERENCE"] = "env:CHOSEN_CATALOG_KEY"
				_, err = resolver.ResolveCatalog(t.Context(), provider)
				require.NoError(t, err)
				delete(values, "STARPORT_CATALOG_TESTPROVIDER_API_KEY_REFERENCE")
			} else {
				require.NoError(t, err)
			}
			state.LegacyInstallation = true
			values["STARPORT_TESTPROVIDER_API_KEY"] = "different-after-acceptance"
			restarted := newAcquisitionResolver(t.Context(), lookup, state)
			require.NoError(t, restarted.err)
			material, err := restarted.ResolveCatalog(t.Context(), provider)
			require.NoError(t, err)
			value, ok := material.Value("api-key")
			require.True(t, ok)
			require.Equal(t, "new", value)
		})
	}
}

func TestAcquisitionPolicyRejectsInvalidMarkerBeforeCreatingState(t *testing.T) {
	root := t.TempDir()
	baseline := filepath.Join(root, "baseline")
	require.NoError(t, os.WriteFile(baseline, []byte("operator file"), 0600))
	settings := Settings{CredentialPolicyDirectory: filepath.Join(root, "policy"), BaselineDirectory: baseline}
	_, err := settings.credentialPolicy(t.Context(), storage.NewMockStore())
	require.Error(t, err)
	_, err = os.Stat(settings.CredentialPolicyDirectory)
	require.True(t, os.IsNotExist(err))
	data, err := os.ReadFile(baseline)
	require.NoError(t, err)
	require.Equal(t, "operator file", string(data))
}

func TestAcquisitionReferenceFailureDoesNotUseAmbientKey(t *testing.T) {
	values := map[string]string{"STARPORT_CATALOG_TESTPROVIDER_API_KEY_REFERENCE": "env:MISSING_CATALOG_KEY", "TESTPROVIDER_API_KEY": "ambient"}
	resolver := NewAcquisitionResolver(func(name string) (string, bool) { v, ok := values[name]; return v, ok })
	_, err := resolver.ResolveCatalog(t.Context(), acquisitionTestProvider(true))
	require.Error(t, err)
	values["STARPORT_CATALOG_TESTPROVIDER_API_KEY_REFERENCE_FALLBACK_AMBIENT"] = "true"
	material, err := resolver.ResolveCatalog(t.Context(), acquisitionTestProvider(true))
	require.NoError(t, err)
	value, ok := material.Value("api-key")
	require.True(t, ok)
	require.Equal(t, "ambient", value)
}

func TestAcquisitionRoleReferenceReadsPrivateFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "provider-key")
	require.NoError(t, os.WriteFile(path, []byte("fixture-file-key"), 0600))
	resolver := NewAcquisitionResolver(func(name string) (string, bool) {
		if name == "STARPORT_CATALOG_TESTPROVIDER_API_KEY_REFERENCE" {
			return "file:" + path, true
		}
		return "", false
	})
	material, err := resolver.ResolveCatalog(t.Context(), acquisitionTestProvider(true))
	require.NoError(t, err)
	value, ok := material.Value("api-key")
	require.True(t, ok)
	require.Equal(t, "fixture-file-key", value)
}
