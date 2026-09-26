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
	cases := []struct {
		name   string
		err    error
		retain bool
	}{
		{"unavailable", credentials.NewSourceError(credentials.SourceErrorUnavailable, "stored"), true},
		{"canceled", context.Canceled, true},
		{"deadline", context.DeadlineExceeded, true},
		{"denied", credentials.NewSourceError(credentials.SourceErrorDenied, "stored"), false},
		{"invalid", credentials.NewSourceError(credentials.SourceErrorInvalid, "stored"), false},
		{"not_configured", credentials.NewSourceError(credentials.SourceErrorNotConfigured, "stored"), false},
		{"withdrawn", ErrKeyNotFound, false},
		{"unknown", errors.New("unknown source failure"), false},
	}
	for _, scope := range []string{AccountScope("a"), SharedScope} {
		for _, test := range cases {
			t.Run(scope+"/"+test.name, func(t *testing.T) {
				synctest.Test(t, func(t *testing.T) {
					cache := newManagedMaterials()
					defer cache.close()
					provider := syntheticCredentialProvider()
					key := materialIdentity{scope: scope, provider: string(provider.ID), account: "a"}
					load := func(context.Context) (credentials.Material, error) {
						return credentials.NewMaterial(provider.Credentials.Profiles[0], nil, credentials.MaterialMetadata{Version: "one"}), nil
					}
					original, err := cache.resolve(t.Context(), key, provider, false, load)
					require.NoError(t, err)
					deadline := cache.entries[key].deadline
					time.Sleep(cache.limits.Validity / 2)
					failed := func(context.Context) (credentials.Material, error) { return credentials.Material{}, test.err }
					_, err = cache.resolve(t.Context(), key, provider, true, failed)
					require.ErrorIs(t, err, test.err)
					if test.retain {
						require.NoError(t, original.CheckValidity(time.Now()))
						require.Equal(t, deadline, cache.entries[key].deadline)
						require.Equal(t, time.Now().Add(cache.limits.RefreshInterval), cache.entries[key].refreshAt)
						warm, err := cache.resolve(t.Context(), key, provider, false, func(context.Context) (credentials.Material, error) {
							t.Fatal("warm permission consulted the failed source")
							return credentials.Material{}, test.err
						})
						require.NoError(t, err)
						require.NoError(t, warm.CheckValidity(time.Now()))
						_, err = cache.resolve(t.Context(), key, provider, true, failed)
						require.ErrorIs(t, err, test.err)
						require.Equal(t, deadline, cache.entries[key].deadline)
						time.Sleep(time.Until(deadline))
						_, err = cache.resolve(t.Context(), key, provider, false, failed)
						require.ErrorIs(t, err, test.err)
					}
					require.Error(t, original.CheckValidity(time.Now()))
					require.Empty(t, cache.entries)
					recovered, err := cache.resolve(t.Context(), key, provider, false, load)
					require.NoError(t, err)
					require.NoError(t, recovered.CheckValidity(time.Now()))
					require.Error(t, original.CheckValidity(time.Now()), "recovery must not renew an issued handle")
				})
			})
		}
	}
	for _, state := range []string{"revoked", "provider_expired"} {
		t.Run(state, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				cache := newManagedMaterials()
				defer cache.close()
				provider := syntheticCredentialProvider()
				key := materialIdentity{scope: SharedScope, provider: string(provider.ID), account: "a"}
				original, err := cache.resolve(t.Context(), key, provider, false, func(context.Context) (credentials.Material, error) {
					return credentials.NewMaterial(provider.Credentials.Profiles[0], nil, credentials.MaterialMetadata{
						Version: "one", ExpiresAt: time.Now().Add(cache.limits.Validity / 4),
					}), nil
				})
				require.NoError(t, err)
				if state == "revoked" {
					original.Validity().Revoke()
				} else {
					time.Sleep(cache.limits.Validity / 4)
				}
				unavailable := credentials.NewSourceError(credentials.SourceErrorUnavailable, "stored")
				_, err = cache.resolve(t.Context(), key, provider, true, func(context.Context) (credentials.Material, error) {
					return credentials.Material{}, unavailable
				})
				require.ErrorIs(t, err, unavailable)
				require.Error(t, original.CheckValidity(time.Now()))
				require.Empty(t, cache.entries)
			})
		})
	}

	t.Run("withdrawal_at_load_deadline", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			cache := newManagedMaterials()
			defer cache.close()
			cache.limits.LoadTimeout = cache.limits.Validity / 4
			provider := syntheticCredentialProvider()
			key := materialIdentity{scope: SharedScope, provider: string(provider.ID), account: "a"}
			original, err := cache.resolve(t.Context(), key, provider, false, func(context.Context) (credentials.Material, error) {
				return credentials.NewMaterial(provider.Credentials.Profiles[0], nil, credentials.MaterialMetadata{Version: "one"}), nil
			})
			require.NoError(t, err)
			_, err = cache.resolve(t.Context(), key, provider, true, func(ctx context.Context) (credentials.Material, error) {
				<-ctx.Done()
				return credentials.Material{}, ErrKeyNotFound
			})
			require.ErrorIs(t, err, ErrKeyNotFound)
			require.ErrorIs(t, original.CheckValidity(time.Now()), credentials.ErrMaterialRevoked)
			require.Empty(t, cache.entries)
		})
	})

}
