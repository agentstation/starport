//go:build integration

package server

// This file contains a partial implementation of provider key proxy handling.
// The methods are marked as unused because they will be integrated
// with the main proxy handler in a full implementation.

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/agentstation/starport/internal/providers"
	"github.com/agentstation/starport/internal/connectors"
	"github.com/agentstation/starport/internal/models"
	"github.com/agentstation/starport/internal/storage"
	"github.com/rs/zerolog/log"
)

// ProviderKeysProxyHandler extends ProxyHandler with provider key support
type ProviderKeysProxyHandler struct {
	*ProxyHandler
	keysManager providers.KeyManager
}

// NewProviderKeysProxyHandler creates a new provider key-aware proxy handler
func NewProviderKeysProxyHandler(registry *ConnectorRegistry, keysManager providers.KeyManager) *ProviderKeysProxyHandler {
	return &ProviderKeysProxyHandler{
		ProxyHandler: NewProxyHandler(registry),
		keysManager:  keysManager,
	}
}

// proxyWithProviderKey handles requests using provider keys
func (h *ProviderKeysProxyHandler) proxyWithProviderKey(ctx context.Context, w http.ResponseWriter, r *http.Request, 
	scope string, provider string, modelID string, originalConnector connectors.Connector) error { //nolint:unused // will be used in full implementation
	
	// Get provider key
	key, err := h.keysManager.GetKey(ctx, scope, provider)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return fmt.Errorf("no provider key found for provider %s", provider)
		}
		return fmt.Errorf("failed to get provider key: %w", err)
	}

	// Create a connector with provider key
	keyConnector, err := h.createProviderKeyConnector(provider, key, originalConnector)
	if err != nil {
		return fmt.Errorf("failed to create provider key connector: %w", err)
	}

	// Record usage for billing
	defer func() {
		// Extract usage from response
		usage := extractUsageFromResponse(w)
		if usage != nil {
			usage.Provider = provider
			usage.Model = modelID
			if err := h.keysManager.RecordUsage(ctx, scope, provider, usage); err != nil {
				log.Error().Err(err).Msg("Failed to record provider key usage")
			}
			
			// Calculate and set provider key cost header
			cost := h.keysManager.CalculateProviderKeyCost(usage)
			w.Header().Set("X-Provider-Key-Cost", fmt.Sprintf("%.6f", cost))
		}
	}()

	// Set key type header
	w.Header().Set("X-Key-Type", string(providers.KeyTypeUser))
	w.Header().Set("X-Provider-Used", provider)

	// Proxy the request using provider key connector
	return h.proxyToConnector(ctx, w, r, keyConnector, modelID)
}

// proxyWithGateway handles requests using gateway keys
func (h *ProviderKeysProxyHandler) proxyWithGateway(ctx context.Context, w http.ResponseWriter, r *http.Request,
	provider string, modelID string, connector connectors.Connector) error { //nolint:unused // will be used in full implementation
	
	// Set key type header
	w.Header().Set("X-Key-Type", string(providers.KeyTypeGateway))
	w.Header().Set("X-Provider-Used", provider)

	// Proxy using gateway connector
	return h.proxyToConnector(ctx, w, r, connector, modelID)
}

// proxyWithGlobalKey handles requests using global provider keys
func (h *ProviderKeysProxyHandler) proxyWithGlobalKey(ctx context.Context, w http.ResponseWriter, r *http.Request,
	provider string, modelID string, originalConnector connectors.Connector) error { //nolint:unused // will be used in full implementation
	
	// Get global key
	key, err := h.keysManager.GetGlobalKey(ctx, provider)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return fmt.Errorf("no global key found for provider %s", provider)
		}
		return fmt.Errorf("failed to get global key: %w", err)
	}

	// Create a connector with global key
	globalConnector, err := h.createProviderKeyConnector(provider, key, originalConnector)
	if err != nil {
		return fmt.Errorf("failed to create global key connector: %w", err)
	}

	// Set key type header
	w.Header().Set("X-Key-Type", string(providers.KeyTypeGateway))
	w.Header().Set("X-Provider-Used", provider)

	// Proxy using global key connector
	return h.proxyToConnector(ctx, w, r, globalConnector, modelID)
}

// determineKeyStrategy determines which key to use for a request
func (h *ProviderKeysProxyHandler) determineKeyStrategy(ctx context.Context, scope string, provider string) providers.FallbackStrategy { //nolint:unused // will be used in full implementation
	return h.keysManager.DetermineKeyStrategy(ctx, scope, provider)
}

// routeRequestWithProviderKeys routes a request considering provider keys
func (h *ProviderKeysProxyHandler) routeRequestWithProviderKeys(ctx context.Context, w http.ResponseWriter, r *http.Request,
	apiKeyID string, modelID string) error { //nolint:unused // will be used in full implementation
	
	// Extract provider from model ID
	provider := extractProvider(modelID)
	if provider == "" {
		return fmt.Errorf("invalid model ID format: %s", modelID)
	}

	// Convert apiKeyID to scope
	scope := "user:" + apiKeyID

	// Get the base connector
	connector, model, err := h.connectorRegistry.GetByModel(modelID)
	if err != nil {
		return err
	}

	// Determine key selection strategy
	strategy := h.determineKeyStrategy(ctx, scope, provider)

	log.Debug().
		Str("scope", scope).
		Str("provider", provider).
		Str("strategy", string(strategy)).
		Msg("Determined key strategy")

	switch strategy {
	case providers.UserKeyOnly:
		// Only use user provider keys
		return h.proxyWithProviderKey(ctx, w, r, scope, provider, model, connector)

	case providers.UserKeyFirst:
		// Try user key first, fallback to gateway
		err := h.proxyWithProviderKey(ctx, w, r, scope, provider, model, connector)
		if err != nil {
			log.Warn().Err(err).Msg("User provider key failed, falling back to gateway")
			return h.proxyWithGateway(ctx, w, r, provider, model, connector)
		}
		return nil

	case providers.GatewayFirst:
		// Try gateway first, fallback to user key on rate limit
		err := h.proxyWithGateway(ctx, w, r, provider, model, connector)
		if err != nil && isRateLimitError(err) {
			log.Info().Msg("Gateway rate limited, falling back to user provider key")
			return h.proxyWithProviderKey(ctx, w, r, scope, provider, model, connector)
		}
		return err

	default:
		// Default to gateway
		return h.proxyWithGateway(ctx, w, r, provider, model, connector)
	}
}

// createProviderKeyConnector creates a new connector instance with provider keys
func (h *ProviderKeysProxyHandler) createProviderKeyConnector(provider string, key *models.ProviderKey, 
	_ connectors.Connector) (connectors.Connector, error) { //nolint:unused // will be used in full implementation
	
	// Create a new provider config with default values
	config := connectors.ProviderConfig{
		Timeout:           30 * time.Second,
		MaxConnections:    100,
		MaxRetries:        3,
		RetryDelay:        1 * time.Second,
		BackoffMultiplier: 2.0,
		Extra:             make(map[string]interface{}),
	}
	
	// TODO: Decrypt the provider key before using it
	// For now, return a placeholder error
	_ = key // key is intentionally unused until decryption is implemented
	
	// Set provider-specific configuration
	switch provider {
	case "openai":
		config.BaseURL = "https://api.openai.com/v1"
		// // config.APIKey = key.Data["api_key"] // TODO: decrypt key first // TODO: decrypt key first
		return connectors.NewOpenAIConnector(config)

	case "anthropic":
		config.BaseURL = "https://api.anthropic.com/v1"
		// config.APIKey = key.Data["api_key"] // TODO: decrypt key first
		return connectors.NewAnthropicConnector(config)

	case "azure", "azure-openai":
		// config.APIKey = key.Data["api_key"] // TODO: decrypt key first
		if endpoint, ok := key.Config["endpoint"].(string); ok {
			config.BaseURL = endpoint
		}
		if deployment, ok := key.Config["deployment_id"].(string); ok {
			config.Extra["deployment_id"] = deployment
		}
		if apiVersion, ok := key.Config["api_version"].(string); ok {
			config.Extra["api_version"] = apiVersion
		}
		return connectors.NewAzureOpenAIConnector(config)

	case "google-aistudio":
		config.BaseURL = "https://generativelanguage.googleapis.com/v1beta"
		// config.APIKey = key.Data["api_key"] // TODO: decrypt key first
		return connectors.NewGoogleAIStudioConnector(config)

	case "google-vertexai":
		config.BaseURL = "https://us-central1-aiplatform.googleapis.com/v1"
		if projectID, ok := key.Config["project_id"].(string); ok {
			config.Extra["project_id"] = projectID
		}
		if location, ok := key.Config["location"].(string); ok {
			config.Extra["location"] = location
			// Update base URL with location
			config.BaseURL = fmt.Sprintf("https://%s-aiplatform.googleapis.com/v1", location)
		}
		// config.Extra["service_account_json"] = key.Data["service_account_json"] // TODO: decrypt key first
		return connectors.NewVertexAIConnector(config)

	case "groq":
		config.BaseURL = "https://api.groq.com/openai/v1"
		// config.APIKey = key.Data["api_key"] // TODO: decrypt key first
		return connectors.NewGroqConnector(config)

	case "mistral":
		config.BaseURL = "https://api.mistral.ai/v1"
		// config.APIKey = key.Data["api_key"] // TODO: decrypt key first
		return connectors.NewMistralConnector(config)

	default:
		return nil, fmt.Errorf("unsupported provider for provider key: %s", provider)
	}
}

// proxyToConnector is a helper to proxy requests to a specific connector
func (h *ProviderKeysProxyHandler) proxyToConnector(_ context.Context, _ http.ResponseWriter, _ *http.Request,
	_ connectors.Connector, _ string) error {
	
	// This would call the appropriate method on the connector
	// For now, return a placeholder error
	return errors.New("proxy to connector not implemented")
}

// Helper functions

// extractProvider extracts the provider from a model ID (e.g., "openai/gpt-4" -> "openai")
func extractProvider(modelID string) string { //nolint:unused // will be used in full implementation
	parts := strings.SplitN(modelID, "/", 2)
	if len(parts) == 2 {
		return parts[0]
	}
	return ""
}

// isRateLimitError checks if an error is a rate limit error
func isRateLimitError(err error) bool { //nolint:unused // will be used in full implementation
	// Check for common rate limit error patterns
	if err == nil {
		return false
	}
	errStr := err.Error()
	return strings.Contains(errStr, "rate limit") || 
		   strings.Contains(errStr, "429") ||
		   strings.Contains(errStr, "too many requests")
}

// extractUsageFromResponse extracts usage information from the response
func extractUsageFromResponse(_ http.ResponseWriter) *providers.Usage {
	// This would extract usage from the response headers or body
	// For now, return nil
	return nil
}