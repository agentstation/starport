package keyring

import (
	"context"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/credentials"
	"github.com/stretchr/testify/require"
)

func TestManagedMaterialInvalidationFencesInflightLoad(t *testing.T) {
	cache := newManagedMaterials()
	provider := syntheticCredentialProvider()
	key := materialIdentity{scope: AccountScope("a"), provider: string(provider.ID)}
	started, release := make(chan struct{}), make(chan struct{})
	result := make(chan error, 1)
	go func() {
		_, err := cache.resolve(t.Context(), key, provider, false, func(context.Context) (credentials.Material, error) {
			close(started)
			<-release
			return credentials.NewMaterial(provider.Credentials.Profiles[0], nil, credentials.MaterialMetadata{}), nil
		})
		result <- err
	}()
	<-started
	cache.invalidate(key.scope, key.provider)
	close(release)
	require.ErrorIs(t, <-result, ErrMaterialChanged)
	calls := 0
	_, err := cache.resolve(t.Context(), key, provider, false, func(context.Context) (credentials.Material, error) {
		calls++
		return credentials.NewMaterial(provider.Credentials.Profiles[0], nil, credentials.MaterialMetadata{}), nil
	})
	require.NoError(t, err)
	require.Equal(t, 1, calls)
}

func TestManagedMaterialExpiryAndContractChangeRequireReload(t *testing.T) {
	cache := newManagedMaterials()
	now := time.Now()
	cache.now = func() time.Time { return now }
	provider := syntheticCredentialProvider()
	key := materialIdentity{scope: AccountScope("a"), provider: string(provider.ID)}
	calls := 0
	load := func(context.Context) (credentials.Material, error) {
		calls++
		return credentials.NewMaterial(provider.Credentials.Profiles[0], nil, credentials.MaterialMetadata{}), nil
	}
	_, err := cache.resolve(t.Context(), key, provider, false, load)
	require.NoError(t, err)
	_, err = cache.resolve(t.Context(), key, provider, false, load)
	require.NoError(t, err)
	require.Equal(t, 1, calls)
	now = now.Add(managedMaterialValidity)
	_, err = cache.resolve(t.Context(), key, provider, false, load)
	require.NoError(t, err)
	require.Equal(t, 2, calls)
	provider.Credentials.Profiles[0].ID = "replacement"
	_, err = cache.resolve(t.Context(), key, provider, false, load)
	require.NoError(t, err)
	require.Equal(t, 3, calls)
}

func TestManagedMaterialRejectsExpiredLoad(t *testing.T) {
	cache := newManagedMaterials()
	provider := syntheticCredentialProvider()
	_, err := cache.resolve(t.Context(), materialIdentity{scope: AccountScope("a"), provider: string(provider.ID)}, provider, false, func(context.Context) (credentials.Material, error) {
		return credentials.NewMaterial(provider.Credentials.Profiles[0], nil, credentials.MaterialMetadata{ExpiresAt: time.Now().Add(-time.Second)}), nil
	})
	require.True(t, credentials.IsSourceError(err, credentials.SourceErrorInvalid))
}

func TestManagedMaterialRefreshRejectsExternalGrantWithdrawal(t *testing.T) {
	provider := syntheticCredentialProvider()
	manager := newSyntheticProviderKeys(t, provider).(*keyManager)
	now := time.Now()
	manager.materials.now = func() time.Time { return now }
	_, err := manager.AddSharedCredential(t.Context(), string(provider.ID), map[string]string{"api-key": "fixture-secret"}, nil, SharedCredentialParams{Access: credentials.AccessGranted, Grants: []string{"a"}})
	require.NoError(t, err)
	_, err = manager.ResolveSharedMaterial(t.Context(), "a", provider)
	require.NoError(t, err)
	record, err := manager.repository.Get(t.Context(), SharedScope, string(provider.ID))
	require.NoError(t, err)
	record.Key.Shared[0].Grants = []string{"b"}
	_, err = manager.repository.Update(t.Context(), record.Key, record.Revision)
	require.NoError(t, err)
	now = now.Add(managedMaterialValidity / 2)
	manager.refreshMaterials(t.Context())
	_, err = manager.ResolveSharedMaterial(t.Context(), "a", provider)
	require.ErrorIs(t, err, ErrKeyNotFound)
	_, err = manager.ResolveSharedMaterial(t.Context(), "b", provider)
	require.NoError(t, err)
}

func TestManagedMaterialRefreshCannotRestoreInvalidatedEntry(t *testing.T) {
	cache := newManagedMaterials()
	provider := syntheticCredentialProvider()
	key := materialIdentity{scope: AccountScope("a"), provider: string(provider.ID)}
	calls := 0
	load := func(context.Context) (credentials.Material, error) {
		calls++
		return credentials.NewMaterial(provider.Credentials.Profiles[0], nil, credentials.MaterialMetadata{}), nil
	}
	_, err := cache.resolve(t.Context(), key, provider, false, load)
	require.NoError(t, err)
	cache.invalidate(key.scope, key.provider)
	_, err = cache.resolve(t.Context(), key, provider, true, load)
	require.ErrorIs(t, err, ErrMaterialChanged)
	require.Equal(t, 1, calls)
}

func TestManagedMaterialCapacityDoesNotReadSource(t *testing.T) {
	cache := newManagedMaterials()
	provider := syntheticCredentialProvider()
	cache.active = managedMaterialLoads
	_, err := cache.resolve(t.Context(), materialIdentity{scope: "account:a", provider: string(provider.ID)}, provider, false, func(context.Context) (credentials.Material, error) {
		t.Fatal("capacity refusal read credential source")
		return credentials.Material{}, nil
	})
	require.ErrorIs(t, err, ErrMaterialCapacity)
}

func TestManagedMaterialShutdownRejectsFurtherLoads(t *testing.T) {
	manager := newSyntheticProviderKeys(t, syntheticCredentialProvider()).(*keyManager)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	require.NoError(t, manager.RunMaterialRefresh(ctx))
	_, err := manager.ResolveStoredMaterial(t.Context(), AccountScope("a"), syntheticCredentialProvider())
	require.ErrorIs(t, err, ErrMaterialClosed)
}
