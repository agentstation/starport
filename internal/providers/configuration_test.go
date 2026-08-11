package providers

import (
	"context"
	"testing"
	"time"

	"github.com/agentstation/starmap/pkg/catalogs"

	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/credentials"
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
	}}
	projected := Configurations(settings)
	vertex := projected[catalogs.ProviderIDGoogleVertex]
	if vertex.Connector.BaseURL != "https://vertex.example" ||
		vertex.Connector.Timeout != time.Minute ||
		vertex.Connector.MaxConnections != 12 || !vertex.Connector.Enabled {
		t.Fatalf("projected Vertex configuration = %#v", vertex)
	}
}

func TestConfigurationsPreservesRequestTimeMaterialSource(t *testing.T) {
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
	projected := Configurations(config.ProvidersConfig{"yaml-cloud": {
		Material: material, CredentialSource: source,
		Timeout: time.Second, MaxConnections: 1, Enabled: true,
	}})
	configured := projected["yaml-cloud"]
	if configured.CredentialSource == nil {
		t.Fatalf("projected cloud configuration = %#v", configured)
	}
	resolved, err := configured.CredentialSource.ResolveMaterial(t.Context())
	if err != nil {
		t.Fatalf("resolve projected material: %v", err)
	}
	value, found := resolved.Value("access-token")
	if !found || value != "renewable" {
		t.Fatalf("projected material value = %q, %t", value, found)
	}
}

type staticMaterialSource struct {
	material credentials.Material
}

func (s staticMaterialSource) ResolveMaterial(context.Context) (credentials.Material, error) {
	return s.material, nil
}
