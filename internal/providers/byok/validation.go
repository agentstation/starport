package byok

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ValidateKey validates provider-specific keys
func (m *keyManager) ValidateKey(ctx context.Context, provider string, key map[string]string, config map[string]any) error {
	switch provider {
	case "openai":
		return m.validateOpenAI(ctx, key, config)
	case "anthropic":
		return m.validateAnthropic(ctx, key, config)
	case "azure", "azure-openai":
		return m.validateAzureOpenAI(ctx, key, config)
	case "google-aistudio":
		return m.validateGoogleAIStudio(ctx, key, config)
	case "google-vertexai":
		return m.validateGoogleVertexAI(ctx, key, config)
	case "aws-bedrock":
		return m.validateAWSBedrock(ctx, key, config)
	case "groq":
		return m.validateGroq(ctx, key, config)
	case "mistral":
		return m.validateMistral(ctx, key, config)
	default:
		return &ValidationError{
			Provider: provider,
			Message:  "unsupported provider",
		}
	}
}

// validateOpenAI validates OpenAI keys
func (m *keyManager) validateOpenAI(ctx context.Context, key map[string]string, _ map[string]any) error {
	apiKey, ok := key["api_key"]
	if !ok || apiKey == "" {
		return &ValidationError{
			Provider: "openai",
			Field:    "api_key",
			Message:  "api_key is required",
		}
	}

	// Check API key format
	if !strings.HasPrefix(apiKey, "sk-") {
		return &ValidationError{
			Provider: "openai",
			Field:    "api_key",
			Message:  "api_key must start with 'sk-'",
		}
	}

	// Optional: Test the key with a simple API call
	if ctx.Value("skip_validation") == nil {
		client := &http.Client{Timeout: 10 * time.Second}
		req, err := http.NewRequestWithContext(ctx, "GET", "https://api.openai.com/v1/models", nil)
		if err != nil {
			return fmt.Errorf("failed to create validation request: %w", err)
		}

		req.Header.Set("Authorization", "Bearer "+apiKey)
		if org, ok := key["organization"]; ok && org != "" {
			req.Header.Set("OpenAI-Organization", org)
		}

		resp, err := client.Do(req)
		if err != nil {
			return &ValidationError{
				Provider: "openai",
				Message:  "failed to validate API key: " + err.Error(),
			}
		}
		defer func() { _ = resp.Body.Close() }()

		if resp.StatusCode == 401 {
			return &ValidationError{
				Provider: "openai",
				Field:    "api_key",
				Message:  "invalid API key",
			}
		}
		if resp.StatusCode != 200 {
			return &ValidationError{
				Provider: "openai",
				Message:  fmt.Sprintf("validation failed with status %d", resp.StatusCode),
			}
		}
	}

	return nil
}

// validateAnthropic validates Anthropic keys
func (m *keyManager) validateAnthropic(ctx context.Context, key map[string]string, _ map[string]any) error {
	apiKey, ok := key["api_key"]
	if !ok || apiKey == "" {
		return &ValidationError{
			Provider: "anthropic",
			Field:    "api_key",
			Message:  "api_key is required",
		}
	}

	// Check API key format
	if !strings.HasPrefix(apiKey, "sk-ant-") {
		return &ValidationError{
			Provider: "anthropic",
			Field:    "api_key",
			Message:  "api_key must start with 'sk-ant-'",
		}
	}

	// Optional: Test the key
	if ctx.Value("skip_validation") == nil {
		client := &http.Client{Timeout: 10 * time.Second}
		req, err := http.NewRequestWithContext(ctx, "GET", "https://api.anthropic.com/v1/models", nil)
		if err != nil {
			return fmt.Errorf("failed to create validation request: %w", err)
		}

		req.Header.Set("x-api-key", apiKey)
		req.Header.Set("anthropic-version", "2023-06-01")

		resp, err := client.Do(req)
		if err != nil {
			return &ValidationError{
				Provider: "anthropic",
				Message:  "failed to validate API key: " + err.Error(),
			}
		}
		defer func() { _ = resp.Body.Close() }()

		if resp.StatusCode == 401 {
			return &ValidationError{
				Provider: "anthropic",
				Field:    "api_key",
				Message:  "invalid API key",
			}
		}
		if resp.StatusCode != 200 {
			return &ValidationError{
				Provider: "anthropic",
				Message:  fmt.Sprintf("validation failed with status %d", resp.StatusCode),
			}
		}
	}

	return nil
}

// validateAzureOpenAI validates Azure OpenAI keys
func (m *keyManager) validateAzureOpenAI(_ context.Context, key map[string]string, config map[string]any) error {
	apiKey, ok := key["api_key"]
	if !ok || apiKey == "" {
		return &ValidationError{
			Provider: "azure",
			Field:    "api_key",
			Message:  "api_key is required",
		}
	}

	// Check for required config
	if config == nil {
		return &ValidationError{
			Provider: "azure",
			Message:  "config is required for Azure OpenAI",
		}
	}

	endpoint, ok := config["endpoint"].(string)
	if !ok || endpoint == "" {
		return &ValidationError{
			Provider: "azure",
			Field:    "config.endpoint",
			Message:  "endpoint is required in config",
		}
	}

	deploymentID, ok := config["deployment_id"].(string)
	if !ok || deploymentID == "" {
		return &ValidationError{
			Provider: "azure",
			Field:    "config.deployment_id",
			Message:  "deployment_id is required in config",
		}
	}

	// Validate endpoint URL
	u, err := url.Parse(endpoint)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return &ValidationError{
			Provider: "azure",
			Field:    "config.endpoint",
			Message:  "invalid endpoint URL",
		}
	}

	return nil
}

// validateGoogleAIStudio validates Google AI Studio keys
func (m *keyManager) validateGoogleAIStudio(_ context.Context, key map[string]string, _ map[string]any) error {
	apiKey, ok := key["api_key"]
	if !ok || apiKey == "" {
		return &ValidationError{
			Provider: "google-aistudio",
			Field:    "api_key",
			Message:  "api_key is required",
		}
	}

	// Google AI Studio keys are typically 39 characters
	if len(apiKey) != 39 {
		return &ValidationError{
			Provider: "google-aistudio",
			Field:    "api_key",
			Message:  "invalid API key length",
		}
	}

	return nil
}

// validateGoogleVertexAI validates Google Vertex AI service account
func (m *keyManager) validateGoogleVertexAI(_ context.Context, key map[string]string, _ map[string]any) error {
	serviceAccount, ok := key["service_account_json"]
	if !ok || serviceAccount == "" {
		return &ValidationError{
			Provider: "google-vertexai",
			Field:    "service_account_json",
			Message:  "service_account_json is required",
		}
	}

	// Try to parse the service account JSON
	var sa map[string]any
	if err := json.Unmarshal([]byte(serviceAccount), &sa); err != nil {
		return &ValidationError{
			Provider: "google-vertexai",
			Field:    "service_account_json",
			Message:  "invalid JSON format",
		}
	}

	// Check required fields
	requiredFields := []string{"type", "project_id", "private_key_id", "private_key", "client_email"}
	for _, field := range requiredFields {
		if _, ok := sa[field]; !ok {
			return &ValidationError{
				Provider: "google-vertexai",
				Field:    "service_account_json",
				Message:  fmt.Sprintf("missing required field: %s", field),
			}
		}
	}

	// Verify it's a service account
	if accountType, ok := sa["type"].(string); !ok || accountType != "service_account" {
		return &ValidationError{
			Provider: "google-vertexai",
			Field:    "service_account_json",
			Message:  "must be a service account",
		}
	}

	return nil
}

// validateAWSBedrock validates AWS Bedrock keys
func (m *keyManager) validateAWSBedrock(_ context.Context, key map[string]string, _ map[string]any) error {
	accessKey, ok := key["access_key_id"]
	if !ok || accessKey == "" {
		return &ValidationError{
			Provider: "aws-bedrock",
			Field:    "access_key_id",
			Message:  "access_key_id is required",
		}
	}

	secretKey, ok := key["secret_access_key"]
	if !ok || secretKey == "" {
		return &ValidationError{
			Provider: "aws-bedrock",
			Field:    "secret_access_key",
			Message:  "secret_access_key is required",
		}
	}

	region, ok := key["region"]
	if !ok || region == "" {
		return &ValidationError{
			Provider: "aws-bedrock",
			Field:    "region",
			Message:  "region is required",
		}
	}

	// Validate access key format (20 characters, alphanumeric)
	if len(accessKey) != 20 || !isAlphanumeric(accessKey) {
		return &ValidationError{
			Provider: "aws-bedrock",
			Field:    "access_key_id",
			Message:  "invalid access key format",
		}
	}

	return nil
}

// validateGroq validates Groq keys
func (m *keyManager) validateGroq(_ context.Context, key map[string]string, _ map[string]any) error {
	apiKey, ok := key["api_key"]
	if !ok || apiKey == "" {
		return &ValidationError{
			Provider: "groq",
			Field:    "api_key",
			Message:  "api_key is required",
		}
	}

	// Groq keys typically start with "gsk_"
	if !strings.HasPrefix(apiKey, "gsk_") {
		return &ValidationError{
			Provider: "groq",
			Field:    "api_key",
			Message:  "api_key must start with 'gsk_'",
		}
	}

	return nil
}

// validateMistral validates Mistral keys
func (m *keyManager) validateMistral(_ context.Context, key map[string]string, _ map[string]any) error {
	apiKey, ok := key["api_key"]
	if !ok || apiKey == "" {
		return &ValidationError{
			Provider: "mistral",
			Field:    "api_key",
			Message:  "api_key is required",
		}
	}

	// Mistral keys are typically 32 characters
	if len(apiKey) != 32 {
		return &ValidationError{
			Provider: "mistral",
			Field:    "api_key",
			Message:  "invalid API key length",
		}
	}

	return nil
}

// isAlphanumeric checks if a string contains only alphanumeric characters
func isAlphanumeric(s string) bool {
	for _, r := range s {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') {
			return false
		}
	}
	return true
}
