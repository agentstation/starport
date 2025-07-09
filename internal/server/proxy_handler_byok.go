package server

// This file contains a partial implementation of BYOK proxy handling.
// The methods are marked as unused because they will be integrated
// with the main proxy handler in a full implementation.

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/agentstation/starport/internal/byok"
	"github.com/agentstation/starport/internal/connectors"
	"github.com/agentstation/starport/internal/storage"
	"github.com/rs/zerolog/log"
)

// BYOKProxyHandler extends ProxyHandler with BYOK support
type BYOKProxyHandler struct {
	*ProxyHandler
	byokManager byok.Manager
}

// NewBYOKProxyHandler creates a new BYOK-aware proxy handler
func NewBYOKProxyHandler(registry *ConnectorRegistry, byokManager byok.Manager) *BYOKProxyHandler {
	return &BYOKProxyHandler{
		ProxyHandler: NewProxyHandler(registry),
		byokManager:  byokManager,
	}
}

// proxyWithBYOK handles requests using BYOK credentials
func (h *BYOKProxyHandler) proxyWithBYOK(ctx context.Context, w http.ResponseWriter, r *http.Request, 
	apiKeyID string, provider string, modelID string, originalConnector connectors.Connector) error { //nolint:unused // will be used in full implementation
	
	// Get BYOK credential
	credential, err := h.byokManager.GetCredential(ctx, apiKeyID, provider)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return fmt.Errorf("no BYOK credential found for provider %s", provider)
		}
		return fmt.Errorf("failed to get BYOK credential: %w", err)
	}

	// Create a connector with BYOK credential
	byokConnector, err := h.createBYOKConnector(provider, credential, originalConnector)
	if err != nil {
		return fmt.Errorf("failed to create BYOK connector: %w", err)
	}

	// Record usage for billing
	defer func() {
		// Extract usage from response
		usage := extractUsageFromResponse(w)
		if usage != nil {
			usage.Provider = provider
			usage.Model = modelID
			if err := h.byokManager.RecordUsage(ctx, apiKeyID, provider, usage); err != nil {
				log.Error().Err(err).Msg("Failed to record BYOK usage")
			}
			
			// Calculate and set BYOK cost header
			cost := h.byokManager.CalculateBYOKCost(usage)
			w.Header().Set("X-BYOK-Cost", fmt.Sprintf("%.6f", cost))
		}
	}()

	// Set key type header
	w.Header().Set("X-Key-Type", string(byok.KeyTypeBYOK))
	w.Header().Set("X-Provider-Used", provider)

	// Proxy the request using BYOK connector
	return h.proxyToConnector(ctx, w, r, byokConnector, modelID)
}

// proxyWithGateway handles requests using gateway keys
func (h *BYOKProxyHandler) proxyWithGateway(ctx context.Context, w http.ResponseWriter, r *http.Request,
	provider string, modelID string, connector connectors.Connector) error { //nolint:unused // will be used in full implementation
	
	// Set key type header
	w.Header().Set("X-Key-Type", string(byok.KeyTypeGateway))
	w.Header().Set("X-Provider-Used", provider)

	// Proxy using gateway connector
	return h.proxyToConnector(ctx, w, r, connector, modelID)
}

// proxyWithDefaultKey handles requests using default provider keys
func (h *BYOKProxyHandler) proxyWithDefaultKey(ctx context.Context, w http.ResponseWriter, r *http.Request,
	provider string, modelID string, originalConnector connectors.Connector) error { //nolint:unused // will be used in full implementation
	
	// Get default key
	credential, err := h.byokManager.GetDefaultKey(ctx, provider)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return fmt.Errorf("no default key found for provider %s", provider)
		}
		return fmt.Errorf("failed to get default key: %w", err)
	}

	// Create a connector with default key
	defaultConnector, err := h.createBYOKConnector(provider, credential, originalConnector)
	if err != nil {
		return fmt.Errorf("failed to create default key connector: %w", err)
	}

	// Set key type header
	w.Header().Set("X-Key-Type", string(byok.KeyTypeDefault))
	w.Header().Set("X-Provider-Used", provider)

	// Proxy using default key connector
	return h.proxyToConnector(ctx, w, r, defaultConnector, modelID)
}

// determineKeyStrategy determines which key to use for a request
func (h *BYOKProxyHandler) determineKeyStrategy(ctx context.Context, apiKeyID string, provider string) byok.FallbackStrategy { //nolint:unused // will be used in full implementation
	return h.byokManager.DetermineKeyStrategy(ctx, apiKeyID, provider)
}

// routeRequestWithBYOK routes a request considering BYOK credentials
func (h *BYOKProxyHandler) routeRequestWithBYOK(ctx context.Context, w http.ResponseWriter, r *http.Request,
	apiKeyID string, modelID string) error { //nolint:unused // will be used in full implementation
	
	// Extract provider from model ID
	provider := extractProvider(modelID)
	if provider == "" {
		return fmt.Errorf("invalid model ID format: %s", modelID)
	}

	// Get the base connector
	connector, model, err := h.connectorRegistry.GetByModel(modelID)
	if err != nil {
		return err
	}

	// Determine key selection strategy
	strategy := h.determineKeyStrategy(ctx, apiKeyID, provider)

	log.Debug().
		Str("api_key_id", apiKeyID).
		Str("provider", provider).
		Str("strategy", string(strategy)).
		Msg("Determined key strategy")

	switch strategy {
	case byok.BYOKOnly:
		// Only use BYOK credentials
		return h.proxyWithBYOK(ctx, w, r, apiKeyID, provider, model, connector)

	case byok.BYOKFirst:
		// Try BYOK first, fallback to gateway
		err := h.proxyWithBYOK(ctx, w, r, apiKeyID, provider, model, connector)
		if err != nil {
			log.Warn().Err(err).Msg("BYOK failed, falling back to gateway")
			return h.proxyWithGateway(ctx, w, r, provider, model, connector)
		}
		return nil

	case byok.GatewayFirst:
		// Try gateway first, fallback to BYOK on rate limit
		err := h.proxyWithGateway(ctx, w, r, provider, model, connector)
		if err != nil && isRateLimitError(err) {
			log.Info().Msg("Gateway rate limited, falling back to BYOK")
			return h.proxyWithBYOK(ctx, w, r, apiKeyID, provider, model, connector)
		}
		return err

	default:
		// Default to gateway
		return h.proxyWithGateway(ctx, w, r, provider, model, connector)
	}
}

// createBYOKConnector creates a new connector instance with BYOK credentials
func (h *BYOKProxyHandler) createBYOKConnector(provider string, credential *byok.Credential, 
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
	
	// Set provider-specific configuration
	switch provider {
	case "openai":
		config.BaseURL = "https://api.openai.com/v1"
		config.APIKey = credential.Data["api_key"]
		if org, ok := credential.Data["organization"]; ok {
			config.Extra["organization"] = org
		}
		return connectors.NewOpenAIConnector(config)

	case "anthropic":
		config.BaseURL = "https://api.anthropic.com/v1"
		config.APIKey = credential.Data["api_key"]
		return connectors.NewAnthropicConnector(config)

	case "azure", "azure-openai":
		config.APIKey = credential.Data["api_key"]
		if endpoint, ok := credential.Config["endpoint"].(string); ok {
			config.BaseURL = endpoint
		}
		if deployment, ok := credential.Config["deployment_id"].(string); ok {
			config.Extra["deployment_id"] = deployment
		}
		if apiVersion, ok := credential.Config["api_version"].(string); ok {
			config.Extra["api_version"] = apiVersion
		}
		return connectors.NewAzureOpenAIConnector(config)

	case "google-aistudio":
		config.BaseURL = "https://generativelanguage.googleapis.com/v1beta"
		config.APIKey = credential.Data["api_key"]
		return connectors.NewGoogleAIStudioConnector(config)

	case "google-vertexai":
		config.BaseURL = "https://us-central1-aiplatform.googleapis.com/v1"
		if projectID, ok := credential.Config["project_id"].(string); ok {
			config.Extra["project_id"] = projectID
		}
		if location, ok := credential.Config["location"].(string); ok {
			config.Extra["location"] = location
			// Update base URL with location
			config.BaseURL = fmt.Sprintf("https://%s-aiplatform.googleapis.com/v1", location)
		}
		config.Extra["service_account_json"] = credential.Data["service_account_json"]
		return connectors.NewVertexAIConnector(config)

	case "groq":
		config.BaseURL = "https://api.groq.com/openai/v1"
		config.APIKey = credential.Data["api_key"]
		return connectors.NewGroqConnector(config)

	case "mistral":
		config.BaseURL = "https://api.mistral.ai/v1"
		config.APIKey = credential.Data["api_key"]
		return connectors.NewMistralConnector(config)

	default:
		return nil, fmt.Errorf("unsupported provider for BYOK: %s", provider)
	}
}

// proxyToConnector is a helper to proxy requests to a specific connector
func (h *BYOKProxyHandler) proxyToConnector(_ context.Context, _ http.ResponseWriter, _ *http.Request,
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
func extractUsageFromResponse(_ http.ResponseWriter) *byok.Usage {
	// This would extract usage from the response headers or body
	// For now, return nil
	return nil
}