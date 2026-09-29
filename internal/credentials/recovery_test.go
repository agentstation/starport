package credentials

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRecoveryCredentialsChecksEveryRetainedSecretWithoutCatalog(t *testing.T) {
	encryption, err := NewEncryptionService([]byte(strings.Repeat("k", 32)))
	require.NoError(t, err)
	valid, err := encryption.EncryptCredential(`{"api_key":"private-test-value"}`)
	require.NoError(t, err)
	invalid, err := encryption.EncryptCredential(`{"api_key":17}`)
	require.NoError(t, err)
	for _, mode := range []string{"account", "shared", "wrong-key", "wrong-identity", "broken-second-shared", "invalid-plaintext"} {
		t.Run(mode, func(t *testing.T) {
			provider := ProviderKey{Scope: "account", Provider: "retired-provider", EncryptedCredential: valid}
			wanted := 1
			if mode == "shared" || mode == "broken-second-shared" {
				provider.Scope, provider.EncryptedCredential = SharedScope, ""
				provider.Shared = []SharedCredential{{ID: "one", EncryptedCredential: valid, Access: AccessOpen}, {ID: "two", EncryptedCredential: valid, Access: AccessGranted}}
				wanted = 2
			}
			if mode == "broken-second-shared" {
				provider.Shared[1].EncryptedCredential = "private-invalid-ciphertext"
			}
			if mode == "invalid-plaintext" {
				provider.EncryptedCredential = invalid
			}
			selected := encryption
			if mode == "wrong-key" {
				selected, err = NewEncryptionService([]byte(strings.Repeat("z", 32)))
				require.NoError(t, err)
			}
			key := StorageKey(provider.Scope, provider.Provider)
			if mode == "wrong-identity" {
				key = StorageKey("someone-else", provider.Provider)
			}
			data, err := json.Marshal(providerCredentialRecord{SchemaVersion: ProviderCredentialStorageSchemaVersion, Revision: 1, Key: provider})
			require.NoError(t, err)
			count, err := VerifyRecoveryRecord(t.Context(), key, data, selected)
			if mode == "account" || mode == "shared" {
				require.NoError(t, err)
				require.Equal(t, wanted, count)
			} else {
				require.ErrorIs(t, err, ErrRecoveryCredential)
				require.Zero(t, count)
				require.NotContains(t, err.Error(), "private-")
			}
		})
	}
}
