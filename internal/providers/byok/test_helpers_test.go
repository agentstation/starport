package byok

import (
	"context"

	"github.com/agentstation/starmap"
	"github.com/agentstation/starmap/pkg/catalogs"

	"github.com/agentstation/starport/internal/credentials"
	"github.com/agentstation/starport/internal/storage"
)

func newTestProviderKeys(store storage.KVStore, masterKey []byte) (ProviderKeys, error) {
	repository, err := credentials.Open(store)
	if err != nil {
		return nil, err
	}
	client, err := starmap.NewContext(context.Background())
	if err != nil {
		return nil, err
	}
	catalog := client.CurrentCatalogState().Catalog
	validator, err := NewCatalogCredentialValidator(func(providerID catalogs.ProviderID) (catalogs.Provider, bool) {
		provider, lookupErr := catalog.Provider(providerID)
		return provider, lookupErr == nil
	})
	if err != nil {
		return nil, err
	}
	return NewProviderKeys(repository, masterKey, validator)
}
