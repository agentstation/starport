package keyring

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/agentstation/starmap/pkg/catalogs"

	"github.com/agentstation/starport/internal/credentials"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSyntheticCatalogProviderOperatorSurfaces(t *testing.T) {
	ctx := t.Context()
	provider := syntheticCredentialProvider()
	manager := newSyntheticProviderKeys(t, provider)

	for account, secret := range map[string]string{"account-a": "secret-a", "account-b": "secret-b"} {
		_, err := manager.AddKey(ctx, AccountScope(account), string(provider.ID), map[string]string{"api-key": secret}, nil, false, 0)
		require.NoError(t, err)
	}
	_, err := manager.AddSharedCredential(ctx, string(provider.ID), map[string]string{"api-key": "global-secret"}, nil, SharedCredentialParams{})
	require.NoError(t, err)

	var wait sync.WaitGroup
	errors := make(chan error, 2)
	for account, want := range map[string]string{"account-a": "secret-a", "account-b": "secret-b"} {
		account, want := account, want
		wait.Add(1)
		go func() {
			defer wait.Done()
			for range 100 {
				material, resolveErr := manager.ResolveStoredMaterial(ctx, AccountScope(account), provider)
				if resolveErr != nil {
					errors <- resolveErr
					return
				}
				value, exists := material.Value("api-key")
				if !exists || value != want {
					errors <- fmt.Errorf("account %s resolved another account's credential", account)
					return
				}
			}
		}()
	}
	wait.Wait()
	close(errors)
	for resolveErr := range errors {
		require.NoError(t, resolveErr)
	}

	missing, err := manager.GetKeys(ctx, AccountScope("missing"), string(provider.ID))
	require.NoError(t, err)
	require.Empty(t, missing, "an exact account lookup must not merge global material")
}

// newSyntheticProviderKeys builds a manager whose validator knows exactly one
// synthetic provider, so shared-credential resolution runs the real
// catalog-contract path.
func newSyntheticProviderKeys(t *testing.T, provider catalogs.Provider) ProviderKeys {
	t.Helper()
	store := storage.NewMockStore()
	repository, err := credentials.Open(store)
	require.NoError(t, err)
	validator, err := NewCatalogCredentialValidator(func(id catalogs.ProviderID) (catalogs.Provider, bool) {
		return provider, id == provider.ID
	})
	require.NoError(t, err)
	masterKey, err := credentials.GenerateMasterKey()
	require.NoError(t, err)
	manager, err := NewProviderKeys(repository, masterKey, validator)
	require.NoError(t, err)
	return manager
}

func syntheticCredentialProvider() catalogs.Provider {
	return catalogs.Provider{
		ID: "acme",
		Credentials: &catalogs.ProviderCredentials{
			Fields: []catalogs.ProviderCredentialField{{
				ID: "api-key", Kind: catalogs.ProviderCredentialFieldSecret, Required: true,
			}},
			Profiles: []catalogs.ProviderCredentialProfile{{
				ID: "api-key", Primitive: catalogs.ProviderAuthenticationAPIKey,
				Fields: []catalogs.ProviderCredentialFieldID{"api-key"},
				Placements: []catalogs.ProviderCredentialPlacement{{
					Field: "api-key", Kind: catalogs.ProviderCredentialPlacementHeader,
					Name: "Authorization", Scheme: catalogs.ProviderCredentialSchemeBearer,
				}},
			}},
			Inference: catalogs.ProviderCredentialPlane{
				Required: true, Alternatives: []catalogs.ProviderCredentialProfileID{"api-key"},
			},
		},
	}
}

// TestListKeys tests the ListKeys function
func TestListKeys(t *testing.T) {
	ctx := context.Background()
	store := storage.NewMockStore()
	masterKey, _ := credentials.GenerateMasterKey()
	manager, err := newTestProviderKeys(store, masterKey)
	require.NoError(t, err)

	// Skip validation
	ctx = context.WithValue(ctx, "skip_validation", true)

	scope := AccountScope("test-key")

	// Add multiple keys with proper format
	keys := map[string]map[string]string{
		"openai":    {"api-key": "sk-test123"},
		"anthropic": {"api-key": "sk-ant-test123"},
		"groq":      {"api-key": "gsk_test123"},
	}
	for provider, key := range keys {
		_, err := manager.AddKey(ctx, scope, provider, key, nil, false, 0)
		require.NoError(t, err)
	}

	// List all keys
	keyList, err := manager.ListKeys(ctx, scope)
	assert.NoError(t, err)
	assert.Len(t, keyList, 3)

	// Verify providers
	foundProviders := make(map[string]bool)
	for _, key := range keyList {
		foundProviders[key.Provider] = true
		// Verify EncryptedCredential is not empty
		assert.NotEmpty(t, key.EncryptedCredential)
	}

	for provider := range keys {
		assert.True(t, foundProviders[provider], "Provider %s should be in list", provider)
	}

	// Test with non-existent scope
	keyList2, err := manager.ListKeys(ctx, AccountScope("non-existent"))
	assert.NoError(t, err)
	assert.Len(t, keyList2, 0)

	// Test with empty scope
	_, err = manager.ListKeys(ctx, "")
	assert.Error(t, err)
}

// TestSharedCredentialOperations covers the argument contract of the
// shared-credential surface.
func TestSharedCredentialOperations(t *testing.T) {
	ctx := context.Background()
	store := storage.NewMockStore()
	masterKey, _ := credentials.GenerateMasterKey()
	manager, err := newTestProviderKeys(store, masterKey)
	require.NoError(t, err)

	// Skip validation
	ctx = context.WithValue(ctx, "skip_validation", true)

	// Every shared operation requires a provider.
	_, err = manager.AddSharedCredential(ctx, "", map[string]string{"api-key": "sk-test"}, nil, SharedCredentialParams{})
	assert.ErrorIs(t, err, ErrProviderRequired)
	_, err = manager.GetSharedCredentials(ctx, "")
	assert.ErrorIs(t, err, ErrProviderRequired)
	err = manager.DeleteSharedCredential(ctx, "", "some-id")
	assert.ErrorIs(t, err, ErrProviderRequired)

	// An invalid key is refused before anything is stored.
	_, err = manager.AddSharedCredential(ctx, "openai", map[string]string{}, nil, SharedCredentialParams{})
	assert.Error(t, err)

	// An unknown access word is refused.
	_, err = manager.AddSharedCredential(ctx, "openai", map[string]string{"api-key": "sk-test"}, nil,
		SharedCredentialParams{Access: "everyone"})
	assert.ErrorIs(t, err, credentials.ErrInvalidAccess)

	// A provider with no shared record lists empty rather than failing.
	shared, err := manager.GetSharedCredentials(ctx, "non-existent")
	assert.NoError(t, err)
	assert.Empty(t, shared)

	// Deleting or updating an absent credential reports not found.
	err = manager.DeleteSharedCredential(ctx, "non-existent", "some-id")
	assert.ErrorIs(t, err, ErrKeyNotFound)
	_, err = manager.UpdateSharedCredential(ctx, "non-existent", "some-id", SharedCredentialUpdate{})
	assert.ErrorIs(t, err, ErrKeyNotFound)
}

// TestSharedScopeRefusesAccountOperations pins the seam split: the account
// methods never read or write the shared record, whose shape is a list.
func TestSharedScopeRefusesAccountOperations(t *testing.T) {
	ctx, _, manager := newProviderCredentialFixture(t)

	_, err := manager.AddKey(ctx, SharedScope, "openai", map[string]string{"api-key": "sk-test"}, nil, false, 0)
	assert.ErrorIs(t, err, ErrScopeIsShared)
	_, err = manager.UpdateKey(ctx, SharedScope, "openai", nil, map[string]any{"region": "test"}, nil, nil)
	assert.ErrorIs(t, err, ErrScopeIsShared)
	err = manager.DeleteKey(ctx, SharedScope, "openai")
	assert.ErrorIs(t, err, ErrScopeIsShared)
	_, err = manager.ResolveStoredMaterial(ctx, SharedScope, syntheticCredentialProvider())
	assert.ErrorIs(t, err, ErrScopeIsShared)
}

// TestOperatorStoresManySharedCredentialsForOneProvider is the CSH-C1
// fail-before case. On the baseline the shared record held one credential, so
// an operator's second key for the same provider drew a revision conflict.
func TestOperatorStoresManySharedCredentialsForOneProvider(t *testing.T) {
	ctx, _, manager := newProviderCredentialFixture(t)

	first, err := manager.AddSharedCredential(ctx, "openai",
		map[string]string{"api-key": "sk-first"}, nil, SharedCredentialParams{Label: "team-a"})
	require.NoError(t, err)
	second, err := manager.AddSharedCredential(ctx, "openai",
		map[string]string{"api-key": "sk-second"}, nil, SharedCredentialParams{Label: "team-b"})
	require.NoError(t, err)
	require.NotEqual(t, first.ID, second.ID)

	shared, err := manager.GetSharedCredentials(ctx, "openai")
	require.NoError(t, err)
	require.Len(t, shared, 2)
	assert.Equal(t, []string{"team-a", "team-b"}, []string{shared[0].Label, shared[1].Label},
		"the list keeps stored order")
	for _, credential := range shared {
		assert.Equal(t, credentials.AccessOpen, credential.Access,
			"a credential added without an access choice is open to every account")
	}
}

func TestListSharedReadsCanonicalRecord(t *testing.T) {
	ctx, _, manager := newProviderCredentialFixture(t)
	addSharedProviderCredential(t, ctx, manager)

	keys, err := manager.ListShared(ctx)
	require.NoError(t, err)
	require.Len(t, keys, 1)
	assert.Equal(t, "openai", keys[0].Provider)
	require.Len(t, keys[0].Shared, 1)
}

func TestProviderCredentialRepositoryContract(t *testing.T) {
	assert.Equal(t, 1, credentials.ProviderCredentialStorageSchemaVersion)
	assert.Equal(t, "credentials:v1:scope:Kg:", credentials.ScopePrefix("*"))
	assert.Equal(t, "credentials:v1:scope:Kg:provider:b3BlbmFp", credentials.StorageKey("*", "openai"))

	ctx, store, manager := newProviderCredentialFixture(t)
	created := addSharedProviderCredential(t, ctx, manager)

	storageKeys, err := store.ScanWithPrefix(ctx, credentials.ScopePrefix("*"), providerCredentialScanLimit)
	require.NoError(t, err)
	assert.Equal(t, []string{credentials.StorageKey("*", "openai")}, storageKeys)

	shared, err := manager.GetSharedCredentials(ctx, "openai")
	require.NoError(t, err)
	require.Len(t, shared, 1)
	assert.Equal(t, created.ID, shared[0].ID)

	updated, err := manager.UpdateSharedCredential(ctx, "openai", created.ID,
		SharedCredentialUpdate{Config: map[string]any{"region": "test"}})
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"region": "test"}, updated.Config)

	// Deleting the last shared credential removes the provider's record.
	require.NoError(t, manager.DeleteSharedCredential(ctx, "openai", created.ID))
	shared, err = manager.GetSharedCredentials(ctx, "openai")
	require.NoError(t, err)
	assert.Empty(t, shared)
	storageKeys, err = store.ScanWithPrefix(ctx, credentials.ScopePrefix("*"), providerCredentialScanLimit)
	require.NoError(t, err)
	assert.Empty(t, storageKeys)
}

func newProviderCredentialFixture(t *testing.T) (context.Context, *storage.MockStore, ProviderKeys) {
	t.Helper()
	ctx := context.WithValue(context.Background(), "skip_validation", true)
	store := storage.NewMockStore()
	masterKey, err := credentials.GenerateMasterKey()
	require.NoError(t, err)

	manager, err := newTestProviderKeys(store, masterKey)
	require.NoError(t, err)
	return ctx, store, manager
}

func addSharedProviderCredential(t *testing.T, ctx context.Context, manager ProviderKeys) *credentials.SharedCredential {
	t.Helper()
	created, err := manager.AddSharedCredential(ctx, "openai", map[string]string{"api-key": "sk-test"}, nil, SharedCredentialParams{})
	require.NoError(t, err)
	return created
}

// TestGrantedSharedCredentialResolution proves the per-credential sharing
// choice: a granted credential serves only its grantees, an open credential
// serves everyone, and resolution walks the list in stored order.
func TestGrantedSharedCredentialResolution(t *testing.T) {
	ctx := t.Context()
	provider := syntheticCredentialProvider()
	manager := newSyntheticProviderKeys(t, provider)

	granted, err := manager.AddSharedCredential(ctx, string(provider.ID),
		map[string]string{"api-key": "secret-granted"}, nil,
		SharedCredentialParams{Access: credentials.AccessGranted, Grants: []string{"account-a"}})
	require.NoError(t, err)
	open, err := manager.AddSharedCredential(ctx, string(provider.ID),
		map[string]string{"api-key": "secret-open"}, nil, SharedCredentialParams{})
	require.NoError(t, err)

	resolveSecret := func(accountID string) string {
		material, resolveErr := manager.ResolveSharedMaterial(ctx, accountID, provider)
		require.NoError(t, resolveErr)
		value, exists := material.Value("api-key")
		require.True(t, exists)
		return value
	}

	assert.Equal(t, "secret-granted", resolveSecret("account-a"),
		"a granted account spends the first credential granted to it")
	assert.Equal(t, "secret-open", resolveSecret("account-b"),
		"an ungranted account falls through to the open credential")
	assert.Equal(t, "secret-open", resolveSecret(""),
		"an anonymous caller may spend only an open credential")

	require.NoError(t, manager.DeleteSharedCredential(ctx, string(provider.ID), open.ID))
	_, err = manager.ResolveSharedMaterial(ctx, "account-b", provider)
	assert.ErrorIs(t, err, ErrKeyNotFound,
		"with only a granted credential left, an ungranted account gets nothing")
	assert.Equal(t, "secret-granted", resolveSecret("account-a"))

	// Revoking the grant closes the last door.
	noGrants := []string{}
	_, err = manager.UpdateSharedCredential(ctx, string(provider.ID), granted.ID,
		SharedCredentialUpdate{Grants: &noGrants})
	require.NoError(t, err)
	_, err = manager.ResolveSharedMaterial(ctx, "account-a", provider)
	assert.ErrorIs(t, err, ErrKeyNotFound)
}

// TestRecordUsageErrors tests error cases in RecordUsage
func TestRecordUsageErrors(t *testing.T) {
	ctx := context.Background()
	store := storage.NewMockStore()
	masterKey, _ := credentials.GenerateMasterKey()
	manager, err := newTestProviderKeys(store, masterKey)
	require.NoError(t, err)

	// Try to record usage for non-existent key
	usage := &Usage{
		Provider:         "openai",
		Model:            "gpt-4",
		PromptTokens:     100,
		CompletionTokens: 50,
	}

	err = manager.RecordUsage(ctx, AccountScope("non-existent"), "openai", usage)
	assert.ErrorIs(t, err, ErrKeyNotFound)
}

// TestValidateKeyEdgeCases tests edge cases in validation
func TestValidateKeyEdgeCases(t *testing.T) {
	store := storage.NewMockStore()
	masterKey, _ := credentials.GenerateMasterKey()
	manager, err := newTestProviderKeys(store, masterKey)
	require.NoError(t, err)

	ctx := context.WithValue(context.Background(), "skip_validation", true)

	tests := []struct {
		name     string
		provider string
		key      map[string]string
		config   map[string]any
		wantErr  bool
	}{
		{
			name:     "Empty OpenAI API key",
			provider: "openai",
			key:      map[string]string{"api-key": ""},
			wantErr:  true,
		},
		{
			name:     "Empty Anthropic API key",
			provider: "anthropic",
			key:      map[string]string{"api-key": ""},
			wantErr:  true,
		},
		{
			name:     "Empty Azure API key",
			provider: "azure-openai",
			key:      map[string]string{"api-key": ""},
			wantErr:  true,
		},
		{
			name:     "Empty Google AI Studio API key",
			provider: "google-ai-studio",
			key:      map[string]string{"api-key": ""},
			wantErr:  true,
		},
		{
			name:     "Empty Vertex AI access token",
			provider: "google-vertex",
			key:      map[string]string{"access-token": ""},
			wantErr:  true,
		},
		{
			name:     "Empty Groq API key",
			provider: "groq",
			key:      map[string]string{"api-key": ""},
			wantErr:  true,
		},
		{
			name:     "Empty Mistral API key",
			provider: "mistral",
			key:      map[string]string{"api-key": ""},
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := manager.ValidateKey(ctx, tt.provider, tt.key, tt.config)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// TestGetKeysWithErrors tests error handling in GetKeys
func TestGetKeysWithErrors(t *testing.T) {
	ctx := context.Background()
	store := storage.NewMockStore()
	masterKey, _ := credentials.GenerateMasterKey()
	manager, err := newTestProviderKeys(store, masterKey)
	require.NoError(t, err)

	// Test with empty scope
	_, err = manager.GetKeys(ctx, "", "openai")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "scope and provider are required")

	// Test with empty provider
	_, err = manager.GetKeys(ctx, AccountScope("test-key"), "")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "scope and provider are required")
}

// TestUpdateKeyEdgeCases tests edge cases in UpdateKey
func TestUpdateKeyEdgeCases(t *testing.T) {
	ctx := context.Background()
	store := storage.NewMockStore()
	masterKey, _ := credentials.GenerateMasterKey()
	manager, err := newTestProviderKeys(store, masterKey)
	require.NoError(t, err)

	// Skip validation
	ctx = context.WithValue(ctx, "skip_validation", true)

	// Add initial key
	scope := AccountScope("test-key")
	provider := "openai"
	_, err = manager.AddKey(ctx, scope, provider, map[string]string{"api-key": "sk-initial"}, nil, false, 0)
	require.NoError(t, err)

	// Update with only config (no new key)
	newConfig := map[string]any{"model": "gpt-4"}
	updatedKey, err := manager.UpdateKey(ctx, scope, provider, nil, newConfig, nil, nil)
	assert.NoError(t, err)
	assert.Equal(t, newConfig, updatedKey.Config)

	// Verify key data was not changed
	encService, err := credentials.NewEncryptionService(masterKey)
	require.NoError(t, err)
	decrypted, err := encService.DecryptCredential(updatedKey.EncryptedCredential)
	require.NoError(t, err)
	assert.Contains(t, decrypted, "sk-initial")

	// Update with a field outside the catalog credential contract.
	_, err = manager.UpdateKey(ctx, scope, provider, map[string]string{"not-declared": "invalid"}, nil, nil, nil)
	assert.Error(t, err)
}

// TestRotateEncryptionKey tests the unimplemented key rotation
func TestRotateEncryptionKey(t *testing.T) {
	ctx := context.Background()
	store := storage.NewMockStore()
	masterKey, _ := credentials.GenerateMasterKey()
	manager, err := newTestProviderKeys(store, masterKey)
	require.NoError(t, err)

	err = manager.RotateEncryptionKey(ctx)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "key rotation not implemented")
}
