package server

import (
	"github.com/agentstation/starport/internal/providers/connectors"
	"github.com/agentstation/starport/internal/registry"
)

// registryAdapter adapts registry.Registry to connectors.Registry
type registryAdapter struct {
	reg *registry.Registry
}

// newRegistryAdapter creates a new adapter
func newRegistryAdapter(reg *registry.Registry) connectors.Registry {
	return &registryAdapter{reg: reg}
}

// Get implements connectors.Registry
func (a *registryAdapter) Get(provider string) connectors.Connector {
	conn, _ := a.reg.Get(provider)
	return conn
}

// List implements connectors.Registry
func (a *registryAdapter) List() []string {
	return a.reg.ListProviders()
}
