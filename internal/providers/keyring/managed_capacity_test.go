package keyring

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/agentstation/starport/internal/credentials"
	"github.com/stretchr/testify/require"
)

func TestManagedMaterialEntryLimitAndExpiryEviction(t *testing.T) {
	cache := newManagedMaterials()
	cache.limits.Entries = 1
	now := time.Now()
	cache.now = func() time.Time { return now }
	provider := syntheticCredentialProvider()
	load := func(context.Context) (credentials.Material, error) {
		return credentials.NewMaterial(provider.Credentials.Profiles[0], nil, credentials.MaterialMetadata{}), nil
	}
	first, err := cache.resolve(t.Context(), materialIdentity{scope: "account:a", provider: "one"}, provider, false, load)
	require.NoError(t, err)
	_, err = cache.resolve(t.Context(), materialIdentity{scope: "account:b", provider: "two"}, provider, false, func(context.Context) (credentials.Material, error) {
		return credentials.Material{}, errors.New("entry capacity triggered a source load")
	})
	require.ErrorIs(t, err, ErrMaterialCapacity)
	now = now.Add(cache.limits.Validity)
	_, err = cache.resolve(t.Context(), materialIdentity{scope: "account:b", provider: "two"}, provider, false, load)
	require.NoError(t, err)
	require.ErrorIs(t, first.CheckValidity(now), credentials.ErrMaterialRevoked)
}

func TestManagedMaterialSecretByteLimitAndReclamation(t *testing.T) {
	cache := newManagedMaterials()
	cache.limits.SecretBytes = 4
	provider := syntheticCredentialProvider()
	load := func(context.Context) (credentials.Material, error) {
		return credentials.NewMaterial(provider.Credentials.Profiles[0], map[catalogs.ProviderCredentialFieldID]string{"k": "123"}, credentials.MaterialMetadata{}), nil
	}
	first := materialIdentity{scope: "account:a", provider: "one"}
	_, err := cache.resolve(t.Context(), first, provider, false, load)
	require.NoError(t, err)
	_, err = cache.resolve(t.Context(), materialIdentity{scope: "account:b", provider: "two"}, provider, false, load)
	require.ErrorIs(t, err, ErrMaterialCapacity)
	cache.invalidate(first.scope, first.provider)
	_, err = cache.resolve(t.Context(), materialIdentity{scope: "account:b", provider: "two"}, provider, false, load)
	require.NoError(t, err)
}

func TestManagedMaterialIdleEvictionRevokesOldHandles(t *testing.T) {
	manager := newSyntheticProviderKeys(t, syntheticCredentialProvider()).(*keyManager)
	now := time.Now()
	manager.materials.now = func() time.Time { return now }
	provider := syntheticCredentialProvider()
	_, err := manager.AddKey(t.Context(), AccountScope("a"), string(provider.ID), map[string]string{"api-key": "fixture-secret"}, nil, false, 0)
	require.NoError(t, err)
	material, err := manager.ResolveStoredMaterial(t.Context(), AccountScope("a"), provider)
	require.NoError(t, err)
	now = now.Add(manager.materials.limits.Idle)
	manager.refreshMaterials(t.Context())
	require.ErrorIs(t, material.CheckValidity(now), credentials.ErrMaterialRevoked)
	current, err := manager.ResolveStoredMaterial(t.Context(), AccountScope("a"), provider)
	require.NoError(t, err)
	require.NoError(t, current.CheckValidity(now))
}
