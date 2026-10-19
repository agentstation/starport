package keyring

import (
	"context"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/agentstation/starport/internal/credentials"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

// slowMaterialRepository stands in for a cold load on a loaded machine. Once
// armed, it holds the next record read for the stall, bounded by the read's
// context, and records the bound that the read ran under.
type slowMaterialRepository struct {
	credentials.Repository
	stall     time.Duration
	armed     atomic.Bool
	stalls    atomic.Int64
	remaining atomic.Int64
}

func (r *slowMaterialRepository) Get(ctx context.Context, scope, provider string) (credentials.Record, error) {
	if r.armed.CompareAndSwap(true, false) {
		r.stalls.Add(1)
		if deadline, ok := ctx.Deadline(); ok {
			r.remaining.Store(int64(time.Until(deadline)))
		}
		select {
		case <-time.After(r.stall):
		case <-ctx.Done():
			return credentials.Record{}, ctx.Err()
		}
	}
	return r.Repository.Get(ctx, scope, provider)
}

// TestManagedMaterialColdLoadOutlastsASlowSource holds the default load bound
// against a cold load that takes two seconds. A cold load pays one Argon2id
// derivation, and under the race detector on a loaded macOS runner the
// one-second bound reported a load that succeeded as an unavailable source
// and dropped its material. The bound now takes most of the validity window,
// because the material's validity starts when the load starts.
func TestManagedMaterialColdLoadOutlastsASlowSource(t *testing.T) {
	for _, shared := range []bool{false, true} {
		name := "account"
		if shared {
			name = "shared"
		}
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				provider := syntheticCredentialProvider()
				repository, err := credentials.Open(storage.NewMockStore())
				require.NoError(t, err)
				slow := &slowMaterialRepository{Repository: repository, stall: 2 * time.Second}
				validator, err := NewCatalogCredentialValidator(func(id catalogs.ProviderID) (catalogs.Provider, bool) {
					return provider, id == provider.ID
				})
				require.NoError(t, err)
				master, err := credentials.GenerateMasterKey()
				require.NoError(t, err)
				limits := credentials.DefaultMaterialLimits()
				manager, err := NewProviderKeys(slow, master, validator)
				require.NoError(t, err)
				defer manager.(*keyManager).materials.close()
				scope := AccountScope("acme")
				if shared {
					_, err = manager.AddSharedCredential(t.Context(), string(provider.ID), map[string]string{"api-key": "fixture-secret"}, nil, SharedCredentialParams{Access: credentials.AccessGranted, Grants: []string{"acme"}})
				} else {
					_, err = manager.AddKey(t.Context(), scope, string(provider.ID), map[string]string{"api-key": "fixture-secret"}, nil, false, 0)
				}
				require.NoError(t, err)

				// The write is done. The cold load's record read is the next read.
				slow.armed.Store(true)
				started := time.Now()
				var material credentials.Material
				if shared {
					material, err = manager.ResolveSharedMaterial(t.Context(), "acme", provider)
				} else {
					material, err = manager.ResolveStoredMaterial(t.Context(), scope, provider)
				}
				require.NoError(t, err, "a cold load slower than one second resolves")
				require.Equal(t, slow.stall, time.Since(started), "the stall held the cold load")
				require.EqualValues(t, 1, slow.stalls.Load())
				value, ok := material.Value("api-key")
				require.True(t, ok)
				require.Equal(t, "fixture-secret", value)
				require.NoError(t, material.CheckValidity(time.Now()))
				require.Equal(t, limits.LoadTimeout, time.Duration(slow.remaining.Load()), "the load runs under the default bound")
				require.Less(t, limits.LoadTimeout, limits.Validity, "a load ends inside the validity window")
			})
		})
	}
}
