package connectors_test

import (
	"testing"

	"github.com/agentstation/starmap/pkg/catalogs"

	"github.com/agentstation/starport/internal/providers/connectors"
)

func TestCatalogDrivenAzureUsesOpenAITransport(t *testing.T) {
	connector, err := connectors.NewConnector(
		"azure-openai",
		[]catalogs.EndpointType{catalogs.EndpointTypeOpenAI},
		connectors.ProviderConfig{BaseURL: "https://resource.example"},
	)
	if err != nil {
		t.Fatalf("create catalog-driven Azure connector: %v", err)
	}
	if connector.Name() != "azure-openai" {
		t.Fatalf("connector name = %q, want %q", connector.Name(), "azure-openai")
	}
}
