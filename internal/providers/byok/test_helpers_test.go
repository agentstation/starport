package byok

import (
	"github.com/agentstation/starport/internal/credentials"
	"github.com/agentstation/starport/internal/providers/connectors"
	"github.com/agentstation/starport/internal/storage"
)

func newTestProviderKeys(store storage.KVStore, masterKey []byte) (ProviderKeys, error) {
	repository, err := credentials.Open(store)
	if err != nil {
		return nil, err
	}
	adapters, err := connectors.ProductionAdapterRegistry()
	if err != nil {
		return nil, err
	}
	return NewProviderKeys(repository, masterKey, adapters)
}
