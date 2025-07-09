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

func TestValidateCredential(t *testing.T) {
	store := storage.NewMockStore()
	masterKey, _ := models.GenerateMasterKey()
	manager, err := NewManager(store, masterKey)
	require.NoError(t, err)

	// Skip actual API calls
	ctx := context.WithValue(context.Background(), "skip_validation", true)

	tests := []struct {
		name     string
		provider string
		cred     map[string]string
		config   map[string]interface{}
		wantErr  bool
		errField string
	}{
		// OpenAI tests
		{
			name:     "Valid OpenAI key",
			provider: "openai",
			cred:     map[string]string{"api_key": "sk-test123456789"},
			wantErr:  false,
		},
		{
			name:     "Missing OpenAI key",
			provider: "openai",
			cred:     map[string]string{},
			wantErr:  true,
			errField: "api_key",
		},
		{
			name:     "Invalid OpenAI key format",
			provider: "openai",
			cred:     map[string]string{"api_key": "invalid-key"},
			wantErr:  true,
			errField: "api_key",
		},
		{
			name:     "OpenAI with organization",
			provider: "openai",
			cred:     map[string]string{"api_key": "sk-test123", "organization": "org-123"},
			wantErr:  false,
		},

		// Anthropic tests
		{
			name:     "Valid Anthropic key",
			provider: "anthropic",
			cred:     map[string]string{"api_key": "sk-ant-test123456789"},
			wantErr:  false,
		},
		{
			name:     "Invalid Anthropic key format",
			provider: "anthropic",
			cred:     map[string]string{"api_key": "sk-test123"},
			wantErr:  true,
			errField: "api_key",
		},

		// Azure OpenAI tests
		{
			name:     "Valid Azure config",
			provider: "azure",
			cred:     map[string]string{"api_key": "test-azure-key"},
			config: map[string]interface{}{
				"endpoint":      "https://test.openai.azure.com",
				"deployment_id": "gpt-4",
			},
			wantErr: false,
		},
		{
			name:     "Azure missing endpoint",
			provider: "azure",
			cred:     map[string]string{"api_key": "test-azure-key"},
			config: map[string]interface{}{
				"deployment_id": "gpt-4",
			},
			wantErr:  true,
			errField: "endpoint",
		},
		{
			name:     "Azure missing deployment",
			provider: "azure",
			cred:     map[string]string{"api_key": "test-azure-key"},
			config: map[string]interface{}{
				"endpoint": "https://test.openai.azure.com",
			},
			wantErr:  true,
			errField: "deployment_id",
		},
		{
			name:     "Azure invalid endpoint URL",
			provider: "azure",
			cred:     map[string]string{"api_key": "test-azure-key"},
			config: map[string]interface{}{
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
			cred:     map[string]string{"api_key": "AIzaSyD" + strings.Repeat("x", 32)}, // 39 chars
			wantErr:  false,
		},
		{
			name:     "Invalid Google AI Studio key length",
			provider: "google-aistudio",
			cred:     map[string]string{"api_key": "AIzaSyD-short"},
			wantErr:  true,
			errField: "api_key",
		},

		// Google Vertex AI tests
		{
			name:     "Valid Vertex AI service account",
			provider: "google-vertexai",
			cred: map[string]string{
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
			cred: map[string]string{
				"service_account_json": "not-json",
			},
			wantErr:  true,
			errField: "service_account_json",
		},
		{
			name:     "Vertex AI missing required field",
			provider: "google-vertexai",
			cred: map[string]string{
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
			cred: map[string]string{
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
			cred: map[string]string{
				"access_key_id":     "AKIAIOSFODNN7EXAMPLE",
				"secret_access_key": "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
				"region":            "us-east-1",
			},
			wantErr: false,
		},
		{
			name:     "AWS Bedrock with session token",
			provider: "aws-bedrock",
			cred: map[string]string{
				"access_key_id":     "ASIAIOSFODNN7EXAMPLE",
				"secret_access_key": "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
				"region":            "us-east-1",
				"session_token":     "FwoGZXIvYXdzEJr...",
			},
			wantErr: false,
		},
		{
			name:     "AWS Bedrock missing access key",
			provider: "aws-bedrock",
			cred: map[string]string{
				"secret_access_key": "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
				"region":            "us-east-1",
			},
			wantErr:  true,
			errField: "access_key_id",
		},
		{
			name:     "AWS Bedrock invalid access key format",
			provider: "aws-bedrock",
			cred: map[string]string{
				"access_key_id":     "INVALID123",
				"secret_access_key": "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
				"region":            "us-east-1",
			},
			wantErr:  true,
			errField: "access_key_id",
		},

		// Groq tests
		{
			name:     "Valid Groq key",
			provider: "groq",
			cred:     map[string]string{"api_key": "gsk_test123456789"},
			wantErr:  false,
		},
		{
			name:     "Invalid Groq key format",
			provider: "groq",
			cred:     map[string]string{"api_key": "invalid-key"},
			wantErr:  true,
			errField: "api_key",
		},

		// Mistral tests
		{
			name:     "Valid Mistral key",
			provider: "mistral",
			cred:     map[string]string{"api_key": strings.Repeat("a", 32)}, // 32 chars
			wantErr:  false,
		},
		{
			name:     "Invalid Mistral key length",
			provider: "mistral",
			cred:     map[string]string{"api_key": "short-key"},
			wantErr:  true,
			errField: "api_key",
		},

		// Unsupported provider
		{
			name:     "Unsupported provider",
			provider: "unsupported",
			cred:     map[string]string{"api_key": "test"},
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := manager.ValidateCredential(ctx, tt.provider, tt.cred, tt.config)
			if tt.wantErr {
				assert.Error(t, err)
				if tt.errField != "" {
					var valErr *ValidationError
					if assert.ErrorAs(t, err, &valErr) {
						assert.Equal(t, tt.errField, valErr.Field)
					}
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// TestValidateCredentialWithAPICalls tests validation with actual API calls
// This test is skipped by default as it requires real API keys
func TestValidateCredentialWithAPICalls(t *testing.T) {
	t.Skip("Requires real API keys")

	store := storage.NewMockStore()
	masterKey, _ := models.GenerateMasterKey()
	manager, err := NewManager(store, masterKey)
	require.NoError(t, err)

	ctx := context.Background() // No skip_validation

	// Test with real OpenAI key (set OPENAI_API_KEY env var to run)
	openAIKey := os.Getenv("OPENAI_API_KEY")
	if openAIKey != "" {
		err := manager.ValidateCredential(ctx, "openai", map[string]string{"api_key": openAIKey}, nil)
		assert.NoError(t, err)
	}

	// Test with invalid key
	err = manager.ValidateCredential(ctx, "openai", map[string]string{"api_key": "sk-invalid"}, nil)
	assert.Error(t, err)
	var valErr *ValidationError
	if assert.ErrorAs(t, err, &valErr) {
		assert.Equal(t, "api_key", valErr.Field)
		assert.Contains(t, valErr.Message, "invalid")
	}
}