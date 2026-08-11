package providers

import (
	"context"
	"testing"
	"time"

	"github.com/agentstation/starmap/pkg/catalogs"

	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/credentials"
	"github.com/agentstation/starport/internal/providerauth"
)

func TestConfigurationsPreservesExactProviderSettings(t *testing.T) {
	profile := catalogs.ProviderCredentialProfile{
		ID: "api-key", Primitive: catalogs.ProviderAuthenticationAPIKey,
		Fields: []catalogs.ProviderCredentialFieldID{"api-key"},
		Placements: []catalogs.ProviderCredentialPlacement{{
			Field: "api-key", Kind: catalogs.ProviderCredentialPlacementHeader,
			Name: "Authorization", Scheme: catalogs.ProviderCredentialSchemeBearer,
		}},
	}
	settings := config.ProvidersConfig{catalogs.ProviderIDGoogleVertex: {
		Material: credentials.NewMaterial(
			profile,
			map[catalogs.ProviderCredentialFieldID]string{"api-key": "token"},
			credentials.MaterialMetadata{Version: "opaque"},
		),
		BaseURL: "https://vertex.example", Timeout: time.Minute,
		MaxConnections: 12, Enabled: true,
		EndpointBindings: map[string]string{"project": "project", "location": "location"},
	}}
	projected, err := Configurations(settings)
	if err != nil {
		t.Fatalf("project configurations: %v", err)
	}
	vertex := projected[catalogs.ProviderIDGoogleVertex]
	if vertex.APIKey != "token" || vertex.BaseURL != "https://vertex.example" ||
		vertex.AuthMode != providerauth.ModeStatic || vertex.Timeout != time.Minute ||
		vertex.MaxConnections != 12 || !vertex.Enabled {
		t.Fatalf("projected Vertex configuration = %#v", vertex)
	}
	if vertex.EndpointBindings["project"] != "project" || vertex.EndpointBindings["location"] != "location" {
		t.Errorf("endpoint bindings = %#v", vertex.EndpointBindings)
	}
	vertex.EndpointBindings["project"] = "changed"
	if settings[catalogs.ProviderIDGoogleVertex].EndpointBindings["project"] != "project" {
		t.Fatal("projection changed the source configuration")
	}
}

func TestConfigurationsProjectsDefaultMaterialThroughBearerSource(t *testing.T) {
	profile := catalogs.ProviderCredentialProfile{
		ID: "default", Primitive: catalogs.ProviderAuthenticationGoogleDefault,
		Fields: []catalogs.ProviderCredentialFieldID{"access-token"},
		Placements: []catalogs.ProviderCredentialPlacement{{
			Field: "access-token", Kind: catalogs.ProviderCredentialPlacementHeader,
			Name: "Authorization", Scheme: catalogs.ProviderCredentialSchemeBearer,
		}},
	}
	material := credentials.NewMaterial(
		profile,
		map[catalogs.ProviderCredentialFieldID]string{"access-token": "renewable"},
		credentials.MaterialMetadata{Version: "opaque", ExpiresAt: time.Now().Add(time.Hour)},
	)
	source := staticMaterialSource{material: material}
	projected, err := Configurations(config.ProvidersConfig{"yaml-cloud": {
		Material: material, CredentialSource: source,
		Timeout: time.Second, MaxConnections: 1, Enabled: true,
	}})
	if err != nil {
		t.Fatalf("project configurations: %v", err)
	}
	configured := projected["yaml-cloud"]
	if configured.AuthMode != providerauth.ModeDefault || configured.CredentialSource == nil {
		t.Fatalf("projected cloud configuration = %#v", configured)
	}
	token, err := configured.CredentialSource.Token(t.Context())
	if err != nil || token.Value != "renewable" {
		t.Fatalf("projected bearer token = %#v, %v", token, err)
	}
}

type staticMaterialSource struct {
	material credentials.Material
}

func (s staticMaterialSource) ResolveMaterial(context.Context) (credentials.Material, error) {
	return s.material, nil
}
