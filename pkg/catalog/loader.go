package catalog

import (
	"fmt"
	"sync"
)

var (
	// Global catalog instance
	globalCatalog *Catalog
	// Mutex for thread-safe initialization
	catalogMutex sync.RWMutex
	// Error from loading (if any)
	loadError error
	// Track if we've attempted to load
	loadAttempted bool
)

// GetCatalog returns the global catalog instance, loading it if necessary
func GetCatalog() (*Catalog, error) {
	// Fast path: check if already loaded
	catalogMutex.RLock()
	if loadAttempted {
		catalogMutex.RUnlock()
		return globalCatalog, loadError
	}
	catalogMutex.RUnlock()

	// Slow path: need to load
	catalogMutex.Lock()
	defer catalogMutex.Unlock()

	// Double-check in case another goroutine loaded while we waited for lock
	if loadAttempted {
		return globalCatalog, loadError
	}

	// Attempt to load
	globalCatalog, loadError = LoadEmbeddedCatalog()
	loadAttempted = true

	if loadError != nil {
		return nil, fmt.Errorf("failed to load catalog: %w", loadError)
	}

	return globalCatalog, nil
}

// ReloadCatalog forces a reload of the catalog (useful for testing)
func ReloadCatalog() error {
	catalogMutex.Lock()
	defer catalogMutex.Unlock()

	globalCatalog, loadError = LoadEmbeddedCatalog()
	loadAttempted = true

	return loadError
}

// MustGetCatalog returns the catalog or panics if it cannot be loaded
func MustGetCatalog() *Catalog {
	catalog, err := GetCatalog()
	if err != nil {
		panic(fmt.Sprintf("failed to load catalog: %v", err))
	}
	return catalog
}