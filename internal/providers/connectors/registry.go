package connectors

import (
	"context"

	"github.com/agentstation/starport/internal/credentials"
)

// Registry manages connector instances
type Registry interface {
	// Get returns a connector by provider name
	Get(provider string) Connector

	// List returns all registered provider names
	List() []string

	// ResolveMaterial resolves request-selected inference material for one exact
	// provider.
	ResolveMaterial(context.Context, string) (credentials.Material, error)
}
