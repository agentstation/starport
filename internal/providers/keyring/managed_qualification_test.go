package keyring

import (
	"encoding/json/v2"
	"fmt"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/credentials"
	"github.com/stretchr/testify/require"
)

func TestManagedMaterialRefreshServesOldestScopesWithoutStarvation(t *testing.T) {
	provider := syntheticCredentialProvider()
	reader := newSyntheticProviderKeys(t, provider).(*keyManager)
	defer reader.materials.close()
	writer := *reader
	writer.materials = newManagedMaterials()
	defer writer.materials.close()
	reader.materials.limits.ConcurrentLoads = 2
	now := time.Now()
	reader.materials.now = func() time.Time { return now }
	handles := make([]credentials.Material, 5)
	for i := range handles {
		scope := AccountScope(fmt.Sprintf("account-%d", i))
		_, err := writer.AddKey(t.Context(), scope, string(provider.ID), map[string]string{"api-key": "before"}, nil, false, 0)
		require.NoError(t, err)
		handles[i], err = reader.ResolveStoredMaterial(t.Context(), scope, provider)
		require.NoError(t, err)
		now = now.Add(100 * time.Millisecond)
		_, err = writer.UpdateKey(t.Context(), scope, string(provider.ID), map[string]string{"api-key": "after"}, nil, nil, nil)
		require.NoError(t, err)
	}
	now = now.Add(2500 * time.Millisecond)
	for turn := range 3 {
		reader.refreshMaterials(t.Context())
		for i, handle := range handles {
			if i < min((turn+1)*2, len(handles)) {
				require.ErrorIs(t, handle.CheckValidity(now), credentials.ErrMaterialRevoked)
			} else {
				require.NoError(t, handle.CheckValidity(now))
			}
		}
	}
}

func TestManagedMaterialCachePreservesCiphertextAndRedaction(t *testing.T) {
	provider := syntheticCredentialProvider()
	manager := newSyntheticProviderKeys(t, provider).(*keyManager)
	defer manager.materials.close()
	const secret = "fixture-managed-sensitive-material"
	scope := AccountScope("disclosure")
	_, err := manager.AddKey(t.Context(), scope, string(provider.ID), map[string]string{"api-key": secret}, nil, false, 0)
	require.NoError(t, err)
	before, err := manager.repository.Get(t.Context(), scope, string(provider.ID))
	require.NoError(t, err)
	material, err := manager.ResolveStoredMaterial(t.Context(), scope, provider)
	require.NoError(t, err)
	after, err := manager.repository.Get(t.Context(), scope, string(provider.ID))
	require.NoError(t, err)
	require.Equal(t, before, after, "cache population must not rewrite encrypted records")
	for _, value := range []any{after, material.Profile()} {
		encoded, err := json.Marshal(value)
		require.NoError(t, err)
		require.NotContains(t, string(encoded), secret)
	}
	encoded, marshalErr := json.Marshal(material)
	require.Error(t, marshalErr)
	require.NotContains(t, string(encoded), secret)
	require.NotContains(t, marshalErr.Error(), secret)
	require.NotContains(t, fmt.Sprintf("%v %+v %#v", material, material, material), secret)
	value, ok := material.Value("api-key")
	require.True(t, ok)
	require.Equal(t, secret, value)
}

func TestManagedMaterialOwnerAndEncryptionManagerIsolation(t *testing.T) {
	provider := syntheticCredentialProvider()
	manager := newSyntheticProviderKeys(t, provider).(*keyManager)
	defer manager.materials.close()
	for _, owner := range []string{"a", "b"} {
		_, err := manager.AddKey(t.Context(), AccountScope(owner), string(provider.ID), map[string]string{"api-key": "secret-" + owner}, nil, false, 0)
		require.NoError(t, err)
	}
	for _, owner := range []string{"a", "b", "a"} {
		material, err := manager.ResolveStoredMaterial(t.Context(), AccountScope(owner), provider)
		require.NoError(t, err)
		value, _ := material.Value("api-key")
		require.Equal(t, "secret-"+owner, value)
	}
	otherMaster, err := credentials.GenerateMasterKey()
	require.NoError(t, err)
	other, err := NewProviderKeys(manager.repository, otherMaster, manager.validator)
	require.NoError(t, err)
	defer other.(*keyManager).materials.close()
	_, err = other.ResolveStoredMaterial(t.Context(), AccountScope("a"), provider)
	require.Error(t, err, "another encryption manager must not reuse decrypted material")
}
