package config

import (
	"sync"
	"testing"

	starmap "github.com/agentstation/starmap"
	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/stretchr/testify/require"
)

// The catalog is immutable. Provider reads return independent values for each test.
var inferenceOriginBaseline = sync.OnceValues(func() (*catalogs.Catalog, error) {
	builder, err := starmap.EmbeddedBuilder()
	if err != nil {
		return nil, err
	}
	return builder.Build()
})

func inferenceOriginCatalog(t *testing.T) *catalogs.Catalog {
	t.Helper()
	catalog, err := inferenceOriginBaseline()
	require.NoError(t, err)
	return catalog
}

func TestInferenceBaseURLSettingReplacesCatalogOrigin(t *testing.T) {
	catalog := inferenceOriginCatalog(t)
	for _, test := range []struct {
		value string
		want  string
	}{
		{value: "", want: ""},
		{value: "https://relay.example", want: "https://relay.example"},
		{value: "https://relay.example/", want: "https://relay.example"},
		{value: "https://relay.example/openai/", want: "https://relay.example/openai"},
		{value: "http://localhost:8089", want: "http://localhost:8089"},
		{value: "http://127.0.0.1:8089/relay", want: "http://127.0.0.1:8089/relay"},
		{value: "http://10.0.0.5", want: "http://10.0.0.5"},
		{value: "http://192.168.1.20:8080", want: "http://192.168.1.20:8080"},
		{value: "http://169.254.10.10", want: "http://169.254.10.10"},
		{value: "http://[::1]:8089", want: "http://[::1]:8089"},
		{value: "http://[fd00::1]", want: "http://[fd00::1]"},
	} {
		name := test.value
		if name == "" {
			name = "absent"
		}
		t.Run(name, func(t *testing.T) {
			values := map[string]string{"OPENAI_API_KEY": "sk-test-key"}
			if test.value != "" {
				values["STARPORT_OPENAI_INFERENCE_BASE_URL"] = test.value
			}
			cfg := &Config{providerEnvironment: mapEnvironmentLookup(values)}
			resolved, failures, err := cfg.ResolveProviderSetLocalIsolated(t.Context(), catalog.Providers(), nil)
			require.NoError(t, err)
			require.Empty(t, failures)
			require.Equal(t, test.want, resolved[catalogs.ProviderIDOpenAI].BaseURL)
			require.Equal(t, test.want, resolved[catalogs.ProviderIDOpenAI].InferenceOrigin)
			value, _ := resolved[catalogs.ProviderIDOpenAI].Material.Value("api-key")
			require.Equal(t, "sk-test-key", value)
			require.Len(t, resolved, 1)
		})
	}
}

func TestInferenceBaseURLSettingRefusesUnsafeOrigins(t *testing.T) {
	catalog := inferenceOriginCatalog(t)
	for _, test := range []struct {
		value  string
		reason string
	}{
		{value: "relay.example/v1", reason: "is not an absolute URL"},
		{value: "mailto:operator@relay.example", reason: "is not an absolute URL"},
		{value: "https://%zz", reason: "is not an absolute URL"},
		{value: "https://{host}.relay.example", reason: "contains a template variable"},
		{value: "https://operator@relay.example", reason: "contains user information"},
		{value: "https://relay.example?region=1", reason: "contains a query"},
		{value: "https://relay.example?", reason: "contains a query"},
		{value: "https://relay.example#section", reason: "contains a fragment"},
		{value: "https://relay.example#", reason: "contains a fragment"},
		{value: "https:///v1", reason: "has no host"},
		{value: "http://relay.example", reason: "uses http for a host that is not local or private"},
		{value: "http://service.localhost", reason: "uses http for a host that is not local or private"},
		{value: "http://192.0.2.10", reason: "uses http for a host that is not local or private"},
		{value: "ftp://relay.example", reason: "must use https, or http for a local or private host"},
	} {
		t.Run(test.value, func(t *testing.T) {
			for _, values := range []map[string]string{
				{"OPENAI_API_KEY": "sk-test-key", "STARPORT_OPENAI_INFERENCE_BASE_URL": test.value},
				// A refused origin fails startup even before any material exists.
				{"STARPORT_OPENAI_INFERENCE_BASE_URL": test.value},
			} {
				cfg := &Config{providerEnvironment: mapEnvironmentLookup(values)}
				resolved, failures, err := cfg.ResolveProviderSetLocalIsolated(t.Context(), catalog.Providers(), nil)
				require.ErrorIs(t, err, ErrInferenceBaseURLRefused)
				require.Nil(t, resolved)
				require.Nil(t, failures)
				require.ErrorContains(t, err, "provider openai")
				require.ErrorContains(t, err, "STARPORT_OPENAI_INFERENCE_BASE_URL "+test.reason)
				require.NotContains(t, err.Error(), test.value)

				cfg = &Config{providerEnvironment: mapEnvironmentLookup(values)}
				require.ErrorIs(t, cfg.ResolveProviders(t.Context(), catalog.Providers()), ErrInferenceBaseURLRefused)
			}
		})
	}
}

func TestInferenceBaseURLSettingRefusesProvidersWithoutOneOrigin(t *testing.T) {
	t.Run("parameterized provider", func(t *testing.T) {
		catalog := inferenceOriginCatalog(t)
		provider, err := catalog.Provider("ollama")
		require.NoError(t, err)
		require.True(t, parameterizedInference(provider))
		cfg := &Config{providerEnvironment: mapEnvironmentLookup(map[string]string{
			"STARPORT_OLLAMA_INFERENCE_BASE_URL": "http://127.0.0.1:11434",
		})}
		_, _, err = cfg.ResolveProviderSetLocalIsolated(t.Context(), catalog.Providers(), nil)
		require.ErrorIs(t, err, ErrInferenceBaseURLRefused)
		require.ErrorContains(t, err, "STARPORT_OLLAMA_INFERENCE_BASE_URL names a provider whose endpoints use catalog bindings")
	})
	t.Run("provider without inference", func(t *testing.T) {
		provider := testCredentialProvider("relay", "RELAY_API_KEY", "")
		providers := catalogs.NewProviders()
		require.NoError(t, providers.Set(provider.ID, &provider))
		cfg := &Config{providerEnvironment: mapEnvironmentLookup(map[string]string{
			"RELAY_API_KEY":                     "relay-key",
			"STARPORT_RELAY_INFERENCE_BASE_URL": "https://relay.example",
		})}
		_, _, err := cfg.ResolveProviderSetLocalIsolated(t.Context(), providers, nil)
		require.ErrorIs(t, err, ErrInferenceBaseURLRefused)
		require.ErrorContains(t, err, "STARPORT_RELAY_INFERENCE_BASE_URL names a provider without an inference service")
	})
}

func TestExplicitProviderBaseURLPrecedesInferenceBaseURLSetting(t *testing.T) {
	catalog := inferenceOriginCatalog(t)
	cfg := &Config{providerEnvironment: mapEnvironmentLookup(map[string]string{
		"OPENAI_API_KEY":                     "sk-test-key",
		"STARPORT_OPENAI_INFERENCE_BASE_URL": "https://relay.example",
	})}
	resolved, err := cfg.ResolveProviderSet(t.Context(), catalog.Providers(), ProvidersConfig{
		catalogs.ProviderIDOpenAI: {BaseURL: "https://explicit.example"},
	})
	require.NoError(t, err)
	require.Equal(t, "https://explicit.example", resolved[catalogs.ProviderIDOpenAI].BaseURL)
	require.Empty(t, resolved[catalogs.ProviderIDOpenAI].InferenceOrigin)
}

func TestInferenceBaseURLSettingClaimsItsName(t *testing.T) {
	t.Run("embedded catalog has no collision", func(t *testing.T) {
		providers := inferenceOriginCatalog(t).Providers().List()
		for _, allowStarmap := range []bool{false, true} {
			require.NoError(t, validateCredentialAliases(providers, allowStarmap))
		}
	})
	t.Run("credential alias collides before reads", func(t *testing.T) {
		first := testCredentialProvider("one", "STARPORT_TWO_INFERENCE_BASE_URL", "")
		second := testCredentialProvider("two", "TWO_API_KEY", "")
		providers := catalogs.NewProviders()
		for _, provider := range []catalogs.Provider{first, second} {
			require.NoError(t, providers.Set(provider.ID, &provider))
		}
		reads := 0
		cfg := &Config{providerEnvironment: lookupFunc(func(string) (string, bool) {
			reads++
			return "must-not-be-read", true
		})}
		err := cfg.ResolveProviders(t.Context(), providers)
		require.ErrorIs(t, err, ErrCredentialAliasCollision)
		require.ErrorContains(t, err, "STARPORT_TWO_INFERENCE_BASE_URL")
		require.Zero(t, reads)
	})
}

func TestRecoverySelectionBindsInferenceBaseURLSetting(t *testing.T) {
	catalog := inferenceOriginCatalog(t)
	cfg := recoverySelectionFixture(t, "", "OPENAI_API_KEY=sk-test-key\n")
	// The loaded resolver reads the key from the dotenv file. The wrapper
	// adds the origin setting without a change to any bound file.
	loaded := cfg.providerEnvironment
	values := map[string]string{}
	cfg.providerEnvironment = lookupFunc(func(name string) (string, bool) {
		if value, found := values[name]; found {
			return value, true
		}
		return loaded.Lookup(name)
	})
	evidence := func() string {
		t.Helper()
		resolved, failures, err := cfg.ResolveProviderSetLocalIsolated(t.Context(), catalog.Providers(), nil)
		require.NoError(t, err)
		require.Empty(t, failures)
		require.Contains(t, resolved, catalogs.ProviderIDOpenAI)
		cfg.Providers = resolved
		selection, err := cfg.InspectRecoverySelection(t.Context())
		require.NoError(t, err)
		return string(selection.PrivateEvidence())
	}
	catalogOrigin := evidence()
	require.Equal(t, catalogOrigin, evidence())
	values["STARPORT_OPENAI_INFERENCE_BASE_URL"] = "https://relay.example"
	approvedOrigin := evidence()
	require.NotEqual(t, catalogOrigin, approvedOrigin)
	require.NotContains(t, approvedOrigin, "relay.example")
}
