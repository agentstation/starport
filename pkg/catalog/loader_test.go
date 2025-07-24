package catalog

import (
	"sync"
	"testing"
)

func TestGetCatalogConcurrency(t *testing.T) {
	// Reset the global state for this test
	catalogMutex.Lock()
	globalCatalog = nil
	loadAttempted = false
	loadError = nil
	catalogMutex.Unlock()

	// Test concurrent access
	var wg sync.WaitGroup
	numGoroutines := 10
	results := make(chan *Catalog, numGoroutines)
	errors := make(chan error, numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cat, err := GetCatalog()
			results <- cat
			errors <- err
		}()
	}

	wg.Wait()
	close(results)
	close(errors)

	// Verify all goroutines got the same catalog instance
	var firstCatalog *Catalog
	for cat := range results {
		if firstCatalog == nil {
			firstCatalog = cat
		} else if cat != firstCatalog {
			t.Error("Different goroutines got different catalog instances")
		}
	}

	// Verify no errors
	for err := range errors {
		if err != nil {
			t.Errorf("Unexpected error: %v", err)
		}
	}
}

func TestReloadCatalog(t *testing.T) {
	// Get initial catalog
	catalog1, err := GetCatalog()
	if err != nil {
		t.Fatalf("Failed to get initial catalog: %v", err)
	}

	// Reload catalog
	err = ReloadCatalog()
	if err != nil {
		t.Fatalf("Failed to reload catalog: %v", err)
	}

	// Get catalog again
	catalog2, err := GetCatalog()
	if err != nil {
		t.Fatalf("Failed to get catalog after reload: %v", err)
	}

	// They should be different instances (reload creates new instance)
	if catalog1 == catalog2 {
		t.Error("Expected different instances after reload")
	}

	// But they should have the same content
	if len(catalog1.Models) != len(catalog2.Models) {
		t.Errorf("Model count mismatch: %d vs %d", len(catalog1.Models), len(catalog2.Models))
	}
}

func TestMustGetCatalog(t *testing.T) {
	// This should not panic
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("MustGetCatalog panicked: %v", r)
		}
	}()

	catalog := MustGetCatalog()
	if catalog == nil {
		t.Error("Expected non-nil catalog from MustGetCatalog")
	}
}

func BenchmarkGetCatalog(b *testing.B) {
	// Reset the global state
	catalogMutex.Lock()
	globalCatalog = nil
	loadAttempted = false
	loadError = nil
	catalogMutex.Unlock()

	b.ResetTimer()

	// First call will load
	for i := 0; i < b.N; i++ {
		_, _ = GetCatalog()
	}
}

func BenchmarkGetCatalogCached(b *testing.B) {
	// Pre-load the catalog
	_, _ = GetCatalog()

	b.ResetTimer()

	// All calls should be cached
	for i := 0; i < b.N; i++ {
		_, _ = GetCatalog()
	}
}