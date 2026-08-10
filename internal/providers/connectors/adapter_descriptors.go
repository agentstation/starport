package connectors

import (
	"context"
	"strings"

	"github.com/agentstation/starmap/pkg/catalogs"
)

const (
	authorizationHeader = "Authorization"
	bearerScheme        = "Bearer"
)

func productionAdapterDescriptors() []AdapterDescriptor {
	chat := catalogs.ProviderOperationChatCompletions
	embeddings := catalogs.ProviderOperationEmbeddings
	apiKeyField := []InferenceCredentialField{{Name: "api_key", Required: true, Sensitive: true}}
	return []AdapterDescriptor{
		{
			ProviderID: catalogs.ProviderIDOpenAI, Operations: []catalogs.ProviderOperation{chat, embeddings},
			EndpointTypes: []catalogs.EndpointType{catalogs.EndpointTypeOpenAI},
			Factory:       connectorFactory(NewOpenAIConnector), Configured: inferenceConfigurationPresent,
			ValidateConfig: requiredAPIKeyConfig, ResolveBaseURL: resolveProviderBase,
			Credential: InferenceCredentialDescriptor{
				Fields: append(apiKeyField, InferenceCredentialField{Name: "organization"}),
				Header: authorizationHeader, Scheme: bearerScheme, Validate: validateOpenAICredential,
			},
		},
		{
			ProviderID: catalogs.ProviderIDAnthropic, Operations: []catalogs.ProviderOperation{chat},
			EndpointTypes: []catalogs.EndpointType{catalogs.EndpointTypeAnthropic},
			Factory:       connectorFactory(NewAnthropicConnector), Configured: inferenceConfigurationPresent,
			ValidateConfig: requiredAPIKeyConfig, ResolveBaseURL: resolveProviderBase,
			Credential: InferenceCredentialDescriptor{
				Fields: apiKeyField, Header: "x-api-key", Validate: validateAnthropicCredential,
			},
		},
		{
			ProviderID: catalogs.ProviderIDGoogleAIStudio, Operations: []catalogs.ProviderOperation{chat, embeddings},
			EndpointTypes: []catalogs.EndpointType{catalogs.EndpointTypeGoogle},
			Factory:       connectorFactory(NewGoogleAIStudioConnector), Configured: inferenceConfigurationPresent,
			ValidateConfig: requiredAPIKeyConfig, ResolveBaseURL: resolveProviderBase,
			Credential: InferenceCredentialDescriptor{
				Fields: apiKeyField, Header: "x-goog-api-key", Validate: validateGoogleAIStudioCredential,
			},
		},
		{
			ProviderID: catalogs.ProviderIDGoogleVertex, Operations: []catalogs.ProviderOperation{chat, embeddings},
			EndpointTypes: []catalogs.EndpointType{catalogs.EndpointTypeGoogleCloud, catalogs.EndpointTypeAnthropic},
			Factory:       connectorFactory(NewVertexAIConnector), Configured: inferenceConfigurationPresent,
			ValidateConfig: requiredCloudCredentialConfig, ResolveBaseURL: resolveProviderBase,
			Credential: InferenceCredentialDescriptor{
				Fields: []InferenceCredentialField{{Name: "access_token", Required: true, Sensitive: true}},
				Header: authorizationHeader, Scheme: bearerScheme, Validate: validateGoogleVertexCredential,
			},
		},
		{
			ProviderID: catalogs.ProviderIDGroq, Operations: []catalogs.ProviderOperation{chat},
			EndpointTypes: []catalogs.EndpointType{catalogs.EndpointTypeOpenAI},
			Factory:       connectorFactory(NewGroqConnector), Configured: inferenceConfigurationPresent,
			ValidateConfig: requiredAPIKeyConfig, ResolveBaseURL: resolveProviderBase,
			Credential: InferenceCredentialDescriptor{
				Fields: apiKeyField, Header: authorizationHeader, Scheme: bearerScheme, Validate: validateGroqCredential,
			},
		},
		{
			ProviderID: catalogs.ProviderIDMistralAI, Operations: []catalogs.ProviderOperation{chat, embeddings},
			EndpointTypes: []catalogs.EndpointType{catalogs.EndpointTypeOpenAI},
			Factory:       connectorFactory(NewMistralConnector), Configured: inferenceConfigurationPresent,
			ValidateConfig: requiredAPIKeyConfig, ResolveBaseURL: resolveProviderBase,
			Credential: InferenceCredentialDescriptor{
				Fields: apiKeyField, Header: authorizationHeader, Scheme: bearerScheme, Validate: validateMistralCredential,
			},
		},
		{
			ProviderID: catalogs.ProviderIDAzureOpenAI, Operations: []catalogs.ProviderOperation{chat, embeddings},
			EndpointTypes: []catalogs.EndpointType{catalogs.EndpointTypeOpenAI},
			Factory:       connectorFactory(NewAzureOpenAIConnector), Configured: azureConfigured,
			ValidateConfig: requiredAzureConfig, ResolveBaseURL: resolveProviderBase,
			Credential: InferenceCredentialDescriptor{
				Fields: []InferenceCredentialField{{Name: "api_key", Required: true, Sensitive: true}},
				Header: "api-key", Validate: validateAzureCredential,
			},
		},
		{
			ProviderID: catalogs.ProviderIDOllama, Operations: []catalogs.ProviderOperation{chat, embeddings},
			EndpointTypes: []catalogs.EndpointType{catalogs.EndpointTypeOllama},
			Factory:       connectorFactory(NewOllamaConnector), Configured: ollamaConfigured,
			ResolveBaseURL: resolveProviderBase,
		},
	}
}

func connectorFactory[T Connector](factory func(ProviderConfig) (T, error)) ConnectorFactory {
	return func(config ProviderConfig) (Connector, error) { return factory(config) }
}

func validateOpenAICredential(_ context.Context, key map[string]string, _ map[string]any) error {
	apiKey, err := requiredCredential("openai", key, "api_key")
	if err != nil {
		return err
	}
	if !strings.HasPrefix(apiKey, "sk-") {
		return credentialValidationError("openai", "api_key", "must start with 'sk-'")
	}
	return nil
}

func validateAnthropicCredential(_ context.Context, key map[string]string, _ map[string]any) error {
	apiKey, err := requiredCredential("anthropic", key, "api_key")
	if err != nil {
		return err
	}
	if !strings.HasPrefix(apiKey, "sk-ant-") {
		return credentialValidationError("anthropic", "api_key", "must start with 'sk-ant-'")
	}
	return nil
}

func validateAzureCredential(_ context.Context, key map[string]string, _ map[string]any) error {
	_, err := requiredCredential("azure-openai", key, "api_key")
	return err
}

func validateGoogleAIStudioCredential(_ context.Context, key map[string]string, _ map[string]any) error {
	apiKey, err := requiredCredential("google-ai-studio", key, "api_key")
	if err != nil {
		return err
	}
	if len(apiKey) != 39 {
		return credentialValidationError("google-ai-studio", "api_key", "has invalid length")
	}
	return nil
}

func validateGoogleVertexCredential(_ context.Context, key map[string]string, _ map[string]any) error {
	_, err := requiredCredential("google-vertex", key, "access_token")
	return err
}

func validateGroqCredential(_ context.Context, key map[string]string, _ map[string]any) error {
	apiKey, err := requiredCredential("groq", key, "api_key")
	if err != nil {
		return err
	}
	if !strings.HasPrefix(apiKey, "gsk_") {
		return credentialValidationError("groq", "api_key", "must start with 'gsk_'")
	}
	return nil
}

func validateMistralCredential(_ context.Context, key map[string]string, _ map[string]any) error {
	apiKey, err := requiredCredential("mistral", key, "api_key")
	if err != nil {
		return err
	}
	if len(apiKey) != 32 {
		return credentialValidationError("mistral", "api_key", "has invalid length")
	}
	return nil
}

func requiredCredential(provider string, key map[string]string, field string) (string, error) {
	value := strings.TrimSpace(key[field])
	if value == "" {
		return "", credentialValidationError(provider, field, "is required")
	}
	return value, nil
}

func credentialValidationError(provider, field, message string) error {
	return &InferenceCredentialValidationError{Provider: provider, Field: field, Message: message}
}

// InferenceCredentialValidationError reports an invalid provider inference credential.
type InferenceCredentialValidationError struct {
	Provider string
	Field    string
	Message  string
}

func (e *InferenceCredentialValidationError) Error() string {
	if e.Field != "" {
		return "validation failed for " + e.Provider + " " + e.Field + ": " + e.Message
	}
	return "validation failed for " + e.Provider + ": " + e.Message
}

var _ error = (*InferenceCredentialValidationError)(nil)
