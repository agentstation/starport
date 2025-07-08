package server

import (
	"github.com/agentstation/starport/internal/connectors"
)

// Ensure ConnectorRegistry implements connectors.Registry interface
var _ connectors.Registry = (*ConnectorRegistry)(nil)

// Get implements the connectors.Registry interface (no error return)
func (r *ConnectorRegistry) Get(provider string) connectors.Connector {
	return r.connectors[provider]
}

// List implements the connectors.Registry interface
func (r *ConnectorRegistry) List() []string {
	providers := make([]string, 0, len(r.connectors))
	for provider := range r.connectors {
		providers = append(providers, provider)
	}
	return providers
}