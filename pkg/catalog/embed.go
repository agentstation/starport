package catalog

import (
	_ "embed"
	"encoding/json"
	"fmt"
)

// Embed the catalog.json file directly
//go:embed catalog.json
var catalogJSON []byte

// LoadEmbeddedCatalog loads and parses the embedded catalog.json
func LoadEmbeddedCatalog() (*Catalog, error) {
	var catalog Catalog
	if err := json.Unmarshal(catalogJSON, &catalog); err != nil {
		return nil, fmt.Errorf("failed to unmarshal catalog: %w", err)
	}

	// Initialize maps if they're nil
	if catalog.Models == nil {
		catalog.Models = make(map[string]*Model)
	}
	if catalog.Providers == nil {
		catalog.Providers = make(map[string]*Provider)
	}
	if catalog.Endpoints == nil {
		catalog.Endpoints = make(map[string][]*Endpoint)
	}

	return &catalog, nil
}