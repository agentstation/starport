package byok

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/agentstation/starport/internal/models"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateKey(t *testing.T) {
	store := storage.NewMockStore()
	masterKey, _ := models.GenerateMasterKey()
	manager, err := NewProviderKeys(store, masterKey)
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
			name:     "Valid Azure config",
			provider: "azure",
			key:      map[string]string{"api_key": "test-azure-key"},
			config: map[string]any{
				"endpoint":      "https://test.openai.azure.com",
				"deployment_id": "gpt-4",
			},
			wantErr: false,
		},
		{
			name:     "Azure missing endpoint",
			provider: "azure",
			key:      map[string]string{"api_key": "test-azure-key"},
			config: map[string]any{
				"deployment_id": "gpt-4",
			},
			wantErr:  true,
			errField: "endpoint",
		},
		{
			name:     "Azure missing deployment",
			provider: "azure",
			key:      map[string]string{"api_key": "test-azure-key"},
			config: map[string]any{
				"endpoint": "https://test.openai.azure.com",
			},
			wantErr:  true,
			errField: "deployment_id",
		},
		{
			name:     "Azure invalid endpoint URL",
			provider: "azure",
			key:      map[string]string{"api_key": "test-azure-key"},
			config: map[string]any{
				"endpoint":      "not-a-url",
				"deployment_id": "gpt-4",
			},
			wantErr:  true,
			errField: "endpoint",
		},

		// Google AI Studio tests
		{
			name:     "Valid Google AI Studio key",
			provider: "google-aistudio",
			key:      map[string]string{"api_key": "AIzaSyD" + strings.Repeat("x", 32)}, // 39 chars
			wantErr:  false,
		},
		{
			name:     "Invalid Google AI Studio key length",
			provider: "google-aistudio",
			key:      map[string]string{"api_key": "AIzaSyD-short"},
			wantErr:  true,
			errField: "api_key",
		},

		// Google Vertex AI tests
		{
			name:     "Valid Vertex AI service account",
			provider: "google-vertexai",
			key: map[string]string{
				"service_account_json": `{
					"type": "service_account",
					"project_id": "test-project",
					"private_key_id": "key123",
					"private_key": "-----BEGIN PRIVATE KEY-----\ntest\n-----END PRIVATE KEY-----",
					"client_email": "test@test.iam.gserviceaccount.com",
					"client_id": "123456789",
					"auth_uri": "https://accounts.google.com/o/oauth2/auth",
					"token_uri": "https://oauth2.googleapis.com/token"
				}`,
			},
			wantErr: false,
		},
		{
			name:     "Invalid Vertex AI JSON",
			provider: "google-vertexai",
			key: map[string]string{
				"service_account_json": "not-json",
			},
			wantErr:  true,
			errField: "service_account_json",
		},
		{
			name:     "Vertex AI missing required field",
			provider: "google-vertexai",
			key: map[string]string{
				"service_account_json": `{
					"type": "service_account",
					"project_id": "test-project"
				}`,
			},
			wantErr:  true,
			errField: "service_account_json",
		},
		{
			name:     "Vertex AI wrong type",
			provider: "google-vertexai",
			key: map[string]string{
				"service_account_json": `{
					"type": "user",
					"project_id": "test-project",
					"private_key_id": "key123",
					"private_key": "test",
					"client_email": "test@test.iam.gserviceaccount.com"
				}`,
			},
			wantErr:  true,
			errField: "service_account_json",
		},

		// AWS Bedrock tests
		{
			name:     "Valid AWS Bedrock credentials",
			provider: "aws-bedrock",
			key: map[string]string{
				"access_key_id":     "AKIAIOSFODNN7EXAMPLE",
				"secret_access_key": "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
				"region":            "us-east-1",
			},
			wantErr: false,
		},
		{
			name:     "AWS Bedrock with session token",
			provider: "aws-bedrock",
			key: map[string]string{
				"access_key_id":     "ASIAIOSFODNN7EXAMPLE",
				"secret_access_key": "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
				"session_token":     "AQoDYXdzEJr...",
				"region":            "us-west-2",
			},
			wantErr: false,
		},
		{
			name:     "AWS Bedrock invalid access key format",
			provider: "aws-bedrock",
			key: map[string]string{
				"access_key_id":     "invalid-key",
				"secret_access_key": "secret",
				"region":            "us-east-1",
			},
			wantErr:  true,
			errField: "access_key_id",
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

// TestValidateKeyWithAPIValidation tests actual API validation when enabled
func TestValidateKeyWithAPIValidation(t *testing.T) {
	// Skip if no real API keys are available
	if os.Getenv("TEST_OPENAI_API_KEY") == "" {
		t.Skip("Skipping API validation test - no test API key provided")
	}

	store := storage.NewMockStore()
	masterKey, _ := models.GenerateMasterKey()
	manager, err := NewProviderKeys(store, masterKey)
	require.NoError(t, err)

	// Test with actual API validation (no skip_validation)
	ctx := context.Background()

	// Test with valid OpenAI key from environment
	validKey := os.Getenv("TEST_OPENAI_API_KEY")
	err = manager.ValidateKey(ctx, "openai", map[string]string{"api_key": validKey}, nil)
	assert.NoError(t, err)

	// Test with invalid OpenAI key
	err = manager.ValidateKey(ctx, "openai", map[string]string{"api_key": "sk-invalid-key-12345"}, nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid API key")
}

// TestValidateKeyNilConfig tests validation with nil config
func TestValidateKeyNilConfig(t *testing.T) {
	store := storage.NewMockStore()
	masterKey, _ := models.GenerateMasterKey()
	manager, err := NewProviderKeys(store, masterKey)
	require.NoError(t, err)

	ctx := context.WithValue(context.Background(), "skip_validation", true)

	// OpenAI should work with nil config
	err = manager.ValidateKey(ctx, "openai", map[string]string{"api_key": "sk-test123"}, nil)
	assert.NoError(t, err)

	// Azure should fail with nil config
	err = manager.ValidateKey(ctx, "azure", map[string]string{"api_key": "test-key"}, nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "config is required")
}

// TestValidateKeyEmptyCredentials tests validation with empty key map
func TestValidateKeyEmptyCredentials(t *testing.T) {
	store := storage.NewMockStore()
	masterKey, _ := models.GenerateMasterKey()
	manager, err := NewProviderKeys(store, masterKey)
	require.NoError(t, err)

	ctx := context.WithValue(context.Background(), "skip_validation", true)

	providers := []string{"openai", "anthropic", "groq", "mistral", "google-aistudio", "google-vertexai", "aws-bedrock"}
	for _, provider := range providers {
		t.Run(provider, func(t *testing.T) {
			err := manager.ValidateKey(ctx, provider, map[string]string{}, nil)
			assert.Error(t, err, "Empty key map should fail for %s", provider)
		})
	}
}

// TestIsAlphanumeric tests the helper function
func TestIsAlphanumeric(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"AKIAIOSFODNN7EXAMPLE", true},
		{"abc123XYZ", true},
		{"", true}, // empty string is considered alphanumeric
		{"has-dash", false},
		{"has_underscore", false},
		{"has space", false},
		{"has@symbol", false},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := isAlphanumeric(tt.input)
			assert.Equal(t, tt.want, got)
		})
	}
}
