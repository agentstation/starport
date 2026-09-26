package keyring

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"testing/synctest"

	"github.com/agentstation/starport/internal/credentials"
	"github.com/stretchr/testify/require"
)

func TestManagedMaterialColdLoadCoalescesSuccessAndFailure(t *testing.T) {
	for _, failed := range []bool{false, true} {
		name := "success"
		if failed {
			name = "failure"
		}
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				cache := newManagedMaterials()
				provider := syntheticCredentialProvider()
				key := materialIdentity{scope: AccountScope("a"), provider: string(provider.ID)}
				release := make(chan struct{})
				results := make(chan error, 8)
				var calls atomic.Int64
				unavailable := errors.New("fixture source unavailable")
				load := func(context.Context) (credentials.Material, error) {
					calls.Add(1)
					<-release
					if failed {
						return credentials.Material{}, unavailable
					}
					return credentials.NewMaterial(provider.Credentials.Profiles[0], nil, credentials.MaterialMetadata{}), nil
				}
				for range 8 {
					go func() { _, err := cache.resolve(t.Context(), key, provider, false, load); results <- err }()
				}
				synctest.Wait()
				require.EqualValues(t, 1, calls.Load())
				close(release)
				for range 8 {
					err := <-results
					if failed {
						require.ErrorIs(t, err, unavailable)
					} else {
						require.NoError(t, err)
					}
				}
				require.EqualValues(t, 1, calls.Load(), "concurrent failures must share the same source attempt")
			})
		})
	}
}

func TestManagedMaterialTenantLoadLimitDoesNotBlockAnotherTenant(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cache := newManagedMaterials()
		cache.limits.TenantConcurrentLoads = 1
		provider := syntheticCredentialProvider()
		release := make(chan struct{})
		result := make(chan error, 1)
		go func() {
			_, err := cache.resolve(t.Context(), materialIdentity{scope: AccountScope("a"), provider: "one"}, provider, false, func(context.Context) (credentials.Material, error) {
				<-release
				return credentials.NewMaterial(provider.Credentials.Profiles[0], nil, credentials.MaterialMetadata{}), nil
			})
			result <- err
		}()
		synctest.Wait()
		load := func(context.Context) (credentials.Material, error) {
			return credentials.NewMaterial(provider.Credentials.Profiles[0], nil, credentials.MaterialMetadata{}), nil
		}
		_, err := cache.resolve(t.Context(), materialIdentity{scope: AccountScope("a"), provider: "two"}, provider, false, load)
		require.ErrorIs(t, err, ErrMaterialCapacity)
		_, err = cache.resolve(t.Context(), materialIdentity{scope: AccountScope("b"), provider: "two"}, provider, false, load)
		require.NoError(t, err)
		close(release)
		require.NoError(t, <-result)
	})
}

func TestManagedMaterialCallerCancellationDoesNotCancelSharedLoad(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cache := newManagedMaterials()
		provider := syntheticCredentialProvider()
		key := materialIdentity{scope: AccountScope("a"), provider: string(provider.ID)}
		release := make(chan struct{})
		leaderCtx, cancel := context.WithCancel(t.Context())
		leader := make(chan error, 1)
		follower := make(chan error, 1)
		load := func(ctx context.Context) (credentials.Material, error) {
			select {
			case <-ctx.Done():
				return credentials.Material{}, ctx.Err()
			case <-release:
				return credentials.NewMaterial(provider.Credentials.Profiles[0], nil, credentials.MaterialMetadata{}), nil
			}
		}
		go func() { _, err := cache.resolve(leaderCtx, key, provider, false, load); leader <- err }()
		synctest.Wait()
		go func() { _, err := cache.resolve(t.Context(), key, provider, false, load); follower <- err }()
		synctest.Wait()
		cancel()
		require.ErrorIs(t, <-leader, context.Canceled)
		close(release)
		require.NoError(t, <-follower)
	})
}
