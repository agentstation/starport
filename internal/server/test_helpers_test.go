package server

import (
	"github.com/agentstation/starport/internal/providers/connectors"
	"github.com/agentstation/starport/internal/registry"
)

// newTestServer creates a server with a mock connector registry for testing
func newTestServer(config *Config) *Server {
	reg := registry.NewEmpty()

	// Add a mock connector for testing
	mockConfig := connectors.ProviderConfig{
		BaseURL: "http://mock",
	}
	mockConnector := connectors.NewMockConnector(mockConfig)
	reg.Register("mock", mockConnector)

	return New(config, reg)
}
