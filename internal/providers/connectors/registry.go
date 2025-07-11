package connectors

// Registry manages connector instances
type Registry interface {
	// Get returns a connector by provider name
	Get(provider string) Connector

	// List returns all registered provider names
	List() []string
}
