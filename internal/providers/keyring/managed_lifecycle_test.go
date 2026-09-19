package keyring

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"github.com/agentstation/starport/internal/credentials"
	"github.com/stretchr/testify/require"
)

func TestManagedMaterialLoadDeadlineReleasesCapacity(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cache := newManagedMaterials()
		defer cache.close()
		cache.limits.ConcurrentLoads = 1
		provider := syntheticCredentialProvider()
		key := materialIdentity{scope: AccountScope("a"), provider: string(provider.ID)}
		started := time.Now()
		_, err := cache.resolve(t.Context(), key, provider, false, func(ctx context.Context) (credentials.Material, error) {
			<-ctx.Done()
			return credentials.Material{}, ctx.Err()
		})
		require.ErrorIs(t, err, context.DeadlineExceeded)
		require.Equal(t, cache.limits.LoadTimeout, time.Since(started))
		material, err := cache.resolve(t.Context(), key, provider, false, func(context.Context) (credentials.Material, error) {
			return credentials.NewMaterial(provider.Credentials.Profiles[0], nil, credentials.MaterialMetadata{}), nil
		})
		require.NoError(t, err)
		require.NoError(t, material.CheckValidity(time.Now()))
	})
}

func TestManagedMaterialShutdownJoinsActiveLoad(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cache := newManagedMaterials()
		provider := syntheticCredentialProvider()
		key := materialIdentity{scope: AccountScope("a"), provider: string(provider.ID)}
		sourceStopped := make(chan struct{})
		result := make(chan error, 1)
		go func() {
			_, err := cache.resolve(t.Context(), key, provider, false, func(ctx context.Context) (credentials.Material, error) {
				<-ctx.Done()
				close(sourceStopped)
				return credentials.Material{}, ctx.Err()
			})
			result <- err
		}()
		synctest.Wait()
		cache.close()
		select {
		case <-sourceStopped:
		default:
			t.Fatal("shutdown returned before source work stopped")
		}
		require.ErrorIs(t, <-result, ErrMaterialChanged)
		require.Zero(t, cache.active)
		require.Empty(t, cache.tenants)
		require.Empty(t, cache.entries)
		_, err := cache.resolve(t.Context(), key, provider, false, func(context.Context) (credentials.Material, error) {
			return credentials.Material{}, nil
		})
		require.ErrorIs(t, err, ErrMaterialClosed)
	})
}

func TestManagedMaterialLateSuccessCannotEscapeDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cache := newManagedMaterials()
		defer cache.close()
		provider := syntheticCredentialProvider()
		key := materialIdentity{scope: AccountScope("a"), provider: string(provider.ID)}
		_, err := cache.resolve(t.Context(), key, provider, false, func(ctx context.Context) (credentials.Material, error) {
			<-ctx.Done()
			return credentials.NewMaterial(provider.Credentials.Profiles[0], nil, credentials.MaterialMetadata{}), nil
		})
		require.ErrorIs(t, err, context.DeadlineExceeded)
		require.Empty(t, cache.entries)
		require.Zero(t, cache.bytes)
	})
}

func TestManagedMaterialOutageCannotExtendValidity(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cache := newManagedMaterials()
		defer cache.close()
		provider := syntheticCredentialProvider()
		key := materialIdentity{scope: AccountScope("a"), provider: string(provider.ID)}
		load := func(context.Context) (credentials.Material, error) {
			return credentials.NewMaterial(provider.Credentials.Profiles[0], nil, credentials.MaterialMetadata{Version: "one"}), nil
		}
		original, err := cache.resolve(t.Context(), key, provider, false, load)
		require.NoError(t, err)
		unavailable := errors.New("fixture repository unavailable")
		failed := func(context.Context) (credentials.Material, error) { return credentials.Material{}, unavailable }
		warm, err := cache.resolve(t.Context(), key, provider, false, failed)
		require.NoError(t, err)
		require.NoError(t, warm.CheckValidity(time.Now()))
		time.Sleep(cache.limits.Validity)
		_, err = cache.resolve(t.Context(), key, provider, false, failed)
		require.ErrorIs(t, err, unavailable)
		require.Error(t, original.CheckValidity(time.Now()))
		require.Empty(t, cache.entries)
		recovered, err := cache.resolve(t.Context(), key, provider, false, load)
		require.NoError(t, err)
		require.NoError(t, recovered.CheckValidity(time.Now()))
		require.Error(t, original.CheckValidity(time.Now()), "recovery must not renew an issued handle")
	})
}
