package server

import (
	"github.com/agentstation/starport/internal/connectors"
)

// newTestServer creates a server with a mock connector registry for testing
func newTestServer(config *Config) *Server {
	registry := NewConnectorRegistry()
	
	// Add a mock connector for testing
	mockConfig := connectors.ProviderConfig{
		BaseURL: "http://mock",
	}
	mockConnector := connectors.NewMockConnector(mockConfig)
	registry.Register("mock", mockConnector)
	
	return New(config, registry)
}