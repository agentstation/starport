package byok

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/agentstation/starport/internal/models"
	"github.com/agentstation/starport/internal/storage"
	"github.com/rs/zerolog/log"
)

// manager implements the Manager interface
type manager struct {
	store      storage.KVStore
	encryption *models.EncryptionService
}

// NewManager creates a new BYOK manager
func NewManager(store storage.KVStore, masterKey []byte) (Manager, error) {
	if store == nil {
		return nil, ErrStoreRequired
	}

	encryption, err := models.NewEncryptionService(masterKey)
	if err != nil {
		return nil, fmt.Errorf("failed to create encryption service: %w", err)
	}

	return &manager{
		store:      store,
		encryption: encryption,
	}, nil
}

// AddCredential adds a new BYOK credential for an API key
func (m *manager) AddCredential(ctx context.Context, apiKeyID, provider string, cred map[string]string, config map[string]interface{}) error {
	// Validate inputs
	if apiKeyID == "" {
		return ErrAPIKeyIDRequired
	}
	if provider == "" {
		return ErrProviderRequired
	}

	// Validate credential format for provider
	if err := m.ValidateCredential(ctx, provider, cred, config); err != nil {
		return err
	}

	// Serialize credential data
	credJSON, err := json.Marshal(cred)
	if err != nil {
		return fmt.Errorf("failed to serialize credential: %w", err)
	}

	// Encrypt credential
	encryptedCred, err := m.encryption.EncryptCredential(string(credJSON))
	if err != nil {
		return fmt.Errorf("failed to encrypt credential: %w", err)
	}

	// Create provider key model
	providerKey := &models.ProviderKey{
		Scope:               "user:" + apiKeyID,
		Provider:            provider,
		EncryptedCredential: encryptedCred,
		Config:              config,
		IsFallback:          true, // Default to allowing fallback
		Priority:            0,    // Default highest priority
		CreatedAt:           time.Now(),
		UpdatedAt:           time.Now(),
		UsageCount:          0,
	}

	// Validate model
	if err := providerKey.Validate(); err != nil {
		return fmt.Errorf("invalid credential: %w", err)
	}

	// Store credential
	key := models.ProviderKeyStorageKey("user:"+apiKeyID, provider)
	data, err := storage.SerializeModel(providerKey)
	if err != nil {
		return fmt.Errorf("failed to serialize credential: %w", err)
	}

	if err := m.store.Set(ctx, key, data); err != nil {
		return fmt.Errorf("failed to store credential: %w", err)
	}

	log.Info().
		Str("api_key_id", apiKeyID).
		Str("provider", provider).
		Msg("BYOK credential added")

	return nil
}

// GetCredential retrieves the highest priority BYOK credential for a provider
func (m *manager) GetCredential(ctx context.Context, apiKeyID, provider string) (*Credential, error) {
	creds, err := m.GetCredentials(ctx, apiKeyID, provider)
	if err != nil {
		return nil, err
	}
	if len(creds) == 0 {
		return nil, storage.ErrNotFound
	}
	return creds[0], nil
}

// GetCredentials retrieves all BYOK credentials for a provider sorted by priority
// This includes both user-specific credentials and global credentials
func (m *manager) GetCredentials(ctx context.Context, apiKeyID, provider string) ([]*Credential, error) {
	if apiKeyID == "" || provider == "" {
		return nil, ErrAPIKeyAndProviderRequired
	}

	var allKeys []string

	// 1. Get user-specific credentials
	userPrefix := fmt.Sprintf("credential:%s:", apiKeyID)
	userKeys, err := m.store.ScanWithPrefix(ctx, userPrefix, 1000)
	if err != nil {
		return nil, fmt.Errorf("failed to list user credentials: %w", err)
	}
	allKeys = append(allKeys, userKeys...)

	// 2. Get global credentials
	globalKey := models.ProviderKeyStorageKey("*", provider)
	if _, err := m.store.Get(ctx, globalKey); err == nil {
		allKeys = append(allKeys, globalKey)
	}

	var credentials []*Credential
	for _, key := range allKeys {
		// Extract provider from key
		parts := strings.Split(key, ":")
		if len(parts) < 3 {
			continue
		}
		keyProvider := parts[2]
		if keyProvider != provider {
			continue
		}

		// Get credential data
		data, err := m.store.Get(ctx, key)
		if err != nil {
			log.Warn().Str("key", key).Err(err).Msg("Failed to get credential")
			continue
		}

		// Deserialize credential
		var providerKey models.ProviderKey
		if err := storage.DeserializeModel(data, &providerKey); err != nil {
			log.Warn().Str("key", key).Err(err).Msg("Failed to deserialize credential")
			continue
		}

		// Decrypt credential
		decryptedJSON, err := m.encryption.DecryptCredential(providerKey.EncryptedCredential)
		if err != nil {
			log.Warn().Str("key", key).Err(err).Msg("Failed to decrypt credential")
			continue
		}

		var credData map[string]string
		if err := json.Unmarshal([]byte(decryptedJSON), &credData); err != nil {
			log.Warn().Str("key", key).Err(err).Msg("Failed to unmarshal credential")
			continue
		}

		// Convert to Credential
		cred := &Credential{
			Provider:   providerKey.Provider,
			Data:       credData,
			Config:     providerKey.Config,
			IsFallback: providerKey.IsFallback,
			Priority:   providerKey.Priority,
			RateLimit:  providerKey.RateLimit,
			CreatedAt:  providerKey.CreatedAt,
			LastUsed:   providerKey.LastUsed,
			UsageCount: providerKey.UsageCount,
		}

		credentials = append(credentials, cred)
	}

	// Sort by priority (lower number = higher priority)
	// Global credentials typically have higher priority values (lower precedence)
	sort.Slice(credentials, func(i, j int) bool {
		return credentials[i].Priority < credentials[j].Priority
	})

	return credentials, nil
}

// ListCredentials lists all BYOK credentials for an API key
func (m *manager) ListCredentials(ctx context.Context, apiKeyID string) ([]*Credential, error) {
	if apiKeyID == "" {
		return nil, ErrAPIKeyIDRequired
	}

	// List all credentials for this API key
	prefix := fmt.Sprintf("credential:%s:", apiKeyID)
	keys, err := m.store.ScanWithPrefix(ctx, prefix, 1000)
	if err != nil {
		return nil, fmt.Errorf("failed to list credentials: %w", err)
	}

	var credentials []*Credential
	for _, key := range keys {
		// Get credential data
		data, err := m.store.Get(ctx, key)
		if err != nil {
			log.Warn().Str("key", key).Err(err).Msg("Failed to get credential")
			continue
		}

		// Deserialize credential
		var providerKey models.ProviderKey
		if err := storage.DeserializeModel(data, &providerKey); err != nil {
			log.Warn().Str("key", key).Err(err).Msg("Failed to deserialize credential")
			continue
		}

		// Don't decrypt for listing - just return metadata
		cred := &Credential{
			Provider:   providerKey.Provider,
			Config:     providerKey.Config,
			IsFallback: providerKey.IsFallback,
			Priority:   providerKey.Priority,
			CreatedAt:  providerKey.CreatedAt,
			LastUsed:   providerKey.LastUsed,
			UsageCount: providerKey.UsageCount,
		}

		credentials = append(credentials, cred)
	}

	return credentials, nil
}

// UpdateCredential updates an existing BYOK credential
func (m *manager) UpdateCredential(ctx context.Context, apiKeyID, provider string, cred map[string]string, config map[string]interface{}) error {
	// Get existing credential
	key := models.ProviderKeyStorageKey("user:"+apiKeyID, provider)
	data, err := m.store.Get(ctx, key)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return ErrCredentialNotFound
		}
		return fmt.Errorf("failed to get credential: %w", err)
	}

	// Deserialize existing
	var providerKey models.ProviderKey
	if err := storage.DeserializeModel(data, &providerKey); err != nil {
		return fmt.Errorf("failed to deserialize credential: %w", err)
	}

	// Validate new credential if provided
	if len(cred) > 0 {
		if err := m.ValidateCredential(ctx, provider, cred, config); err != nil {
			return err
		}

		// Serialize and encrypt new credential
		credJSON, err := json.Marshal(cred)
		if err != nil {
			return fmt.Errorf("failed to serialize credential: %w", err)
		}

		encryptedCred, err := m.encryption.EncryptCredential(string(credJSON))
		if err != nil {
			return fmt.Errorf("failed to encrypt credential: %w", err)
		}

		providerKey.EncryptedCredential = encryptedCred
	}

	// Update config if provided
	if config != nil {
		providerKey.Config = config
	}

	// Update timestamp
	providerKey.UpdatedAt = time.Now()

	// Validate and store
	if err := providerKey.Validate(); err != nil {
		return fmt.Errorf("invalid credential: %w", err)
	}

	data, err = storage.SerializeModel(&providerKey)
	if err != nil {
		return fmt.Errorf("failed to serialize credential: %w", err)
	}

	if err := m.store.Set(ctx, key, data); err != nil {
		return fmt.Errorf("failed to store credential: %w", err)
	}

	log.Info().
		Str("api_key_id", apiKeyID).
		Str("provider", provider).
		Msg("BYOK credential updated")

	return nil
}

// DeleteCredential removes a BYOK credential
func (m *manager) DeleteCredential(ctx context.Context, apiKeyID, provider string) error {
	if apiKeyID == "" || provider == "" {
		return fmt.Errorf("%w and %w", ErrAPIKeyIDRequired, ErrProviderRequired)
	}

	key := models.ProviderKeyStorageKey("user:"+apiKeyID, provider)
	
	// Check if credential exists first
	if _, err := m.store.Get(ctx, key); err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return ErrCredentialNotFound
		}
		return fmt.Errorf("failed to check credential: %w", err)
	}
	
	if err := m.store.Delete(ctx, key); err != nil {
		return fmt.Errorf("failed to delete credential: %w", err)
	}

	log.Info().
		Str("api_key_id", apiKeyID).
		Str("provider", provider).
		Msg("BYOK credential deleted")

	return nil
}

// SetGlobalCredential sets a gateway-wide credential for a provider
func (m *manager) SetGlobalCredential(ctx context.Context, provider string, cred map[string]string, config map[string]interface{}, rateLimit *models.RateLimitConfig) error {
	if provider == "" {
		return ErrProviderRequired
	}

	// Validate credential
	if err := m.ValidateCredential(ctx, provider, cred, config); err != nil {
		return err
	}

	// Serialize and encrypt credential
	credJSON, err := json.Marshal(cred)
	if err != nil {
		return fmt.Errorf("failed to serialize credential: %w", err)
	}

	encryptedCred, err := m.encryption.EncryptCredential(string(credJSON))
	if err != nil {
		return fmt.Errorf("failed to encrypt credential: %w", err)
	}

	// Create global credential as a ProviderKey with scope = "*"
	globalKey := &models.ProviderKey{
		Scope:               "*",
		Provider:            provider,
		EncryptedCredential: encryptedCred,
		Config:              config,
		RateLimit:           rateLimit,
		Priority:            100, // Lower priority than user credentials
		CreatedAt:           time.Now(),
		UpdatedAt:           time.Now(),
	}

	// Validate model
	if err := globalKey.Validate(); err != nil {
		return fmt.Errorf("invalid global credential: %w", err)
	}

	// Store global credential
	key := models.GlobalProviderKeyStorageKey(provider)
	data, err := storage.SerializeModel(globalKey)
	if err != nil {
		return fmt.Errorf("failed to serialize global credential: %w", err)
	}

	if err := m.store.Set(ctx, key, data); err != nil {
		return fmt.Errorf("failed to store global credential: %w", err)
	}

	log.Info().
		Str("provider", provider).
		Msg("Global credential set")

	return nil
}

// GetGlobalCredential retrieves the global credential for a provider
func (m *manager) GetGlobalCredential(ctx context.Context, provider string) (*Credential, error) {
	if provider == "" {
		return nil, ErrProviderRequired
	}

	key := models.GlobalProviderKeyStorageKey(provider)
	data, err := m.store.Get(ctx, key)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return nil, storage.ErrNotFound
		}
		return nil, fmt.Errorf("failed to get global credential: %w", err)
	}

	// Deserialize ProviderKey
	var globalKey models.ProviderKey
	if err := storage.DeserializeModel(data, &globalKey); err != nil {
		return nil, fmt.Errorf("failed to deserialize global credential: %w", err)
	}

	// Decrypt credential
	decryptedJSON, err := m.encryption.DecryptCredential(globalKey.EncryptedCredential)
	if err != nil {
		return nil, fmt.Errorf("failed to decrypt global credential: %w", err)
	}

	var credData map[string]string
	if err := json.Unmarshal([]byte(decryptedJSON), &credData); err != nil {
		return nil, fmt.Errorf("failed to unmarshal global credential: %w", err)
	}

	// Convert to Credential
	cred := &Credential{
		Provider:   globalKey.Provider,
		Data:       credData,
		Config:     globalKey.Config,
		RateLimit:  globalKey.RateLimit,
		Priority:   globalKey.Priority,
		IsFallback: globalKey.IsFallback,
		CreatedAt:  globalKey.CreatedAt,
		LastUsed:   globalKey.LastUsed,
		UsageCount: globalKey.UsageCount,
	}

	return cred, nil
}

// DeleteGlobalCredential removes a global credential
func (m *manager) DeleteGlobalCredential(ctx context.Context, provider string) error {
	if provider == "" {
		return ErrProviderRequired
	}

	key := models.GlobalProviderKeyStorageKey(provider)
	
	// Check if global credential exists first
	if _, err := m.store.Get(ctx, key); err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return ErrCredentialNotFound
		}
		return fmt.Errorf("failed to check global credential: %w", err)
	}
	
	if err := m.store.Delete(ctx, key); err != nil {
		return fmt.Errorf("failed to delete global credential: %w", err)
	}

	log.Info().
		Str("provider", provider).
		Msg("Global credential deleted")

	return nil
}

// ListGlobalCredentials lists all global credentials
func (m *manager) ListGlobalCredentials(ctx context.Context) ([]*Credential, error) {
	// Global credentials are stored with api_key_id = "global"
	prefix := storage.KeyPrefixCredential + "global:"
	keys, err := m.store.ScanWithPrefix(ctx, prefix, 1000)
	if err != nil {
		return nil, fmt.Errorf("failed to list global credentials: %w", err)
	}

	var credentials []*Credential
	for _, key := range keys {
		data, err := m.store.Get(ctx, key)
		if err != nil {
			log.Warn().Str("key", key).Err(err).Msg("Failed to get global credential")
			continue
		}

		var globalKey models.ProviderKey
		if err := storage.DeserializeModel(data, &globalKey); err != nil {
			log.Warn().Str("key", key).Err(err).Msg("Failed to deserialize global credential")
			continue
		}

		// Don't decrypt for listing
		cred := &Credential{
			Provider:   globalKey.Provider,
			Config:     globalKey.Config,
			RateLimit:  globalKey.RateLimit,
			Priority:   globalKey.Priority,
			IsFallback: globalKey.IsFallback,
			CreatedAt:  globalKey.CreatedAt,
			LastUsed:   globalKey.LastUsed,
			UsageCount: globalKey.UsageCount,
		}

		credentials = append(credentials, cred)
	}

	return credentials, nil
}

// DetermineKeyStrategy determines the fallback strategy for a given API key and provider
func (m *manager) DetermineKeyStrategy(ctx context.Context, apiKeyID string, provider string) FallbackStrategy {
	// Check if API key has BYOK credentials
	creds, err := m.GetCredentials(ctx, apiKeyID, provider)
	if err != nil || len(creds) == 0 {
		// No BYOK credentials, use gateway
		return GatewayFirst
	}

	// Check for BYOK-only preference in API key metadata
	// This would be implemented by checking the API key's configuration
	// For now, default to GatewayFirst
	return GatewayFirst
}

// CalculateBYOKCost calculates the 5% cost for BYOK usage
func (m *manager) CalculateBYOKCost(usage *Usage) float64 {
	// Get standard pricing for the provider/model
	standardCost := getStandardCost(usage)
	
	// BYOK pricing is 5% of standard rate
	return standardCost * 0.05
}

// RecordUsage records usage of a BYOK credential
func (m *manager) RecordUsage(ctx context.Context, apiKeyID string, provider string, _ *Usage) error {
	// Get the credential
	key := models.ProviderKeyStorageKey("user:"+apiKeyID, provider)
	data, err := m.store.Get(ctx, key)
	if err != nil {
		return fmt.Errorf("failed to get credential: %w", err)
	}

	// Update usage stats
	var providerKey models.ProviderKey
	if err := storage.DeserializeModel(data, &providerKey); err != nil {
		return fmt.Errorf("failed to deserialize credential: %w", err)
	}

	now := time.Now()
	providerKey.LastUsed = &now
	providerKey.UsageCount++
	providerKey.UpdatedAt = now

	// Store updated credential
	data, err = storage.SerializeModel(&providerKey)
	if err != nil {
		return fmt.Errorf("failed to serialize credential: %w", err)
	}

	if err := m.store.Set(ctx, key, data); err != nil {
		return fmt.Errorf("failed to update credential: %w", err)
	}

	return nil
}

// RotateEncryptionKey re-encrypts all credentials with a new master key
func (m *manager) RotateEncryptionKey(_ context.Context) error {
	// This would be implemented to:
	// 1. Generate new master key
	// 2. Re-encrypt all credentials
	// 3. Update master key
	// For now, return not implemented
	return ErrKeyRotationNotImplemented
}

// getStandardCost calculates the standard cost for usage
func getStandardCost(usage *Usage) float64 {
	// Simplified pricing calculation
	// In production, this would use actual provider pricing tables
	var cost float64
	
	switch usage.Provider {
	case "openai":
		// Example: GPT-4 pricing
		cost = float64(usage.PromptTokens) * 0.00003 + float64(usage.CompletionTokens) * 0.00006
	case "anthropic":
		// Example: Claude pricing
		cost = float64(usage.PromptTokens) * 0.00002 + float64(usage.CompletionTokens) * 0.00006
	case "google-aistudio", "google-vertexai":
		// Example: Gemini pricing
		cost = float64(usage.TotalTokens) * 0.00002
	default:
		// Default pricing
		cost = float64(usage.TotalTokens) * 0.00001
	}
	
	// Add image costs if applicable
	if usage.ImageCount > 0 {
		cost += float64(usage.ImageCount) * 0.02 // Example: $0.02 per image
	}
	
	// Add audio costs if applicable
	if usage.AudioSeconds > 0 {
		cost += usage.AudioSeconds * 0.006 // Example: $0.006 per second
	}
	
	return cost
}