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

// ValidateCredential validates provider-specific credentials
func (m *manager) ValidateCredential(ctx context.Context, provider string, cred map[string]string, config map[string]interface{}) error {
	switch provider {
	case "openai":
		return m.validateOpenAI(ctx, cred, config)
	case "anthropic":
		return m.validateAnthropic(ctx, cred, config)
	case "azure", "azure-openai":
		return m.validateAzureOpenAI(ctx, cred, config)
	case "google-aistudio":
		return m.validateGoogleAIStudio(ctx, cred, config)
	case "google-vertexai":
		return m.validateGoogleVertexAI(ctx, cred, config)
	case "aws-bedrock":
		return m.validateAWSBedrock(ctx, cred, config)
	case "groq":
		return m.validateGroq(ctx, cred, config)
	case "mistral":
		return m.validateMistral(ctx, cred, config)
	default:
		return &ValidationError{
			Provider: provider,
			Message:  "unsupported provider",
		}
	}
}

// validateOpenAI validates OpenAI credentials
func (m *manager) validateOpenAI(ctx context.Context, cred map[string]string, _ map[string]interface{}) error {
	apiKey, ok := cred["api_key"]
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
		if org, ok := cred["organization"]; ok && org != "" {
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

// validateAnthropic validates Anthropic credentials
func (m *manager) validateAnthropic(ctx context.Context, cred map[string]string, _ map[string]interface{}) error {
	apiKey, ok := cred["api_key"]
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

	// Optional: Test the key with a simple API call
	if ctx.Value("skip_validation") == nil {
		client := &http.Client{Timeout: 10 * time.Second}

		// Create a minimal messages request
		reqBody := map[string]interface{}{
			"model": "claude-3-haiku-20240307",
			"messages": []map[string]string{
				{"role": "user", "content": "test"},
			},
			"max_tokens": 1,
		}

		bodyJSON, _ := json.Marshal(reqBody)
		req, err := http.NewRequestWithContext(ctx, "POST", "https://api.anthropic.com/v1/messages", strings.NewReader(string(bodyJSON)))
		if err != nil {
			return fmt.Errorf("failed to create validation request: %w", err)
		}

		req.Header.Set("x-api-key", apiKey)
		req.Header.Set("anthropic-version", "2023-06-01")
		req.Header.Set("Content-Type", "application/json")

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
		// 200 or 400 (usage limit) are both valid - key is correct
		if resp.StatusCode != 200 && resp.StatusCode != 400 && resp.StatusCode != 429 {
			return &ValidationError{
				Provider: "anthropic",
				Message:  fmt.Sprintf("validation failed with status %d", resp.StatusCode),
			}
		}
	}

	return nil
}

// validateAzureOpenAI validates Azure OpenAI credentials
func (m *manager) validateAzureOpenAI(ctx context.Context, cred map[string]string, config map[string]interface{}) error {
	apiKey, ok := cred["api_key"]
	if !ok || apiKey == "" {
		return &ValidationError{
			Provider: "azure",
			Field:    "api_key",
			Message:  "api_key is required",
		}
	}

	// Check required config
	var endpoint, deploymentID string
	if config != nil {
		if e, ok := config["endpoint"].(string); ok {
			endpoint = e
		}
		if d, ok := config["deployment_id"].(string); ok {
			deploymentID = d
		}
	}

	if endpoint == "" {
		return &ValidationError{
			Provider: "azure",
			Field:    "endpoint",
			Message:  "endpoint is required in config",
		}
	}

	if deploymentID == "" {
		return &ValidationError{
			Provider: "azure",
			Field:    "deployment_id",
			Message:  "deployment_id is required in config",
		}
	}

	// Validate endpoint URL
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return &ValidationError{
			Provider: "azure",
			Field:    "endpoint",
			Message:  "invalid endpoint URL",
		}
	}

	// Optional: Test the deployment exists
	if ctx.Value("skip_validation") == nil {
		client := &http.Client{Timeout: 10 * time.Second}

		apiVersion := "2024-02-01"
		if config != nil {
			if v, ok := config["api_version"].(string); ok && v != "" {
				apiVersion = v
			}
		}

		testURL := fmt.Sprintf("%s/openai/deployments/%s/chat/completions?api-version=%s",
			strings.TrimRight(endpoint, "/"), deploymentID, apiVersion)

		req, err := http.NewRequestWithContext(ctx, "POST", testURL, strings.NewReader(`{"messages":[{"role":"user","content":"test"}],"max_tokens":1}`))
		if err != nil {
			return fmt.Errorf("failed to create validation request: %w", err)
		}

		req.Header.Set("api-key", apiKey)
		req.Header.Set("Content-Type", "application/json")

		resp, err := client.Do(req)
		if err != nil {
			return &ValidationError{
				Provider: "azure",
				Message:  "failed to validate deployment: " + err.Error(),
			}
		}
		defer func() { _ = resp.Body.Close() }()

		if resp.StatusCode == 401 {
			return &ValidationError{
				Provider: "azure",
				Field:    "api_key",
				Message:  "invalid API key",
			}
		}
		if resp.StatusCode == 404 {
			return &ValidationError{
				Provider: "azure",
				Field:    "deployment_id",
				Message:  "deployment not found",
			}
		}
	}

	return nil
}

// validateGoogleAIStudio validates Google AI Studio credentials
func (m *manager) validateGoogleAIStudio(_ context.Context, cred map[string]string, _ map[string]interface{}) error {
	apiKey, ok := cred["api_key"]
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
			Message:  "api_key should be 39 characters long",
		}
	}

	return nil
}

// validateGoogleVertexAI validates Google Vertex AI credentials
func (m *manager) validateGoogleVertexAI(_ context.Context, cred map[string]string, _ map[string]interface{}) error {
	serviceAccount, ok := cred["service_account_json"]
	if !ok || serviceAccount == "" {
		return &ValidationError{
			Provider: "google-vertexai",
			Field:    "service_account_json",
			Message:  "service_account_json is required",
		}
	}

	// Parse and validate service account JSON
	var sa map[string]interface{}
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

	// Validate type
	if sa["type"] != "service_account" {
		return &ValidationError{
			Provider: "google-vertexai",
			Field:    "service_account_json",
			Message:  "type must be 'service_account'",
		}
	}

	return nil
}

// validateAWSBedrock validates AWS Bedrock credentials
func (m *manager) validateAWSBedrock(_ context.Context, cred map[string]string, _ map[string]interface{}) error {
	accessKeyID, ok := cred["access_key_id"]
	if !ok || accessKeyID == "" {
		return &ValidationError{
			Provider: "aws-bedrock",
			Field:    "access_key_id",
			Message:  "access_key_id is required",
		}
	}

	secretAccessKey, ok := cred["secret_access_key"]
	if !ok || secretAccessKey == "" {
		return &ValidationError{
			Provider: "aws-bedrock",
			Field:    "secret_access_key",
			Message:  "secret_access_key is required",
		}
	}

	region, ok := cred["region"]
	if !ok || region == "" {
		return &ValidationError{
			Provider: "aws-bedrock",
			Field:    "region",
			Message:  "region is required",
		}
	}

	// Validate AWS access key format
	if !strings.HasPrefix(accessKeyID, "AKIA") && !strings.HasPrefix(accessKeyID, "ASIA") {
		return &ValidationError{
			Provider: "aws-bedrock",
			Field:    "access_key_id",
			Message:  "invalid access key format",
		}
	}

	return nil
}

// validateGroq validates Groq credentials
func (m *manager) validateGroq(_ context.Context, cred map[string]string, _ map[string]interface{}) error {
	apiKey, ok := cred["api_key"]
	if !ok || apiKey == "" {
		return &ValidationError{
			Provider: "groq",
			Field:    "api_key",
			Message:  "api_key is required",
		}
	}

	// Check API key format
	if !strings.HasPrefix(apiKey, "gsk_") {
		return &ValidationError{
			Provider: "groq",
			Field:    "api_key",
			Message:  "api_key must start with 'gsk_'",
		}
	}

	return nil
}

// validateMistral validates Mistral credentials
func (m *manager) validateMistral(_ context.Context, cred map[string]string, _ map[string]interface{}) error {
	apiKey, ok := cred["api_key"]
	if !ok || apiKey == "" {
		return &ValidationError{
			Provider: "mistral",
			Field:    "api_key",
			Message:  "api_key is required",
		}
	}

	// Mistral API keys are typically 32 characters
	if len(apiKey) != 32 {
		return &ValidationError{
			Provider: "mistral",
			Field:    "api_key",
			Message:  "api_key should be 32 characters long",
		}
	}

	return nil
}
