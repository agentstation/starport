package byok

import (
	"context"
	"strings"
	"testing"

	"github.com/agentstation/starport/internal/credentials"
	"github.com/agentstation/starport/internal/providers/connectors"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateKey(t *testing.T) {
	store := storage.NewMockStore()
	masterKey, _ := credentials.GenerateMasterKey()
	manager, err := newTestProviderKeys(store, masterKey)
	require.NoError(t, err)

	// Skip actual API calls
	ctx := context.WithValue(context.Background(), "skip_validation", true)

	tests := []struct {
		name     string
		provider string
		key      map[string]string
		config   map[string]any
		wantErr  bool
		errField string
	}{
		// OpenAI tests
		{
			name:     "Valid OpenAI key",
			provider: "openai",
			key:      map[string]string{"api_key": "sk-test123456789"},
			wantErr:  false,
		},
		{
			name:     "Missing OpenAI key",
			provider: "openai",
			key:      map[string]string{},
			wantErr:  true,
			errField: "api_key",
		},
		{
			name:     "Invalid OpenAI key format",
			provider: "openai",
			key:      map[string]string{"api_key": "invalid-key"},
			wantErr:  true,
			errField: "api_key",
		},
		{
			name:     "OpenAI with organization",
			provider: "openai",
			key:      map[string]string{"api_key": "sk-test123", "organization": "org-123"},
			wantErr:  false,
		},

		// Anthropic tests
		{
			name:     "Valid Anthropic key",
			provider: "anthropic",
			key:      map[string]string{"api_key": "sk-ant-test123456789"},
			wantErr:  false,
		},
		{
			name:     "Invalid Anthropic key format",
			provider: "anthropic",
			key:      map[string]string{"api_key": "sk-test123"},
			wantErr:  true,
			errField: "api_key",
		},

		// Azure OpenAI tests
		{
			name:     "Valid Azure API key",
			provider: "azure-openai",
			key:      map[string]string{"api_key": "test-azure-key"},
			wantErr:  false,
		},
		{
			name:     "Azure missing API key",
			provider: "azure-openai",
			key:      map[string]string{},
			wantErr:  true,
			errField: "api_key",
		},

		// Google AI Studio tests
		{
			name:     "Valid Google AI Studio key",
			provider: "google-ai-studio",
			key:      map[string]string{"api_key": "AIzaSyD" + strings.Repeat("x", 32)}, // 39 chars
			wantErr:  false,
		},
		{
			name:     "Invalid Google AI Studio key length",
			provider: "google-ai-studio",
			key:      map[string]string{"api_key": "AIzaSyD-short"},
			wantErr:  true,
			errField: "api_key",
		},

		// Google Vertex AI tests
		{
			name:     "Valid Vertex AI access token",
			provider: "google-vertex",
			key:      map[string]string{"access_token": "vertex-access-token"},
			wantErr:  false,
		},
		{
			name:     "Empty Vertex AI access token",
			provider: "google-vertex",
			key:      map[string]string{"access_token": ""},
			wantErr:  true,
			errField: "access_token",
		},

		// Groq tests
		{
			name:     "Valid Groq key",
			provider: "groq",
			key:      map[string]string{"api_key": "gsk_test123456789"},
			wantErr:  false,
		},
		{
			name:     "Invalid Groq key format",
			provider: "groq",
			key:      map[string]string{"api_key": "invalid-groq-key"},
			wantErr:  true,
			errField: "api_key",
		},

		// Mistral tests
		{
			name:     "Valid Mistral key",
			provider: "mistral",
			key:      map[string]string{"api_key": strings.Repeat("a", 32)}, // 32 chars
			wantErr:  false,
		},
		{
			name:     "Invalid Mistral key length",
			provider: "mistral",
			key:      map[string]string{"api_key": "too-short"},
			wantErr:  true,
			errField: "api_key",
		},

		// Unsupported provider
		{
			name:     "Unsupported provider",
			provider: "unsupported",
			key:      map[string]string{"api_key": "test"},
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := manager.ValidateKey(ctx, tt.provider, tt.key, tt.config)
			if tt.wantErr {
				assert.Error(t, err)
				if tt.errField != "" {
					assert.Contains(t, err.Error(), tt.errField)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidateKeyDoesNotPerformImplicitNetworkProbe(t *testing.T) {
	store := storage.NewMockStore()
	masterKey, _ := credentials.GenerateMasterKey()
	manager, err := newTestProviderKeys(store, masterKey)
	require.NoError(t, err)

	err = manager.ValidateKey(context.Background(), "openai", map[string]string{"api_key": "sk-local-shape"}, nil)
	assert.NoError(t, err)
	err = manager.ValidateKey(context.Background(), "openai", map[string]string{"api_key": "invalid-key"}, nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "must start")
}

// TestValidateKeyNilConfig tests validation with nil config
func TestValidateKeyNilConfig(t *testing.T) {
	store := storage.NewMockStore()
	masterKey, _ := credentials.GenerateMasterKey()
	manager, err := newTestProviderKeys(store, masterKey)
	require.NoError(t, err)

	ctx := context.WithValue(context.Background(), "skip_validation", true)

	// OpenAI should work with nil config
	err = manager.ValidateKey(ctx, "openai", map[string]string{"api_key": "sk-test123"}, nil)
	assert.NoError(t, err)

	// Azure deployments and endpoints are catalog facts, not credential fields.
	err = manager.ValidateKey(ctx, "azure-openai", map[string]string{"api_key": "test-key"}, nil)
	assert.NoError(t, err)
}

// TestValidateKeyEmptyCredentials tests validation with empty key map
func TestValidateKeyEmptyCredentials(t *testing.T) {
	store := storage.NewMockStore()
	masterKey, _ := credentials.GenerateMasterKey()
	manager, err := newTestProviderKeys(store, masterKey)
	require.NoError(t, err)

	ctx := context.WithValue(context.Background(), "skip_validation", true)

	providers := []string{"openai", "anthropic", "groq", "mistral", "google-ai-studio", "google-vertex"}
	for _, provider := range providers {
		t.Run(provider, func(t *testing.T) {
			err := manager.ValidateKey(ctx, provider, map[string]string{}, nil)
			assert.Error(t, err, "Empty key map should fail for %s", provider)
		})
	}
}

func TestUnsupportedInferenceProviderFailsClosed(t *testing.T) {
	store := storage.NewMockStore()
	masterKey, err := credentials.GenerateMasterKey()
	require.NoError(t, err)
	manager, err := newTestProviderKeys(store, masterKey)
	require.NoError(t, err)

	err = manager.ValidateKey(context.Background(), "aws-bedrock", map[string]string{
		"access_key_id": "must-not-be-accepted",
	}, nil)
	require.ErrorIs(t, err, connectors.ErrAdapterProviderUnsupported)
}

func TestValidateKeyCanonicalGoogleProviderNames(t *testing.T) {
	store := storage.NewMockStore()
	masterKey, _ := credentials.GenerateMasterKey()
	manager, err := newTestProviderKeys(store, masterKey)
	require.NoError(t, err)

	ctx := context.WithValue(context.Background(), "skip_validation", true)

	err = manager.ValidateKey(ctx, "google-ai-studio", map[string]string{
		"api_key": "AIzaSyD" + strings.Repeat("x", 32),
	}, nil)
	require.NoError(t, err)

	err = manager.ValidateKey(ctx, "google-vertex", map[string]string{
		"access_token": "vertex-access-token",
	}, nil)
	require.NoError(t, err)
}
