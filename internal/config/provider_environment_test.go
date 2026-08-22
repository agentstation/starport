package config

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	starmap "github.com/agentstation/starmap"
	"github.com/agentstation/starmap/pkg/catalogs"

	"github.com/agentstation/starport/internal/credentials"
)

func TestCatalogCredentialEnvironmentPrecedence(t *testing.T) {
	provider := testCredentialProvider("openai", "OPENAI_API_KEY", `^valid-(conventional|product)$`)
	providers := catalogs.NewProviders()
	if err := providers.Set(provider.ID, &provider); err != nil {
		t.Fatal(err)
	}

	for _, test := range []struct {
		name   string
		values map[string]string
		want   string
	}{
		{
			name: "conventional precedes product alias",
			values: map[string]string{
				"OPENAI_API_KEY":          "valid-conventional",
				"STARPORT_OPENAI_API_KEY": "valid-product",
			},
			want: "valid-conventional",
		},
		{
			name: "product alias is the final ambient candidate",
			values: map[string]string{
				"STARPORT_OPENAI_API_KEY": "valid-product",
			},
			want: "valid-product",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			cfg := &Config{providerEnvironment: mapEnvironmentLookup(test.values)}
			if err := cfg.ResolveProviders(context.Background(), providers); err != nil {
				t.Fatalf("ResolveProviders: %v", err)
			}
			got, _ := cfg.Providers[provider.ID].Material.Value("api-key")
			if got != test.want {
				t.Fatalf("API key = %q, want selected source", got)
			}
		})
	}

	t.Run("invalid selected value is terminal", func(t *testing.T) {
		lookups := make([]string, 0, 2)
		cfg := &Config{providerEnvironment: lookupFunc(func(name string) (string, bool) {
			lookups = append(lookups, name)
			values := map[string]string{
				"OPENAI_API_KEY":          "invalid",
				"STARPORT_OPENAI_API_KEY": "valid-product",
			}
			value, found := values[name]
			return value, found
		})}
		err := cfg.ResolveProviders(context.Background(), providers)
		var selectedErr *credentials.SelectedValueError
		if !errors.As(err, &selectedErr) {
			t.Fatalf("ResolveProviders error = %v", err)
		}
		if !reflect.DeepEqual(lookups, []string{
			"STARPORT_OPENAI_API_KEY_REFERENCE",
			"STARPORT_OPENAI_API_KEY_REFERENCE_FALLBACK_AMBIENT",
			"OPENAI_API_KEY",
		}) {
			t.Fatalf("lookups = %#v, want terminal conventional selection", lookups)
		}
	})
}

func TestCatalogDerivedCredentialReferenceEnvironment(t *testing.T) {
	provider := testCredentialProvider("openai", "OPENAI_API_KEY", `^valid-`)
	providers := catalogs.NewProviders()
	if err := providers.Set(provider.ID, &provider); err != nil {
		t.Fatal(err)
	}

	t.Run("reference precedes ambient value", func(t *testing.T) {
		lookup := mapEnvironmentLookup(map[string]string{
			"OPENAI_API_KEY":                                     "valid-ambient",
			"STARPORT_OPENAI_API_KEY_REFERENCE":                  "test:operator-key",
			"STARPORT_OPENAI_API_KEY_REFERENCE_FALLBACK_AMBIENT": "false",
		})
		var sourceCalls int
		cfg := &Config{providerEnvironment: lookup}
		cfg.credentialResolver = credentials.NewResolver(
			credentials.WithEnvironmentLookup(lookup.Lookup),
			credentials.WithReferenceSource(&configReferenceSource{
				backend: "test",
				resolve: func(_ context.Context, reference credentials.Reference) (credentials.SourceMaterial, error) {
					sourceCalls++
					if reference.Resource() != "operator-key" {
						t.Fatalf("resource = %q", reference.Resource())
					}
					return credentials.NewSourceMaterial(
						map[string]string{"value": "valid-reference"},
						"version-1",
						time.Time{},
						nil,
					), nil
				},
			}),
		)

		if err := cfg.ResolveProviders(t.Context(), providers); err != nil {
			t.Fatalf("resolve providers: %v", err)
		}
		value, _ := cfg.Providers[provider.ID].Material.Value("api-key")
		if value != "valid-reference" || sourceCalls != 1 {
			t.Fatalf("resolved value = %q after %d source calls", value, sourceCalls)
		}
	})

	t.Run("programmatic reference precedes environment reference", func(t *testing.T) {
		lookup := mapEnvironmentLookup(map[string]string{
			"STARPORT_OPENAI_API_KEY_REFERENCE": "test:environment-key",
		})
		cfg := &Config{
			providerEnvironment: lookup,
			Providers: ProvidersConfig{provider.ID: {
				CredentialReferences: map[catalogs.ProviderCredentialFieldID]CredentialReference{
					"api-key": {Reference: "test:programmatic-key"},
				},
			}},
		}
		cfg.credentialResolver = credentials.NewResolver(
			credentials.WithEnvironmentLookup(lookup.Lookup),
			credentials.WithReferenceSource(&configReferenceSource{
				backend: "test",
				resolve: func(_ context.Context, reference credentials.Reference) (credentials.SourceMaterial, error) {
					if reference.Resource() != "programmatic-key" {
						t.Fatalf("resource = %q", reference.Resource())
					}
					return credentials.NewSourceMaterial(
						map[string]string{"value": "valid-programmatic"},
						"version-1",
						time.Time{},
						nil,
					), nil
				},
			}),
		)

		if err := cfg.ResolveProviders(t.Context(), providers); err != nil {
			t.Fatalf("resolve providers: %v", err)
		}
		value, _ := cfg.Providers[provider.ID].Material.Value("api-key")
		if value != "valid-programmatic" {
			t.Fatalf("resolved value = %q", value)
		}
	})

	t.Run("typed not-configured result permits declared fallback", func(t *testing.T) {
		lookup := mapEnvironmentLookup(map[string]string{
			"OPENAI_API_KEY":                                     "valid-ambient",
			"STARPORT_OPENAI_API_KEY_REFERENCE":                  "test:missing",
			"STARPORT_OPENAI_API_KEY_REFERENCE_FALLBACK_AMBIENT": "true",
		})
		cfg := &Config{providerEnvironment: lookup}
		cfg.credentialResolver = credentials.NewResolver(
			credentials.WithEnvironmentLookup(lookup.Lookup),
			credentials.WithReferenceSource(&configReferenceSource{
				backend: "test",
				resolve: func(context.Context, credentials.Reference) (credentials.SourceMaterial, error) {
					return credentials.SourceMaterial{}, credentials.NewSourceError(
						credentials.SourceErrorNotConfigured,
						"test",
					)
				},
			}),
		)

		if err := cfg.ResolveProviders(t.Context(), providers); err != nil {
			t.Fatalf("resolve providers: %v", err)
		}
		value, _ := cfg.Providers[provider.ID].Material.Value("api-key")
		if value != "valid-ambient" {
			t.Fatalf("resolved value = %q", value)
		}
	})

	for _, values := range []map[string]string{
		{"STARPORT_OPENAI_API_KEY_REFERENCE_FALLBACK_AMBIENT": "true"},
		{
			"STARPORT_OPENAI_API_KEY_REFERENCE":                  "test:key",
			"STARPORT_OPENAI_API_KEY_REFERENCE_FALLBACK_AMBIENT": "yes",
		},
	} {
		cfg := &Config{providerEnvironment: mapEnvironmentLookup(values)}
		if err := cfg.ResolveProviders(t.Context(), providers); err == nil {
			t.Fatalf("invalid reference environment was accepted: %#v", values)
		}
	}
}

func TestCatalogOnlyProviderEnvironmentResolvesWithoutSourceRoster(t *testing.T) {
	builder, err := starmap.EmbeddedBuilder()
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := builder.Build()
	if err != nil {
		t.Fatal(err)
	}
	cfg := &Config{providerEnvironment: mapEnvironmentLookup(map[string]string{
		"FIREWORKS_API_KEY": "fireworks-key",
	})}
	if err := cfg.ResolveProviders(context.Background(), catalog.Providers()); err != nil {
		t.Fatal(err)
	}
	resolved, found := cfg.Providers["fireworks-ai"]
	value, exists := resolved.Material.Value("api-key")
	if !found || !exists || value != "fireworks-key" ||
		resolved.Material.Profile().Primitive != catalogs.ProviderAuthenticationAPIKey {
		t.Fatalf("Fireworks configuration = %#v", resolved)
	}
}

func TestHetznerInferenceEnvironmentComesFromCatalog(t *testing.T) {
	builder, err := starmap.EmbeddedBuilder()
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := builder.Build()
	if err != nil {
		t.Fatal(err)
	}
	cfg := &Config{providerEnvironment: mapEnvironmentLookup(map[string]string{
		"STARPORT_HETZNER_API_KEY": "hetzner-inference-key",
	})}
	if err := cfg.ResolveProviders(t.Context(), catalog.Providers()); err != nil {
		t.Fatal(err)
	}
	resolved, found := cfg.Providers[catalogs.ProviderIDHetzner]
	if !found {
		t.Fatal("Hetzner inference configuration is missing")
	}
	value, exists := resolved.Material.Value("api-key")
	if !exists || value != "hetzner-inference-key" ||
		resolved.Material.Profile().Primitive != catalogs.ProviderAuthenticationAPIKey {
		t.Fatalf("Hetzner configuration = %#v", resolved)
	}
}

func TestCredentialAliasCollisionsFailBeforeConnectorConstruction(t *testing.T) {
	first := testCredentialProvider("one", "STARPORT_TWO_API_KEY", "")
	second := testCredentialProvider("two", "TWO_API_KEY", "")
	providers := catalogs.NewProviders()
	for _, provider := range []catalogs.Provider{first, second} {
		if err := providers.Set(provider.ID, &provider); err != nil {
			t.Fatal(err)
		}
	}
	reads := 0
	cfg := &Config{providerEnvironment: lookupFunc(func(string) (string, bool) {
		reads++
		return "must-not-be-read", true
	})}
	err := cfg.ResolveProviders(context.Background(), providers)
	if !errors.Is(err, ErrCredentialAliasCollision) {
		t.Fatalf("ResolveProviders error = %v", err)
	}
	if reads != 0 {
		t.Fatalf("environment reads = %d, want zero before collision failure", reads)
	}
}

func TestCredentialReferenceAliasCollisionsFailBeforeReads(t *testing.T) {
	first := testCredentialProvider("one", "STARPORT_TWO_API_KEY_REFERENCE", "")
	second := testCredentialProvider("two", "TWO_API_KEY", "")
	providers := catalogs.NewProviders()
	for _, provider := range []catalogs.Provider{first, second} {
		if err := providers.Set(provider.ID, &provider); err != nil {
			t.Fatal(err)
		}
	}
	reads := 0
	cfg := &Config{providerEnvironment: lookupFunc(func(string) (string, bool) {
		reads++
		return "must-not-be-read", true
	})}
	err := cfg.ResolveProviders(t.Context(), providers)
	if !errors.Is(err, ErrCredentialAliasCollision) {
		t.Fatalf("resolve providers error = %v", err)
	}
	if reads != 0 {
		t.Fatalf("environment reads = %d, want zero", reads)
	}
}

func testCredentialProvider(
	providerID catalogs.ProviderID,
	environment string,
	pattern string,
) catalogs.Provider {
	return catalogs.Provider{
		ID: providerID, Name: string(providerID),
		Credentials: &catalogs.ProviderCredentials{
			Fields: []catalogs.ProviderCredentialField{{
				ID: "api-key", Kind: catalogs.ProviderCredentialFieldSecret, Required: true,
				Environment: []string{environment}, Pattern: pattern,
			}},
			Profiles: []catalogs.ProviderCredentialProfile{{
				ID: "api-key", Primitive: catalogs.ProviderAuthenticationAPIKey,
				Fields: []catalogs.ProviderCredentialFieldID{"api-key"},
				Placements: []catalogs.ProviderCredentialPlacement{{
					Field: "api-key", Kind: catalogs.ProviderCredentialPlacementHeader,
					Name: "Authorization", Scheme: catalogs.ProviderCredentialSchemeBearer,
				}},
			}},
			Inference: catalogs.ProviderCredentialPlane{
				Required: true, Alternatives: []catalogs.ProviderCredentialProfileID{"api-key"},
			},
		},
	}
}

type lookupFunc func(string) (string, bool)

func (f lookupFunc) Lookup(name string) (string, bool) { return f(name) }

func mapEnvironmentLookup(values map[string]string) environmentLookup {
	return lookupFunc(func(name string) (string, bool) {
		value, found := values[name]
		return value, found
	})
}

type configReferenceSource struct {
	backend credentials.ReferenceBackend
	resolve func(context.Context, credentials.Reference) (credentials.SourceMaterial, error)
}

func (s *configReferenceSource) Backend() credentials.ReferenceBackend { return s.backend }

func (s *configReferenceSource) Resolve(
	ctx context.Context,
	reference credentials.Reference,
) (credentials.SourceMaterial, error) {
	return s.resolve(ctx, reference)
}
