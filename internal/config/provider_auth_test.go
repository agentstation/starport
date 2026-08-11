package config

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/agentstation/starmap/pkg/catalogs"

	"github.com/agentstation/starport/internal/credentials"
)

func TestLoaderDerivesCloudAuthenticationFromCatalogProfiles(t *testing.T) {
	provider := testCloudProvider()
	providers := catalogs.NewProviders()
	if err := providers.Set(provider.ID, &provider); err != nil {
		t.Fatal(err)
	}
	lookup := mapEnvironmentLookup(map[string]string{"ACME_PROJECT": "project"})
	cfg := &Config{providerEnvironment: lookup}
	cfg.credentialResolver = credentials.NewResolver(
		credentials.WithEnvironmentLookup(lookup.Lookup),
		credentials.WithCloudChain(
			catalogs.ProviderAuthenticationGoogleDefault,
			credentials.CloudChainFunc(func(
				context.Context,
				catalogs.ProviderCredentialProfile,
				map[catalogs.ProviderCredentialFieldID]catalogs.ProviderCredentialField,
			) (credentials.SourceMaterial, error) {
				return credentials.NewSourceMaterial(
					map[string]string{"access-token": "cloud-token"},
					"renewable-version",
					time.Now().Add(time.Hour),
					&credentials.Lease{Renewable: true},
				), nil
			}),
		),
	)

	if err := cfg.ResolveProviders(context.Background(), providers); err != nil {
		t.Fatalf("resolve cloud provider: %v", err)
	}
	resolved := cfg.Providers[provider.ID]
	if resolved.Material.Profile().Primitive != catalogs.ProviderAuthenticationGoogleDefault {
		t.Fatalf("selected primitive = %q", resolved.Material.Profile().Primitive)
	}
	assertProviderMaterialValue(t, cfg, provider.ID, "access-token", "cloud-token")
	if resolved.EndpointBindings["project"] != "project" {
		t.Fatalf("endpoint bindings = %#v", resolved.EndpointBindings)
	}
}

func TestProviderConfigRejectsInvalidOperationalValues(t *testing.T) {
	config := ProviderConfig{BaseURL: "://invalid", Timeout: time.Second, MaxConnections: 1}
	if err := config.Validate(); err == nil || !strings.Contains(err.Error(), "base URL") {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestProvidersConfigDoesNotOwnProviderAuthenticationRoster(t *testing.T) {
	providers := ProvidersConfig{"yaml-only": {
		BaseURL: "https://provider.test", Timeout: time.Second, MaxConnections: 1,
	}}
	if err := providers.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func testCloudProvider() catalogs.Provider {
	return catalogs.Provider{
		ID: "acme-cloud", Name: "Acme Cloud",
		Inference: &catalogs.ProviderInference{BaseURL: "https://{project}.example.test"},
		Credentials: &catalogs.ProviderCredentials{
			Fields: []catalogs.ProviderCredentialField{
				{ID: "project", Kind: catalogs.ProviderCredentialFieldParameter, Required: true, Environment: []string{"ACME_PROJECT"}},
				{ID: "access-token", Kind: catalogs.ProviderCredentialFieldSecret, Required: true},
			},
			Profiles: []catalogs.ProviderCredentialProfile{{
				ID: "default", Primitive: catalogs.ProviderAuthenticationGoogleDefault,
				Scopes: []string{"https://example.test/.default"},
				Fields: []catalogs.ProviderCredentialFieldID{"project", "access-token"},
				Placements: []catalogs.ProviderCredentialPlacement{{
					Field: "access-token", Kind: catalogs.ProviderCredentialPlacementHeader,
					Name: "Authorization", Scheme: catalogs.ProviderCredentialSchemeBearer,
				}},
				EndpointBindings: []catalogs.ProviderCredentialEndpointBinding{{
					Variable: "project", Field: "project",
					Format: catalogs.ProviderCredentialEndpointBindingPathSegment,
				}},
			}},
			Inference: catalogs.ProviderCredentialPlane{
				Required: true, Alternatives: []catalogs.ProviderCredentialProfileID{"default"},
			},
		},
	}
}
