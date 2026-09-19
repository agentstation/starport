package keyring

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/agentstation/starport/internal/credentials"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

type materialReadCounter struct {
	credentials.Repository
	reads atomic.Int64
}

func (r *materialReadCounter) Get(ctx context.Context, scope, provider string) (credentials.Record, error) {
	r.reads.Add(1)
	return r.Repository.Get(ctx, scope, provider)
}

func TestManagedMaterialWarmRequestsDoNotReadRepository(t *testing.T) {
	for _, shared := range []bool{false, true} {
		name := "account"
		if shared {
			name = "shared"
		}
		t.Run(name, func(t *testing.T) {
			provider := syntheticCredentialProvider()
			repository, err := credentials.Open(storage.NewMockStore())
			require.NoError(t, err)
			counted := &materialReadCounter{Repository: repository}
			validator, err := NewCatalogCredentialValidator(func(id catalogs.ProviderID) (catalogs.Provider, bool) {
				return provider, id == provider.ID
			})
			require.NoError(t, err)
			master, err := credentials.GenerateMasterKey()
			require.NoError(t, err)
			manager, err := NewProviderKeys(counted, master, validator)
			require.NoError(t, err)
			if shared {
				_, err = manager.AddSharedCredential(t.Context(), string(provider.ID), map[string]string{"api-key": "fixture-secret"}, nil, SharedCredentialParams{})
			} else {
				_, err = manager.AddKey(t.Context(), AccountScope("account-a"), string(provider.ID), map[string]string{"api-key": "fixture-secret"}, nil, false, 0)
			}
			require.NoError(t, err)
			scope := AccountScope("account-a")
			resolve := func() credentials.Material {
				var material credentials.Material
				var resolveErr error
				if shared {
					material, resolveErr = manager.ResolveSharedMaterial(t.Context(), "account-a", provider)
				} else {
					material, resolveErr = manager.ResolveStoredMaterial(t.Context(), scope, provider)
				}
				require.NoError(t, resolveErr)
				value, ok := material.Value("api-key")
				require.True(t, ok)
				require.Equal(t, "fixture-secret", value)
				return material
			}
			resolve()
			before := counted.reads.Load()
			for range 3 {
				resolve()
			}

			var allocationErr error
			allocations := testing.AllocsPerRun(100, func() {
				if shared {
					_, allocationErr = manager.ResolveSharedMaterial(t.Context(), "account-a", provider)
				} else {
					_, allocationErr = manager.ResolveStoredMaterial(t.Context(), scope, provider)
				}
			})
			require.NoError(t, allocationErr)
			require.Zero(t, allocations, "warm selection must not allocate loader closures")
			require.Equal(t, before, counted.reads.Load(), "warm requests must use managed material without repository reads")
		})
	}
}

func BenchmarkManagedMaterialWarmAccount(b *testing.B) {
	provider := syntheticCredentialProvider()
	repository, err := credentials.Open(storage.NewMockStore())
	require.NoError(b, err)
	validator, err := NewCatalogCredentialValidator(func(id catalogs.ProviderID) (catalogs.Provider, bool) {
		return provider, id == provider.ID
	})
	require.NoError(b, err)
	master, err := credentials.GenerateMasterKey()
	require.NoError(b, err)
	manager, err := NewProviderKeys(repository, master, validator)
	require.NoError(b, err)
	scope := AccountScope("benchmark-account")
	_, err = manager.AddKey(b.Context(), scope, string(provider.ID), map[string]string{"api-key": "fixture-secret"}, nil, false, 0)
	require.NoError(b, err)
	_, err = manager.ResolveStoredMaterial(b.Context(), scope, provider)
	require.NoError(b, err)
	b.ReportAllocs()
	for b.Loop() {
		material, err := manager.ResolveStoredMaterial(b.Context(), scope, provider)
		if err != nil || material.Empty() {
			b.Fatalf("resolve warm material: %v", err)
		}
	}
}
