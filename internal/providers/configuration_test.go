package providers

import (
	"testing"
	"time"

	"github.com/agentstation/starmap/pkg/catalogs"

	"github.com/agentstation/starport/internal/config"
)

func TestConfigurationsPreservesExactProviderSettings(t *testing.T) {
	settings := config.ProvidersConfig{GoogleVertexAI: config.ProviderConfig{
		APIKey: "token", BaseURL: "https://vertex.example", Timeout: time.Minute,
		MaxConnections: 12, Enabled: true, ProjectID: "project", Location: "location",
	}}
	projected := Configurations(settings)
	vertex := projected[catalogs.ProviderIDGoogleVertex]
	if vertex.APIKey != "token" || vertex.BaseURL != "https://vertex.example" ||
		vertex.Timeout != time.Minute || vertex.MaxConnections != 12 || !vertex.Enabled {
		t.Fatalf("projected Vertex configuration = %#v", vertex)
	}
	if vertex.EndpointBindings["project"] != "project" || vertex.EndpointBindings["location"] != "location" {
		t.Errorf("endpoint bindings = %#v", vertex.EndpointBindings)
	}
	vertex.EndpointBindings["project"] = "changed"
	if settings.GoogleVertexAI.ProjectID != "project" {
		t.Fatal("projection changed the source configuration")
	}
}
