package keyring

import (
	"testing"
	"time"

	"github.com/agentstation/starport/internal/credentials"
	"github.com/stretchr/testify/require"
)

func TestStoredDestinationIdentityTracksSelectedCredential(t *testing.T) {
	provider := syntheticCredentialProvider()
	manager := newSyntheticProviderKeys(t, provider)
	for _, account := range []string{"account-a", "account-b"} {
		_, err := manager.AddKey(t.Context(), AccountScope(account), string(provider.ID), map[string]string{"api-key": "same-secret"}, nil, false, 0)
		require.NoError(t, err)
	}
	first, err := manager.ResolveStoredMaterial(t.Context(), AccountScope("account-a"), provider)
	require.NoError(t, err)
	other, err := manager.ResolveStoredMaterial(t.Context(), AccountScope("account-b"), provider)
	require.NoError(t, err)
	require.NotEmpty(t, first.Handle())
	require.NotEqual(t, first.Handle(), other.Handle())
	require.NotContains(t, first.Handle(), "account-a")
	require.NotContains(t, first.Handle(), "same-secret")
	shared, err := manager.AddSharedCredential(t.Context(), string(provider.ID), map[string]string{"api-key": "same-secret"}, nil, SharedCredentialParams{})
	require.NoError(t, err)
	selected, err := manager.ResolveSharedMaterial(t.Context(), "account-a", provider)
	require.NoError(t, err)
	require.NotEqual(t, first.Handle(), selected.Handle())
	sharedForOther, err := manager.ResolveSharedMaterial(t.Context(), "account-b", provider)
	require.NoError(t, err)
	require.Equal(t, selected.Handle(), sharedForOther.Handle())
	_, err = manager.UpdateSharedCredential(t.Context(), string(provider.ID), shared.ID, SharedCredentialUpdate{Key: map[string]string{"api-key": "rotated-secret"}})
	require.NoError(t, err)
	rotated, err := manager.ResolveSharedMaterial(t.Context(), "account-a", provider)
	require.NoError(t, err)
	require.Equal(t, selected.Handle(), rotated.Handle())
	require.NotEqual(t, selected.Version(), rotated.Version())
	require.ErrorIs(t, selected.CheckValidity(time.Now()), credentials.ErrMaterialRevoked)
}
