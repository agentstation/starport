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

// keyManager implements the KeyManager interface
type keyManager struct {
	store      storage.KVStore
	encryption *models.EncryptionService
}

// NewProviderKeys creates a new provider key manager
func NewProviderKeys(store storage.KVStore, masterKey []byte) (ProviderKeys, error) {
	if store == nil {
		return nil, ErrStoreRequired
	}

	encryption, err := models.NewEncryptionService(masterKey)
	if err != nil {
		return nil, fmt.Errorf("failed to create encryption service: %w", err)
	}

	return &keyManager{
		store:      store,
		encryption: encryption,
	}, nil
}

// AddKey adds a new provider key for a scope
func (m *keyManager) AddKey(ctx context.Context, scope, provider string, key map[string]string, config map[string]any, isFallback bool, priority int) (*models.ProviderKey, error) {
	// Validate inputs
	if scope == "" {
		return nil, ErrScopeRequired
	}
	if provider == "" {
		return nil, ErrProviderRequired
	}

	// Validate key format for provider
	if err := m.ValidateKey(ctx, provider, key, config); err != nil {
		return nil, err
	}

	// Serialize key data
	keyJSON, err := json.Marshal(key)
	if err != nil {
		return nil, fmt.Errorf("failed to serialize key: %w", err)
	}

	// Encrypt key
	encryptedKey, err := m.encryption.EncryptCredential(string(keyJSON))
	if err != nil {
		return nil, fmt.Errorf("failed to encrypt key: %w", err)
	}

	// Create provider key model
	providerKey := &models.ProviderKey{
		Scope:               scope,
		Provider:            provider,
		EncryptedCredential: encryptedKey,
		Config:              config,
		IsFallback:          isFallback,
		Priority:            priority,
		CreatedAt:           time.Now(),
		UpdatedAt:           time.Now(),
		UsageCount:          0,
	}

	// Validate model
	if err := providerKey.Validate(); err != nil {
		return nil, fmt.Errorf("invalid key: %w", err)
	}

	// Store key
	storageKey := models.ProviderKeyStorageKey(scope, provider)
	data, err := storage.SerializeModel(providerKey)
	if err != nil {
		return nil, fmt.Errorf("failed to serialize key: %w", err)
	}

	if err := m.store.Set(ctx, storageKey, data); err != nil {
		return nil, fmt.Errorf("failed to store key: %w", err)
	}

	log.Info().
		Str("scope", scope).
		Str("provider", provider).
		Msg("Provider key added")

	return providerKey, nil
}

// GetKey retrieves the highest priority provider key
func (m *keyManager) GetKey(ctx context.Context, scope, provider string) (*models.ProviderKey, error) {
	keys, err := m.GetKeys(ctx, scope, provider)
	if err != nil {
		return nil, err
	}
	if len(keys) == 0 {
		return nil, storage.ErrNotFound
	}
	return keys[0], nil
}

// GetKeys retrieves all provider keys for a provider sorted by priority
// This includes both user-specific keys and global keys
func (m *keyManager) GetKeys(ctx context.Context, scope, provider string) ([]*models.ProviderKey, error) {
	if scope == "" || provider == "" {
		return nil, ErrScopeAndProviderRequired
	}

	var allStorageKeys []string

	// 1. Get user-specific keys
	// Use the storage key prefix from models
	userPrefix := fmt.Sprintf("%s:%s:", models.PrefixProviderKey, scope)
	userKeys, err := m.store.ScanWithPrefix(ctx, userPrefix, 1000)
	if err != nil {
		return nil, fmt.Errorf("failed to list user keys: %w", err)
	}
	allStorageKeys = append(allStorageKeys, userKeys...)

	// 2. Get global keys (scope = "*")
	globalKey := models.ProviderKeyStorageKey("*", provider)
	if _, err := m.store.Get(ctx, globalKey); err == nil {
		allStorageKeys = append(allStorageKeys, globalKey)
	}

	var keys []*models.ProviderKey
	for _, storageKey := range allStorageKeys {
		// Extract provider from key
		// Storage key format: provider_key:scope:provider
		parts := strings.Split(storageKey, ":")
		if len(parts) < 3 {
			continue
		}
		keyProvider := parts[len(parts)-1] // Provider is always the last part
		if keyProvider != provider {
			continue
		}

		// Get key data
		data, err := m.store.Get(ctx, storageKey)
		if err != nil {
			log.Warn().Str("key", storageKey).Err(err).Msg("Failed to get key")
			continue
		}

		// Deserialize key
		var providerKey models.ProviderKey
		if err := storage.DeserializeModel(data, &providerKey); err != nil {
			log.Warn().Str("key", storageKey).Err(err).Msg("Failed to deserialize key")
			continue
		}

		// Decrypt key
		decryptedJSON, err := m.encryption.DecryptCredential(providerKey.EncryptedCredential)
		if err != nil {
			log.Warn().Str("key", storageKey).Err(err).Msg("Failed to decrypt key")
			continue
		}

		var keyData map[string]string
		if err := json.Unmarshal([]byte(decryptedJSON), &keyData); err != nil {
			log.Warn().Str("key", storageKey).Err(err).Msg("Failed to unmarshal key")
			continue
		}

		// For now, just append the provider key
		keys = append(keys, &providerKey)
	}

	// Sort by priority (lower number = higher priority)
	// Global keys typically have higher priority values (lower precedence)
	sort.Slice(keys, func(i, j int) bool {
		return keys[i].Priority < keys[j].Priority
	})

	return keys, nil
}

// ListKeys lists all provider keys for a scope
func (m *keyManager) ListKeys(ctx context.Context, scope string) ([]*models.ProviderKey, error) {
	if scope == "" {
		return nil, ErrScopeRequired
	}

	// List all keys for this scope
	// Use the storage key prefix from models
	prefix := fmt.Sprintf("%s:%s:", models.PrefixProviderKey, scope)
	storageKeys, err := m.store.ScanWithPrefix(ctx, prefix, 1000)
	if err != nil {
		return nil, fmt.Errorf("failed to list keys: %w", err)
	}

	var keys []*models.ProviderKey
	for _, storageKey := range storageKeys {
		// Get key data
		data, err := m.store.Get(ctx, storageKey)
		if err != nil {
			log.Warn().Str("key", storageKey).Err(err).Msg("Failed to get key")
			continue
		}

		// Deserialize key
		var providerKey models.ProviderKey
		if err := storage.DeserializeModel(data, &providerKey); err != nil {
			log.Warn().Str("key", storageKey).Err(err).Msg("Failed to deserialize key")
			continue
		}

		// Don't decrypt for listing - just return the key
		keys = append(keys, &providerKey)
	}

	return keys, nil
}

// UpdateKey updates an existing provider key
func (m *keyManager) UpdateKey(ctx context.Context, scope, provider string, key map[string]string, config map[string]any, isFallback *bool, priority *int) (*models.ProviderKey, error) {
	// Get existing key
	storageKey := models.ProviderKeyStorageKey(scope, provider)
	data, err := m.store.Get(ctx, storageKey)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return nil, ErrKeyNotFound
		}
		return nil, fmt.Errorf("failed to get key: %w", err)
	}

	// Deserialize existing
	var providerKey models.ProviderKey
	if err := storage.DeserializeModel(data, &providerKey); err != nil {
		return nil, fmt.Errorf("failed to deserialize key: %w", err)
	}

	// Validate new key if provided
	if len(key) > 0 {
		if err := m.ValidateKey(ctx, provider, key, config); err != nil {
			return nil, err
		}

		// Serialize and encrypt new key
		keyJSON, err := json.Marshal(key)
		if err != nil {
			return nil, fmt.Errorf("failed to serialize key: %w", err)
		}

		encryptedKey, err := m.encryption.EncryptCredential(string(keyJSON))
		if err != nil {
			return nil, fmt.Errorf("failed to encrypt key: %w", err)
		}

		providerKey.EncryptedCredential = encryptedKey
	}

	// Update config if provided
	if config != nil {
		providerKey.Config = config
	}

	// Update isFallback if provided
	if isFallback != nil {
		providerKey.IsFallback = *isFallback
	}

	// Update priority if provided
	if priority != nil {
		providerKey.Priority = *priority
	}

	// Update timestamp
	providerKey.UpdatedAt = time.Now()

	// Validate and store
	if err := providerKey.Validate(); err != nil {
		return nil, fmt.Errorf("invalid key: %w", err)
	}

	data, err = storage.SerializeModel(&providerKey)
	if err != nil {
		return nil, fmt.Errorf("failed to serialize key: %w", err)
	}

	if err := m.store.Set(ctx, storageKey, data); err != nil {
		return nil, fmt.Errorf("failed to store key: %w", err)
	}

	log.Info().
		Str("scope", scope).
		Str("provider", provider).
		Msg("Provider key updated")

	return &providerKey, nil
}

// DeleteKey removes a provider key
func (m *keyManager) DeleteKey(ctx context.Context, scope, provider string) error {
	if scope == "" || provider == "" {
		return fmt.Errorf("%w and %w", ErrScopeRequired, ErrProviderRequired)
	}

	storageKey := models.ProviderKeyStorageKey(scope, provider)

	// Check if key exists first
	if _, err := m.store.Get(ctx, storageKey); err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return ErrKeyNotFound
		}
		return fmt.Errorf("failed to check key: %w", err)
	}

	if err := m.store.Delete(ctx, storageKey); err != nil {
		return fmt.Errorf("failed to delete key: %w", err)
	}

	log.Info().
		Str("scope", scope).
		Str("provider", provider).
		Msg("Provider key deleted")

	return nil
}

// AddGlobalKey adds a gateway-wide key for a provider
func (m *keyManager) AddGlobalKey(ctx context.Context, provider string, key map[string]string, config map[string]any, rateLimit *models.RateLimitConfig) (*models.ProviderKey, error) {
	if provider == "" {
		return nil, ErrProviderRequired
	}

	// Validate key
	if err := m.ValidateKey(ctx, provider, key, config); err != nil {
		return nil, err
	}

	// Serialize and encrypt key
	keyJSON, err := json.Marshal(key)
	if err != nil {
		return nil, fmt.Errorf("failed to serialize key: %w", err)
	}

	encryptedKey, err := m.encryption.EncryptCredential(string(keyJSON))
	if err != nil {
		return nil, fmt.Errorf("failed to encrypt key: %w", err)
	}

	// Create global key as a ProviderKey with scope = "*"
	globalKey := &models.ProviderKey{
		Scope:               "*",
		Provider:            provider,
		EncryptedCredential: encryptedKey,
		Config:              config,
		RateLimit:           rateLimit,
		Priority:            100, // Lower priority than user keys
		CreatedAt:           time.Now(),
		UpdatedAt:           time.Now(),
	}

	// Validate model
	if err := globalKey.Validate(); err != nil {
		return nil, fmt.Errorf("invalid global key: %w", err)
	}

	// Store global key
	storageKey := models.GlobalProviderKeyStorageKey(provider)
	data, err := storage.SerializeModel(globalKey)
	if err != nil {
		return nil, fmt.Errorf("failed to serialize global key: %w", err)
	}

	if err := m.store.Set(ctx, storageKey, data); err != nil {
		return nil, fmt.Errorf("failed to store global key: %w", err)
	}

	log.Info().
		Str("provider", provider).
		Msg("Global provider key set")

	return globalKey, nil
}

// GetGlobalKey retrieves the global key for a provider
func (m *keyManager) GetGlobalKey(ctx context.Context, provider string) (*models.ProviderKey, error) {
	if provider == "" {
		return nil, ErrProviderRequired
	}

	storageKey := models.GlobalProviderKeyStorageKey(provider)
	data, err := m.store.Get(ctx, storageKey)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return nil, storage.ErrNotFound
		}
		return nil, fmt.Errorf("failed to get global key: %w", err)
	}

	// Deserialize ProviderKey
	var globalKey models.ProviderKey
	if err := storage.DeserializeModel(data, &globalKey); err != nil {
		return nil, fmt.Errorf("failed to deserialize global key: %w", err)
	}

	// For now, return the globalKey - we can decrypt on demand
	return &globalKey, nil
}

// UpdateGlobalKey updates an existing global provider key
func (m *keyManager) UpdateGlobalKey(ctx context.Context, provider string, key map[string]string, config map[string]any, rateLimit *models.RateLimitConfig) (*models.ProviderKey, error) {
	// Get existing key
	storageKey := models.GlobalProviderKeyStorageKey(provider)
	data, err := m.store.Get(ctx, storageKey)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return nil, ErrKeyNotFound
		}
		return nil, fmt.Errorf("failed to get global key: %w", err)
	}

	// Deserialize existing
	var globalKey models.ProviderKey
	if err := storage.DeserializeModel(data, &globalKey); err != nil {
		return nil, fmt.Errorf("failed to deserialize global key: %w", err)
	}

	// Validate new key if provided
	if len(key) > 0 {
		if err := m.ValidateKey(ctx, provider, key, config); err != nil {
			return nil, err
		}

		// Serialize and encrypt new key
		keyJSON, err := json.Marshal(key)
		if err != nil {
			return nil, fmt.Errorf("failed to serialize key: %w", err)
		}

		encryptedKey, err := m.encryption.EncryptCredential(string(keyJSON))
		if err != nil {
			return nil, fmt.Errorf("failed to encrypt key: %w", err)
		}

		globalKey.EncryptedCredential = encryptedKey
	}

	// Update config if provided
	if config != nil {
		globalKey.Config = config
	}

	// Update rate limit if provided
	if rateLimit != nil {
		globalKey.RateLimit = rateLimit
	}

	// Update timestamp
	globalKey.UpdatedAt = time.Now()

	// Validate and store
	if err := globalKey.Validate(); err != nil {
		return nil, fmt.Errorf("invalid global key: %w", err)
	}

	data, err = storage.SerializeModel(&globalKey)
	if err != nil {
		return nil, fmt.Errorf("failed to serialize global key: %w", err)
	}

	if err := m.store.Set(ctx, storageKey, data); err != nil {
		return nil, fmt.Errorf("failed to store global key: %w", err)
	}

	log.Info().
		Str("provider", provider).
		Msg("Global provider key updated")

	return &globalKey, nil
}

// DeleteGlobalKey removes a global key
func (m *keyManager) DeleteGlobalKey(ctx context.Context, provider string) error {
	if provider == "" {
		return ErrProviderRequired
	}

	storageKey := models.GlobalProviderKeyStorageKey(provider)

	// Check if global key exists first
	if _, err := m.store.Get(ctx, storageKey); err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return ErrKeyNotFound
		}
		return fmt.Errorf("failed to check global key: %w", err)
	}

	if err := m.store.Delete(ctx, storageKey); err != nil {
		return fmt.Errorf("failed to delete global key: %w", err)
	}

	log.Info().
		Str("provider", provider).
		Msg("Global provider key deleted")

	return nil
}

// ListGlobalKeys lists all global keys
func (m *keyManager) ListGlobalKeys(ctx context.Context) ([]*models.ProviderKey, error) {
	// Global keys are stored with scope = "*"
	prefix := storage.KeyPrefixProviderKey + "*:"
	storageKeys, err := m.store.ScanWithPrefix(ctx, prefix, 1000)
	if err != nil {
		return nil, fmt.Errorf("failed to list global keys: %w", err)
	}

	var keys []*models.ProviderKey
	for _, storageKey := range storageKeys {
		data, err := m.store.Get(ctx, storageKey)
		if err != nil {
			log.Warn().Str("key", storageKey).Err(err).Msg("Failed to get global key")
			continue
		}

		var globalKey models.ProviderKey
		if err := storage.DeserializeModel(data, &globalKey); err != nil {
			log.Warn().Str("key", storageKey).Err(err).Msg("Failed to deserialize global key")
			continue
		}

		// Don't decrypt for listing
		keys = append(keys, &globalKey)
	}

	return keys, nil
}

// DetermineKeyStrategy determines the fallback strategy for a given scope and provider
func (m *keyManager) DetermineKeyStrategy(ctx context.Context, scope string, provider string) FallbackStrategy {
	// Check if scope has provider keys
	keys, err := m.GetKeys(ctx, scope, provider)
	if err != nil || len(keys) == 0 {
		// No user keys, use gateway
		return GatewayFirst
	}

	// Check for user-only preference in scope metadata
	// This would be implemented by checking the scope's configuration
	// For now, default to GatewayFirst
	return GatewayFirst
}

// CalculateProviderKeyCost calculates the 5% cost for user-provided keys
func (m *keyManager) CalculateProviderKeyCost(usage *Usage) float64 {
	// Get standard pricing for the provider/model
	standardCost := getStandardCost(usage)

	// User key pricing is 5% of standard rate
	return standardCost * 0.05
}

// RecordUsage records usage of a provider key
func (m *keyManager) RecordUsage(ctx context.Context, scope string, provider string, _ *Usage) error {
	// Get the key
	storageKey := models.ProviderKeyStorageKey(scope, provider)
	data, err := m.store.Get(ctx, storageKey)
	if err != nil {
		return fmt.Errorf("failed to get key: %w", err)
	}

	// Update usage stats
	var providerKey models.ProviderKey
	if err := storage.DeserializeModel(data, &providerKey); err != nil {
		return fmt.Errorf("failed to deserialize key: %w", err)
	}

	now := time.Now()
	providerKey.LastUsed = &now
	providerKey.UsageCount++
	providerKey.UpdatedAt = now

	// Store updated key
	data, err = storage.SerializeModel(&providerKey)
	if err != nil {
		return fmt.Errorf("failed to serialize key: %w", err)
	}

	if err := m.store.Set(ctx, storageKey, data); err != nil {
		return fmt.Errorf("failed to update key: %w", err)
	}

	return nil
}

// RotateEncryptionKey re-encrypts all keys with a new master key
func (m *keyManager) RotateEncryptionKey(_ context.Context) error {
	// This would be implemented to:
	// 1. Generate new master key
	// 2. Re-encrypt all keys
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
		cost = float64(usage.PromptTokens)*0.00003 + float64(usage.CompletionTokens)*0.00006
	case "anthropic":
		// Example: Claude pricing
		cost = float64(usage.PromptTokens)*0.00002 + float64(usage.CompletionTokens)*0.00006
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
