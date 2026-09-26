package catalog

import (
	"sync"

	starmap "github.com/agentstation/starmap"
	"github.com/agentstation/starmap/pkg/catalogs"
)

// Bundled returns the immutable catalog shipped in the pinned Starmap module.
// Accepted upstream catalogs and runtime refresh cannot replace this value.
func Bundled() (*catalogs.Catalog, error) { return bundledCatalog() }

var bundledCatalog = sync.OnceValues(func() (*catalogs.Catalog, error) {
	client, err := starmap.New()
	if err != nil {
		return nil, err
	}
	return client.Catalog(), nil
})
