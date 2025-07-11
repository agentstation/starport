package byok

import (
	"context"
	"testing"

	"github.com/agentstation/starport/internal/models"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestListCredentials tests the ListCredentials function
func TestListCredentials(t *testing.T) {
	ctx := context.Background()
	store := storage.NewMockStore()
	masterKey, _ := models.GenerateMasterKey()
	manager, err := NewManager(store, masterKey)
	require.NoError(t, err)

	// Skip validation
	ctx = context.WithValue(ctx, "skip_validation", true)

	apiKeyID := "test-key"

	// Add multiple credentials with proper format
	credentials := map[string]map[string]string{
		"openai":    {"api_key": "sk-test123"},
		"anthropic": {"api_key": "sk-ant-test123"},
		"groq":      {"api_key": "gsk_test123"},
	}
	for provider, cred := range credentials {
		err := manager.AddCredential(ctx, apiKeyID, provider, cred, nil)
		require.NoError(t, err)
	}

	// List all credentials
	credList, err := manager.ListCredentials(ctx, apiKeyID)
	assert.NoError(t, err)
	assert.Len(t, credList, 3)

	// Verify providers
	foundProviders := make(map[string]bool)
	for _, cred := range credList {
		foundProviders[cred.Provider] = true
		// Verify Data field is nil in listing (not decrypted)
		assert.Nil(t, cred.Data)
	}

	for provider := range credentials {
		assert.True(t, foundProviders[provider], "Provider %s should be in list", provider)
	}

	// Test with non-existent API key
	credList2, err := manager.ListCredentials(ctx, "non-existent")
	assert.NoError(t, err)
	assert.Len(t, credList2, 0)

	// Test with empty API key
	_, err = manager.ListCredentials(ctx, "")
	assert.Error(t, err)
}

// TestDefaultKeyOperations tests all default key operations
func TestDefaultKeyOperations(t *testing.T) {
	ctx := context.Background()
	store := storage.NewMockStore()
	masterKey, _ := models.GenerateMasterKey()
	manager, err := NewManager(store, masterKey)
	require.NoError(t, err)

	// Skip validation
	ctx = context.WithValue(ctx, "skip_validation", true)

	// Test setting global credential with empty provider
	err = manager.SetGlobalCredential(ctx, "", map[string]string{"api_key": "sk-test"}, nil, nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "provider is required")

	// Test getting global credential with empty provider
	_, err = manager.GetGlobalCredential(ctx, "")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "provider is required")

	// Test deleting global credential with empty provider
	err = manager.DeleteGlobalCredential(ctx, "")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "provider is required")

	// Test with invalid credential
	err = manager.SetGlobalCredential(ctx, "openai", map[string]string{}, nil, nil)
	assert.Error(t, err)

	// Test getting non-existent global credential
	_, err = manager.GetGlobalCredential(ctx, "non-existent")
	assert.Error(t, err)
	assert.ErrorIs(t, err, storage.ErrNotFound)

	// Test deleting non-existent global credential
	err = manager.DeleteGlobalCredential(ctx, "non-existent")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "credential not found")
}

// TestRecordUsageErrors tests error cases in RecordUsage
func TestRecordUsageErrors(t *testing.T) {
	ctx := context.Background()
	store := storage.NewMockStore()
	masterKey, _ := models.GenerateMasterKey()
	manager, err := NewManager(store, masterKey)
	require.NoError(t, err)

	// Try to record usage for non-existent credential
	usage := &Usage{
		Provider:         "openai",
		Model:            "gpt-4",
		PromptTokens:     100,
		CompletionTokens: 50,
	}

	err = manager.RecordUsage(ctx, "non-existent", "openai", usage)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get credential")
}

// TestValidateCredentialEdgeCases tests edge cases in validation
func TestValidateCredentialEdgeCases(t *testing.T) {
	store := storage.NewMockStore()
	masterKey, _ := models.GenerateMasterKey()
	manager, err := NewManager(store, masterKey)
	require.NoError(t, err)

	ctx := context.WithValue(context.Background(), "skip_validation", true)

	tests := []struct {
		name     string
		provider string
		cred     map[string]string
		config   map[string]interface{}
		wantErr  bool
	}{
		{
			name:     "Empty OpenAI API key",
			provider: "openai",
			cred:     map[string]string{"api_key": ""},
			wantErr:  true,
		},
		{
			name:     "Empty Anthropic API key",
			provider: "anthropic",
			cred:     map[string]string{"api_key": ""},
			wantErr:  true,
		},
		{
			name:     "Empty Azure API key",
			provider: "azure",
			cred:     map[string]string{"api_key": ""},
			wantErr:  true,
		},
		{
			name:     "Empty Google AI Studio API key",
			provider: "google-aistudio",
			cred:     map[string]string{"api_key": ""},
			wantErr:  true,
		},
		{
			name:     "Empty Vertex AI service account",
			provider: "google-vertexai",
			cred:     map[string]string{"service_account_json": ""},
			wantErr:  true,
		},
		{
			name:     "Empty AWS access key",
			provider: "aws-bedrock",
			cred: map[string]string{
				"access_key_id":     "",
				"secret_access_key": "secret",
				"region":            "us-east-1",
			},
			wantErr: true,
		},
		{
			name:     "Empty AWS secret key",
			provider: "aws-bedrock",
			cred: map[string]string{
				"access_key_id":     "AKIATEST",
				"secret_access_key": "",
				"region":            "us-east-1",
			},
			wantErr: true,
		},
		{
			name:     "Empty AWS region",
			provider: "aws-bedrock",
			cred: map[string]string{
				"access_key_id":     "AKIATEST",
				"secret_access_key": "secret",
				"region":            "",
			},
			wantErr: true,
		},
		{
			name:     "Empty Groq API key",
			provider: "groq",
			cred:     map[string]string{"api_key": ""},
			wantErr:  true,
		},
		{
			name:     "Empty Mistral API key",
			provider: "mistral",
			cred:     map[string]string{"api_key": ""},
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := manager.ValidateCredential(ctx, tt.provider, tt.cred, tt.config)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// TestGetCredentialsWithErrors tests error handling in GetCredentials
func TestGetCredentialsWithErrors(t *testing.T) {
	ctx := context.Background()
	store := storage.NewMockStore()
	masterKey, _ := models.GenerateMasterKey()
	manager, err := NewManager(store, masterKey)
	require.NoError(t, err)

	// Test with empty API key ID
	_, err = manager.GetCredentials(ctx, "", "openai")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "api key ID and provider are required")

	// Test with empty provider
	_, err = manager.GetCredentials(ctx, "test-key", "")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "api key ID and provider are required")
}

// TestUpdateCredentialEdgeCases tests edge cases in UpdateCredential
func TestUpdateCredentialEdgeCases(t *testing.T) {
	ctx := context.Background()
	store := storage.NewMockStore()
	masterKey, _ := models.GenerateMasterKey()
	manager, err := NewManager(store, masterKey)
	require.NoError(t, err)

	// Skip validation
	ctx = context.WithValue(ctx, "skip_validation", true)

	// Add initial credential
	apiKeyID := "test-key"
	provider := "openai"
	err = manager.AddCredential(ctx, apiKeyID, provider, map[string]string{"api_key": "sk-initial"}, nil)
	require.NoError(t, err)

	// Update with only config (no new credential)
	newConfig := map[string]interface{}{"model": "gpt-4"}
	err = manager.UpdateCredential(ctx, apiKeyID, provider, nil, newConfig)
	assert.NoError(t, err)

	// Verify config was updated but credential unchanged
	cred, err := manager.GetCredential(ctx, apiKeyID, provider)
	assert.NoError(t, err)
	assert.Equal(t, newConfig, cred.Config)
	assert.Equal(t, "sk-initial", cred.Data["api_key"])

	// Update with invalid new credential
	err = manager.UpdateCredential(ctx, apiKeyID, provider, map[string]string{"api_key": "invalid"}, nil)
	assert.Error(t, err)
}

// TestRotateEncryptionKey tests the unimplemented key rotation
func TestRotateEncryptionKey(t *testing.T) {
	ctx := context.Background()
	store := storage.NewMockStore()
	masterKey, _ := models.GenerateMasterKey()
	manager, err := NewManager(store, masterKey)
	require.NoError(t, err)

	err = manager.RotateEncryptionKey(ctx)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "key rotation not implemented")
}

// TestCostCalculationEdgeCases tests edge cases in cost calculation
func TestCostCalculationEdgeCases(t *testing.T) {
	store := storage.NewMockStore()
	masterKey, _ := models.GenerateMasterKey()
	manager, err := NewManager(store, masterKey)
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
			cost := manager.CalculateBYOKCost(tt.usage)
			assert.GreaterOrEqual(t, cost, 0.0, "Cost should never be negative")
		})
	}
}
