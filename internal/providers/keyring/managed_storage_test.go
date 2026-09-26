package keyring

import (
	"testing"
	"time"

	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/agentstation/starport/internal/credentials"
	"github.com/agentstation/starport/internal/repotest"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

func TestManagedMaterialStorageRotationRevocationAndRecreation(t *testing.T) {
	repotest.Run(t, func(t *testing.T, store storage.KVStore) {
		provider := syntheticCredentialProvider()
		repo, err := credentials.Open(store)
		require.NoError(t, err)
		master, err := credentials.GenerateMasterKey()
		require.NoError(t, err)
		validator, err := NewCatalogCredentialValidator(func(id catalogs.ProviderID) (catalogs.Provider, bool) { return provider, id == provider.ID })
		require.NoError(t, err)
		writer, err := NewProviderKeys(repo, master, validator)
		require.NoError(t, err)
		readerKeys, err := NewProviderKeys(repo, master, validator)
		require.NoError(t, err)
		reader := readerKeys.(*keyManager)
		now := time.Now()
		reader.materials.now = func() time.Time { return now }
		scope := AccountScope("a")
		_, err = writer.AddKey(t.Context(), scope, string(provider.ID), map[string]string{"api-key": "before"}, nil, false, 0)
		require.NoError(t, err)
		old, err := reader.ResolveStoredMaterial(t.Context(), scope, provider)
		require.NoError(t, err)
		_, err = writer.UpdateKey(t.Context(), scope, string(provider.ID), map[string]string{"api-key": "after"}, nil, nil, nil)
		require.NoError(t, err)
		now = now.Add(reader.materials.limits.Validity / 2)
		reader.refreshMaterials(t.Context())
		require.ErrorIs(t, old.CheckValidity(now), credentials.ErrMaterialRevoked)
		current, err := reader.ResolveStoredMaterial(t.Context(), scope, provider)
		require.NoError(t, err)
		value, _ := current.Value("api-key")
		require.Equal(t, "after", value)
		require.NoError(t, writer.DeleteKey(t.Context(), scope, string(provider.ID)))
		now = now.Add(reader.materials.limits.Validity / 2)
		reader.refreshMaterials(t.Context())
		require.ErrorIs(t, current.CheckValidity(now), credentials.ErrMaterialRevoked)
		_, err = reader.ResolveStoredMaterial(t.Context(), scope, provider)
		require.ErrorIs(t, err, ErrKeyNotFound)
		_, err = writer.AddKey(t.Context(), scope, string(provider.ID), map[string]string{"api-key": "recreated"}, nil, false, 0)
		require.NoError(t, err)
		recreated, err := reader.ResolveStoredMaterial(t.Context(), scope, provider)
		require.NoError(t, err)
		value, _ = recreated.Value("api-key")
		require.Equal(t, "recreated", value)
		require.NotEqual(t, old.Version(), recreated.Version())

		shared, err := writer.AddSharedCredential(t.Context(), string(provider.ID), map[string]string{"api-key": "shared"}, nil, SharedCredentialParams{Access: credentials.AccessGranted, Grants: []string{"a"}})
		require.NoError(t, err)
		granted, err := reader.ResolveSharedMaterial(t.Context(), "a", provider)
		require.NoError(t, err)
		grants := []string{"b"}
		_, err = writer.UpdateSharedCredential(t.Context(), string(provider.ID), shared.ID, SharedCredentialUpdate{Grants: &grants})
		require.NoError(t, err)
		now = now.Add(reader.materials.limits.Validity / 2)
		reader.refreshMaterials(t.Context())
		require.ErrorIs(t, granted.CheckValidity(now), credentials.ErrMaterialRevoked)
		_, err = reader.ResolveSharedMaterial(t.Context(), "a", provider)
		require.ErrorIs(t, err, ErrKeyNotFound)
		_, err = reader.ResolveSharedMaterial(t.Context(), "b", provider)
		require.NoError(t, err)
	})
}
