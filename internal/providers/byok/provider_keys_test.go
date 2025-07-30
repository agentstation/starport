package byok

import (
	"context"
	"testing"

	"github.com/agentstation/starport/internal/models"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestListKeys tests the ListKeys function
func TestListKeys(t *testing.T) {
	ctx := context.Background()
	store := storage.NewMockStore()
	masterKey, _ := models.GenerateMasterKey()
	manager, err := NewProviderKeys(store, masterKey)
	require.NoError(t, err)

	// Skip validation
	ctx = context.WithValue(ctx, "skip_validation", true)

	scope := "user:test-key"

	// Add multiple keys with proper format
	keys := map[string]map[string]string{
		"openai":    {"api_key": "sk-test123"},
		"anthropic": {"api_key": "sk-ant-test123"},
		"groq":      {"api_key": "gsk_test123"},
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
	keyList2, err := manager.ListKeys(ctx, "user:non-existent")
	assert.NoError(t, err)
	assert.Len(t, keyList2, 0)

	// Test with empty scope
	_, err = manager.ListKeys(ctx, "")
	assert.Error(t, err)
}

// TestGlobalKeyOperations tests all global key operations
func TestGlobalKeyOperations(t *testing.T) {
	ctx := context.Background()
	store := storage.NewMockStore()
	masterKey, _ := models.GenerateMasterKey()
	manager, err := NewProviderKeys(store, masterKey)
	require.NoError(t, err)

	// Skip validation
	ctx = context.WithValue(ctx, "skip_validation", true)

	// Test adding global key with empty provider
	_, err = manager.AddGlobalKey(ctx, "", map[string]string{"api_key": "sk-test"}, nil, nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "provider is required")

	// Test getting global key with empty provider
	_, err = manager.GetGlobalKey(ctx, "")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "provider is required")

	// Test deleting global key with empty provider
	err = manager.DeleteGlobalKey(ctx, "")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "provider is required")

	// Test with invalid key
	_, err = manager.AddGlobalKey(ctx, "openai", map[string]string{}, nil, nil)
	assert.Error(t, err)

	// Test getting non-existent global key
	_, err = manager.GetGlobalKey(ctx, "non-existent")
	assert.Error(t, err)
	assert.ErrorIs(t, err, storage.ErrNotFound)

	// Test deleting non-existent global key
	err = manager.DeleteGlobalKey(ctx, "non-existent")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "key not found")
}

// TestRecordUsageErrors tests error cases in RecordUsage
func TestRecordUsageErrors(t *testing.T) {
	ctx := context.Background()
	store := storage.NewMockStore()
	masterKey, _ := models.GenerateMasterKey()
	manager, err := NewProviderKeys(store, masterKey)
	require.NoError(t, err)

	// Try to record usage for non-existent key
	usage := &Usage{
		Provider:         "openai",
		Model:            "gpt-4",
		PromptTokens:     100,
		CompletionTokens: 50,
	}

	err = manager.RecordUsage(ctx, "user:non-existent", "openai", usage)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get key")
}

// TestValidateKeyEdgeCases tests edge cases in validation
func TestValidateKeyEdgeCases(t *testing.T) {
	store := storage.NewMockStore()
	masterKey, _ := models.GenerateMasterKey()
	manager, err := NewProviderKeys(store, masterKey)
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
			key:      map[string]string{"api_key": ""},
			wantErr:  true,
		},
		{
			name:     "Empty Anthropic API key",
			provider: "anthropic",
			key:      map[string]string{"api_key": ""},
			wantErr:  true,
		},
		{
			name:     "Empty Azure API key",
			provider: "azure",
			key:      map[string]string{"api_key": ""},
			wantErr:  true,
		},
		{
			name:     "Empty Google AI Studio API key",
			provider: "google-aistudio",
			key:      map[string]string{"api_key": ""},
			wantErr:  true,
		},
		{
			name:     "Empty Vertex AI service account",
			provider: "google-vertexai",
			key:      map[string]string{"service_account_json": ""},
			wantErr:  true,
		},
		{
			name:     "Empty AWS access key",
			provider: "aws-bedrock",
			key: map[string]string{
				"access_key_id":     "",
				"secret_access_key": "secret",
				"region":            "us-east-1",
			},
			wantErr: true,
		},
		{
			name:     "Empty AWS secret key",
			provider: "aws-bedrock",
			key: map[string]string{
				"access_key_id":     "AKIATEST",
				"secret_access_key": "",
				"region":            "us-east-1",
			},
			wantErr: true,
		},
		{
			name:     "Empty AWS region",
			provider: "aws-bedrock",
			key: map[string]string{
				"access_key_id":     "AKIATEST",
				"secret_access_key": "secret",
				"region":            "",
			},
			wantErr: true,
		},
		{
			name:     "Empty Groq API key",
			provider: "groq",
			key:      map[string]string{"api_key": ""},
			wantErr:  true,
		},
		{
			name:     "Empty Mistral API key",
			provider: "mistral",
			key:      map[string]string{"api_key": ""},
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
	masterKey, _ := models.GenerateMasterKey()
	manager, err := NewProviderKeys(store, masterKey)
	require.NoError(t, err)

	// Test with empty scope
	_, err = manager.GetKeys(ctx, "", "openai")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "scope and provider are required")

	// Test with empty provider
	_, err = manager.GetKeys(ctx, "user:test-key", "")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "scope and provider are required")
}

// TestUpdateKeyEdgeCases tests edge cases in UpdateKey
func TestUpdateKeyEdgeCases(t *testing.T) {
	ctx := context.Background()
	store := storage.NewMockStore()
	masterKey, _ := models.GenerateMasterKey()
	manager, err := NewProviderKeys(store, masterKey)
	require.NoError(t, err)

	// Skip validation
	ctx = context.WithValue(ctx, "skip_validation", true)

	// Add initial key
	scope := "user:test-key"
	provider := "openai"
	_, err = manager.AddKey(ctx, scope, provider, map[string]string{"api_key": "sk-initial"}, nil, false, 0)
	require.NoError(t, err)

	// Update with only config (no new key)
	newConfig := map[string]any{"model": "gpt-4"}
	updatedKey, err := manager.UpdateKey(ctx, scope, provider, nil, newConfig, nil, nil)
	assert.NoError(t, err)
	assert.Equal(t, newConfig, updatedKey.Config)

	// Verify key data was not changed
	encService, err := models.NewEncryptionService(masterKey)
	require.NoError(t, err)
	decrypted, err := encService.DecryptCredential(updatedKey.EncryptedCredential)
	require.NoError(t, err)
	assert.Contains(t, decrypted, "sk-initial")

	// Update with invalid new key
	_, err = manager.UpdateKey(ctx, scope, provider, map[string]string{"api_key": "invalid"}, nil, nil, nil)
	assert.Error(t, err)
}

// TestRotateEncryptionKey tests the unimplemented key rotation
func TestRotateEncryptionKey(t *testing.T) {
	ctx := context.Background()
	store := storage.NewMockStore()
	masterKey, _ := models.GenerateMasterKey()
	manager, err := NewProviderKeys(store, masterKey)
	require.NoError(t, err)

	err = manager.RotateEncryptionKey(ctx)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "key rotation not implemented")
}

// TestCostCalculationEdgeCases tests edge cases in cost calculation
func TestCostCalculationEdgeCases(t *testing.T) {
	store := storage.NewMockStore()
	masterKey, _ := models.GenerateMasterKey()
	manager, err := NewProviderKeys(store, masterKey)
	require.NoError(t, err)

	tests := []struct {
		name  string
		usage *Usage
	}{
		{
			name: "Unknown provider",
			usage: &Usage{
				Provider:    "unknown",
				Model:       "some-model",
				TotalTokens: 1000,
			},
		},
		{
			name: "Zero tokens",
			usage: &Usage{
				Provider:         "openai",
				Model:            "gpt-4",
				PromptTokens:     0,
				CompletionTokens: 0,
			},
		},
		{
			name: "Audio usage",
			usage: &Usage{
				Provider:     "openai",
				Model:        "whisper",
				AudioSeconds: 60.5,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cost := manager.CalculateProviderKeyCost(tt.usage)
			assert.GreaterOrEqual(t, cost, 0.0, "Cost should never be negative")
		})
	}
}
