package config

import (
	"errors"
	"reflect"
	"testing"

	"github.com/agentstation/starmap/pkg/catalogs"
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
			if err := cfg.ResolveProviders(providers); err != nil {
				t.Fatalf("ResolveProviders: %v", err)
			}
			if got := cfg.Providers[provider.ID].APIKey; got != test.want {
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
		err := cfg.ResolveProviders(providers)
		if !errors.Is(err, ErrProviderCredentialInvalid) {
			t.Fatalf("ResolveProviders error = %v", err)
		}
		if !reflect.DeepEqual(lookups, []string{"OPENAI_API_KEY"}) {
			t.Fatalf("lookups = %#v, want terminal conventional selection", lookups)
		}
	})
}

func TestCatalogOnlyProviderEnvironmentResolvesWithoutSourceRoster(t *testing.T) {
	builder, err := catalogs.NewEmbedded()
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
	if err := cfg.ResolveProviders(catalog.Providers()); err != nil {
		t.Fatal(err)
	}
	resolved, found := cfg.Providers["fireworks-ai"]
	if !found || resolved.APIKey != "fireworks-key" ||
		resolved.Primitive != catalogs.ProviderAuthenticationAPIKey {
		t.Fatalf("Fireworks configuration = %#v", resolved)
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
	err := cfg.ResolveProviders(providers)
	if !errors.Is(err, ErrCredentialAliasCollision) {
		t.Fatalf("ResolveProviders error = %v", err)
	}
	if reads != 0 {
		t.Fatalf("environment reads = %d, want zero before collision failure", reads)
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
