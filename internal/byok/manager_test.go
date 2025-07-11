package byok

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/models"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewManager(t *testing.T) {
	tests := []struct {
		name      string
		store     storage.KVStore
		masterKey []byte
		wantErr   bool
		errMsg    string
	}{
		{
			name:      "Valid inputs",
			store:     storage.NewMockStore(),
			masterKey: make([]byte, 32),
			wantErr:   false,
		},
		{
			name:      "Nil store",
			store:     nil,
			masterKey: make([]byte, 32),
			wantErr:   true,
			errMsg:    "store is required",
		},
		{
			name:      "Short master key",
			store:     storage.NewMockStore(),
			masterKey: make([]byte, 16),
			wantErr:   true,
			errMsg:    "master key must be at least 32 bytes",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			manager, err := NewManager(tt.store, tt.masterKey)
			if tt.wantErr {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
				assert.Nil(t, manager)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, manager)
			}
		})
	}
}

func TestAddCredential(t *testing.T) {
	ctx := context.Background()
	store := storage.NewMockStore()
	masterKey, _ := models.GenerateMasterKey()
	manager, err := NewManager(store, masterKey)
	require.NoError(t, err)

	tests := []struct {
		name     string
		apiKeyID string
		provider string
		cred     map[string]string
		config   map[string]interface{}
		wantErr  bool
		errMsg   string
	}{
		{
			name:     "Valid OpenAI credential",
			apiKeyID: "test-key-1",
			provider: "openai",
			cred: map[string]string{
				"api_key": "sk-test123",
			},
			config:  nil,
			wantErr: false,
		},
		{
			name:     "Valid Azure credential",
			apiKeyID: "test-key-2",
			provider: "azure",
			cred: map[string]string{
				"api_key": "test-azure-key",
			},
			config: map[string]interface{}{
				"endpoint":      "https://test.openai.azure.com",
				"deployment_id": "gpt-4",
			},
			wantErr: false,
		},
		{
			name:     "Missing API key ID",
			apiKeyID: "",
			provider: "openai",
			cred: map[string]string{
				"api_key": "sk-test123",
			},
			wantErr: true,
			errMsg:  "api key ID is required",
		},
		{
			name:     "Missing provider",
			apiKeyID: "test-key-3",
			provider: "",
			cred: map[string]string{
				"api_key": "sk-test123",
			},
			wantErr: true,
			errMsg:  "provider is required",
		},
		{
			name:     "Invalid credential format",
			apiKeyID: "test-key-4",
			provider: "openai",
			cred:     map[string]string{},
			wantErr:  true,
			errMsg:   "api_key is required",
		},
	}

	// Add context to skip validation
	ctx = context.WithValue(ctx, "skip_validation", true)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := manager.AddCredential(ctx, tt.apiKeyID, tt.provider, tt.cred, tt.config)
			if tt.wantErr {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			} else {
				assert.NoError(t, err)

				// Verify credential was stored
				key := models.ProviderKeyStorageKey("user:"+tt.apiKeyID, tt.provider)
				data, err := store.Get(ctx, key)
				assert.NoError(t, err)
				assert.NotNil(t, data)

				// Verify we can retrieve it
				cred, err := manager.GetCredential(ctx, tt.apiKeyID, tt.provider)
				assert.NoError(t, err)
				assert.Equal(t, tt.provider, cred.Provider)
				assert.Equal(t, tt.cred, cred.Data)
				if tt.config != nil {
					assert.Equal(t, tt.config, cred.Config)
				}
			}
		})
	}
}

func TestGetCredentials(t *testing.T) {
	ctx := context.Background()
	store := storage.NewMockStore()
	masterKey, _ := models.GenerateMasterKey()
	manager, err := NewManager(store, masterKey)
	require.NoError(t, err)

	// Skip validation
	ctx = context.WithValue(ctx, "skip_validation", true)

	apiKeyID := "test-key"
	provider := "openai"

	// Add multiple credentials with different priorities
	creds := []struct {
		priority int
		apiKey   string
	}{
		{priority: 2, apiKey: "sk-third"},
		{priority: 0, apiKey: "sk-first"},
		{priority: 1, apiKey: "sk-second"},
	}

	for _, c := range creds {
		err := manager.AddCredential(ctx, apiKeyID, provider, map[string]string{"api_key": c.apiKey}, nil)
		require.NoError(t, err)

		// Update priority
		key := models.ProviderKeyStorageKey("user:"+apiKeyID, provider)
		data, _ := store.Get(ctx, key)
		var providerKey models.ProviderKey
		storage.DeserializeModel(data, &providerKey)
		providerKey.Priority = c.priority
		data, _ = storage.SerializeModel(&providerKey)
		store.Set(ctx, key, data)
	}

	// Get all credentials
	credentials, err := manager.GetCredentials(ctx, apiKeyID, provider)
	assert.NoError(t, err)
	assert.Len(t, credentials, 1) // Only one credential per provider in current implementation

	// Test with non-existent provider
	credentials, err = manager.GetCredentials(ctx, apiKeyID, "non-existent")
	assert.NoError(t, err)
	assert.Len(t, credentials, 0)
}

func TestUpdateCredential(t *testing.T) {
	ctx := context.Background()
	store := storage.NewMockStore()
	masterKey, _ := models.GenerateMasterKey()
	manager, err := NewManager(store, masterKey)
	require.NoError(t, err)

	// Skip validation
	ctx = context.WithValue(ctx, "skip_validation", true)

	apiKeyID := "test-key"
	provider := "openai"

	// Add initial credential
	err = manager.AddCredential(ctx, apiKeyID, provider, map[string]string{"api_key": "sk-old"}, nil)
	require.NoError(t, err)

	// Update credential
	newCred := map[string]string{"api_key": "sk-new"}
	newConfig := map[string]interface{}{"custom": "value"}
	err = manager.UpdateCredential(ctx, apiKeyID, provider, newCred, newConfig)
	assert.NoError(t, err)

	// Verify update
	cred, err := manager.GetCredential(ctx, apiKeyID, provider)
	assert.NoError(t, err)
	assert.Equal(t, newCred, cred.Data)
	assert.Equal(t, newConfig, cred.Config)

	// Update non-existent credential
	err = manager.UpdateCredential(ctx, apiKeyID, "non-existent", newCred, nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "credential not found")
}

func TestDeleteCredential(t *testing.T) {
	ctx := context.Background()
	store := storage.NewMockStore()
	masterKey, _ := models.GenerateMasterKey()
	manager, err := NewManager(store, masterKey)
	require.NoError(t, err)

	// Skip validation
	ctx = context.WithValue(ctx, "skip_validation", true)

	apiKeyID := "test-key"
	provider := "openai"

	// Add credential
	err = manager.AddCredential(ctx, apiKeyID, provider, map[string]string{"api_key": "sk-test"}, nil)
	require.NoError(t, err)

	// Delete credential
	err = manager.DeleteCredential(ctx, apiKeyID, provider)
	assert.NoError(t, err)

	// Verify deletion
	_, err = manager.GetCredential(ctx, apiKeyID, provider)
	assert.Error(t, err)
	assert.True(t, errors.Is(err, storage.ErrNotFound))

	// Delete non-existent credential
	err = manager.DeleteCredential(ctx, apiKeyID, "non-existent")
	assert.Error(t, err)
}

func TestDefaultKeys(t *testing.T) {
	ctx := context.Background()
	store := storage.NewMockStore()
	masterKey, _ := models.GenerateMasterKey()
	manager, err := NewManager(store, masterKey)
	require.NoError(t, err)

	// Skip validation
	ctx = context.WithValue(ctx, "skip_validation", true)

	provider := "openai"
	cred := map[string]string{"api_key": "sk-default"}
	config := map[string]interface{}{"rate_limit": float64(1000)}

	// Set global credential
	rateLimit := &models.RateLimitConfig{
		RequestsPerMinute: 1000,
		TokensPerMinute:   100000,
	}
	err = manager.SetGlobalCredential(ctx, provider, cred, config, rateLimit)
	assert.NoError(t, err)

	// Get global credential
	globalCred, err := manager.GetGlobalCredential(ctx, provider)
	assert.NoError(t, err)
	assert.Equal(t, provider, globalCred.Provider)
	assert.Equal(t, cred, globalCred.Data)
	assert.Equal(t, config, globalCred.Config)
	assert.Equal(t, rateLimit, globalCred.RateLimit)

	// List global credentials
	keys, err := manager.ListGlobalCredentials(ctx)
	assert.NoError(t, err)
	assert.Len(t, keys, 1)
	assert.Equal(t, provider, keys[0].Provider)

	// Delete global credential
	err = manager.DeleteGlobalCredential(ctx, provider)
	assert.NoError(t, err)

	// Verify deletion
	_, err = manager.GetGlobalCredential(ctx, provider)
	assert.Error(t, err)
	assert.True(t, errors.Is(err, storage.ErrNotFound))
}

func TestDetermineKeyStrategy(t *testing.T) {
	ctx := context.Background()
	store := storage.NewMockStore()
	masterKey, _ := models.GenerateMasterKey()
	manager, err := NewManager(store, masterKey)
	require.NoError(t, err)

	// Skip validation
	ctx = context.WithValue(ctx, "skip_validation", true)

	// No BYOK credentials - should use gateway
	strategy := manager.DetermineKeyStrategy(ctx, "test-key", "openai")
	assert.Equal(t, GatewayFirst, strategy)

	// Add BYOK credential
	err = manager.AddCredential(ctx, "test-key", "openai", map[string]string{"api_key": "sk-test"}, nil)
	require.NoError(t, err)

	// With BYOK credential - still defaults to GatewayFirst for now
	strategy = manager.DetermineKeyStrategy(ctx, "test-key", "openai")
	assert.Equal(t, GatewayFirst, strategy)
}

func TestCalculateBYOKCost(t *testing.T) {
	store := storage.NewMockStore()
	masterKey, _ := models.GenerateMasterKey()
	manager, err := NewManager(store, masterKey)
	require.NoError(t, err)

	tests := []struct {
		name         string
		usage        *Usage
		expectedCost float64
	}{
		{
			name: "OpenAI GPT-4 usage",
			usage: &Usage{
				Provider:         "openai",
				Model:            "gpt-4",
				PromptTokens:     1000,
				CompletionTokens: 500,
			},
			expectedCost: 0.003, // (1000 * 0.00003 + 500 * 0.00006) = 0.06, then * 0.05 = 0.003
		},
		{
			name: "Anthropic Claude usage",
			usage: &Usage{
				Provider:         "anthropic",
				Model:            "claude-3-opus",
				PromptTokens:     1000,
				CompletionTokens: 500,
			},
			expectedCost: 0.0025, // (1000 * 0.00002 + 500 * 0.00006) = 0.05, then * 0.05 = 0.0025
		},
		{
			name: "With image generation",
			usage: &Usage{
				Provider:   "openai",
				Model:      "dall-e-3",
				ImageCount: 2,
			},
			expectedCost: 0.002, // 2 * 0.02 * 0.05
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cost := manager.CalculateBYOKCost(tt.usage)
			assert.InDelta(t, tt.expectedCost, cost, 0.0000001)
		})
	}
}

func TestRecordUsage(t *testing.T) {
	ctx := context.Background()
	store := storage.NewMockStore()
	masterKey, _ := models.GenerateMasterKey()
	manager, err := NewManager(store, masterKey)
	require.NoError(t, err)

	// Skip validation
	ctx = context.WithValue(ctx, "skip_validation", true)

	apiKeyID := "test-key"
	provider := "openai"

	// Add credential
	err = manager.AddCredential(ctx, apiKeyID, provider, map[string]string{"api_key": "sk-test"}, nil)
	require.NoError(t, err)

	// Record usage
	usage := &Usage{
		Provider:         provider,
		Model:            "gpt-4",
		PromptTokens:     100,
		CompletionTokens: 50,
		Timestamp:        time.Now(),
	}
	err = manager.RecordUsage(ctx, apiKeyID, provider, usage)
	assert.NoError(t, err)

	// Verify usage was recorded
	cred, err := manager.GetCredential(ctx, apiKeyID, provider)
	assert.NoError(t, err)
	assert.NotNil(t, cred.LastUsed)
	assert.Equal(t, int64(1), cred.UsageCount)
}

func TestValidationError(t *testing.T) {
	tests := []struct {
		name     string
		err      *ValidationError
		expected string
	}{
		{
			name: "With field",
			err: &ValidationError{
				Provider: "openai",
				Field:    "api_key",
				Message:  "invalid format",
			},
			expected: "validation failed for openai api_key: invalid format",
		},
		{
			name: "Without field",
			err: &ValidationError{
				Provider: "anthropic",
				Message:  "unsupported model",
			},
			expected: "validation failed for anthropic: unsupported model",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.err.Error())
		})
	}
}

// TestEncryptionSecurity verifies that credentials are properly encrypted
func TestEncryptionSecurity(t *testing.T) {
	ctx := context.Background()
	store := storage.NewMockStore()
	masterKey, _ := models.GenerateMasterKey()
	manager, err := NewManager(store, masterKey)
	require.NoError(t, err)

	// Skip validation
	ctx = context.WithValue(ctx, "skip_validation", true)

	apiKeyID := "test-key"
	provider := "openai"
	secretKey := "sk-super-secret-key-123456789"

	// Add credential
	err = manager.AddCredential(ctx, apiKeyID, provider, map[string]string{"api_key": secretKey}, nil)
	require.NoError(t, err)

	// Get raw data from store
	key := models.ProviderKeyStorageKey("user:"+apiKeyID, provider)
	rawData, err := store.Get(ctx, key)
	require.NoError(t, err)

	// Verify the secret is not in plaintext
	assert.NotContains(t, string(rawData), secretKey)

	// Deserialize to check encrypted field
	var providerKey models.ProviderKey
	err = storage.DeserializeModel(rawData, &providerKey)
	require.NoError(t, err)

	// Encrypted credential should be base64 and not contain the secret
	assert.NotEmpty(t, providerKey.EncryptedCredential)
	assert.NotContains(t, providerKey.EncryptedCredential, secretKey)

	// Verify we can decrypt it correctly
	cred, err := manager.GetCredential(ctx, apiKeyID, provider)
	assert.NoError(t, err)
	assert.Equal(t, secretKey, cred.Data["api_key"])
}
