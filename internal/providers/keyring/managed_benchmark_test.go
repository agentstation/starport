package keyring

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/agentstation/starport/internal/credentials"
	"github.com/agentstation/starport/internal/storage"
)

func BenchmarkManagedMaterialLifecycle(b *testing.B) {
	for _, state := range []string{"cold", "expired", "rotation", "revoked"} {
		b.Run(state, func(b *testing.B) {
			manager, provider, scope := managedBenchmarkFixture(b)
			now := time.Now()
			manager.materials.now = func() time.Time { return now }
			if state == "revoked" {
				if err := manager.DeleteKey(b.Context(), scope, string(provider.ID)); err != nil {
					b.Fatal(err)
				}
			}
			revision := 0
			b.ReportAllocs()
			for b.Loop() {
				switch state {
				case "cold":
					manager.materials.invalidate(scope, string(provider.ID))
				case "expired":
					now = now.Add(manager.materials.limits.Validity)
				case "rotation":
					revision++
					_, err := manager.UpdateKey(b.Context(), scope, string(provider.ID), map[string]string{"api-key": fmt.Sprintf("fixture-revision-%d", revision)}, nil, nil, nil)
					if err != nil {
						b.Fatal(err)
					}
				}
				material, err := manager.ResolveStoredMaterial(b.Context(), scope, provider)
				if state == "revoked" {
					if !errors.Is(err, ErrKeyNotFound) {
						b.Fatalf("revoked credential: %v", err)
					}
				} else if err != nil || material.Empty() {
					b.Fatalf("resolve credential: %v", err)
				}
			}
		})
	}
}

func BenchmarkManagedMaterialWarmShared(b *testing.B) {
	manager, provider, _ := managedBenchmarkFixture(b)
	_, err := manager.AddSharedCredential(b.Context(), string(provider.ID), map[string]string{"api-key": "fixture-shared"}, nil, SharedCredentialParams{})
	if err != nil {
		b.Fatal(err)
	}
	_, err = manager.ResolveSharedMaterial(b.Context(), "benchmark", provider)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for b.Loop() {
		material, err := manager.ResolveSharedMaterial(b.Context(), "benchmark", provider)
		if err != nil || material.Empty() {
			b.Fatalf("resolve shared material: %v", err)
		}
	}
}

func managedBenchmarkFixture(b *testing.B) (*keyManager, catalogs.Provider, string) {
	b.Helper()
	provider := syntheticCredentialProvider()
	store := storage.NewMockStore()
	b.Cleanup(func() { _ = store.Close() })
	repository, err := credentials.Open(store)
	if err != nil {
		b.Fatal(err)
	}
	validator, err := NewCatalogCredentialValidator(func(id catalogs.ProviderID) (catalogs.Provider, bool) { return provider, id == provider.ID })
	if err != nil {
		b.Fatal(err)
	}
	master, err := credentials.GenerateMasterKey()
	if err != nil {
		b.Fatal(err)
	}
	keys, err := NewProviderKeys(repository, master, validator)
	if err != nil {
		b.Fatal(err)
	}
	manager := keys.(*keyManager)
	b.Cleanup(manager.materials.close)
	scope := AccountScope("benchmark")
	_, err = manager.AddKey(b.Context(), scope, string(provider.ID), map[string]string{"api-key": "fixture-secret"}, nil, false, 0)
	if err != nil {
		b.Fatal(err)
	}
	_, err = manager.ResolveStoredMaterial(b.Context(), scope, provider)
	if err != nil {
		b.Fatal(err)
	}
	return manager, provider, scope
}
