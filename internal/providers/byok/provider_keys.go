package byok

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/agentstation/starport/internal/credentials"
	"github.com/rs/zerolog/log"
)

// keyManager implements the KeyManager interface
type keyManager struct {
	repository credentials.Repository
	encryption *credentials.EncryptionService
	validator  CredentialValidator
}

const providerCredentialScanLimit = 1000
const maxCredentialUpdateAttempts = 256

// NewProviderKeys creates a new provider key manager
func NewProviderKeys(
	repository credentials.Repository,
	masterKey []byte,
	validator CredentialValidator,
) (ProviderKeys, error) {
	if repository == nil {
		return nil, ErrRepositoryRequired
	}
	if validator == nil {
		return nil, ErrCredentialValidatorRequired
	}

	encryption, err := credentials.NewEncryptionService(masterKey)
	if err != nil {
		return nil, fmt.Errorf("failed to create encryption service: %w", err)
	}

	return &keyManager{
		repository: repository,
		encryption: encryption,
		validator:  validator,
	}, nil
}

// AddKey adds a new provider key for a scope
func (m *keyManager) AddKey(ctx context.Context, scope, provider string, key map[string]string, config map[string]any, isFallback bool, priority int) (*credentials.ProviderKey, error) {
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
	providerKey := &credentials.ProviderKey{
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

	created, err := m.repository.Create(ctx, *providerKey)
	if err != nil {
		return nil, fmt.Errorf("failed to store key: %w", err)
	}

	log.Info().
		Str("scope", scope).
		Str("provider", provider).
		Msg("Provider key added")

	return &created.Key, nil
}

// GetKey retrieves the highest priority provider key
func (m *keyManager) GetKey(ctx context.Context, scope, provider string) (*credentials.ProviderKey, error) {
	keys, err := m.GetKeys(ctx, scope, provider)
	if err != nil {
		return nil, err
	}
	if len(keys) == 0 {
		return nil, ErrKeyNotFound
	}
	return keys[0], nil
}

// GetKeys retrieves all provider keys for a provider sorted by priority
// This includes both user-specific keys and global keys
func (m *keyManager) GetKeys(ctx context.Context, scope, provider string) ([]*credentials.ProviderKey, error) {
	if scope == "" || provider == "" {
		return nil, ErrScopeAndProviderRequired
	}

	userRecords, err := m.repository.ListScope(ctx, scope, providerCredentialScanLimit)
	if err != nil {
		return nil, fmt.Errorf("failed to list user keys: %w", err)
	}
	globalRecords, err := m.repository.ListScope(ctx, "*", providerCredentialScanLimit)
	if err != nil {
		return nil, fmt.Errorf("failed to list global keys: %w", err)
	}

	var keys []*credentials.ProviderKey
	for _, record := range append(userRecords, globalRecords...) {
		providerKey := record.Key
		if providerKey.Provider != provider {
			continue
		}

		// Decrypt key
		decryptedJSON, err := m.encryption.DecryptCredential(providerKey.EncryptedCredential)
		if err != nil {
			log.Warn().Str("provider", providerKey.Provider).Err(err).Msg("Failed to decrypt key")
			continue
		}

		var keyData map[string]string
		if err := json.Unmarshal([]byte(decryptedJSON), &keyData); err != nil {
			log.Warn().Str("provider", providerKey.Provider).Err(err).Msg("Failed to unmarshal key")
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
func (m *keyManager) ListKeys(ctx context.Context, scope string) ([]*credentials.ProviderKey, error) {
	if scope == "" {
		return nil, ErrScopeRequired
	}

	records, err := m.repository.ListScope(ctx, scope, providerCredentialScanLimit)
	if err != nil {
		return nil, fmt.Errorf("failed to list keys: %w", err)
	}
	return providerKeysFromRecords(records), nil
}

// UpdateKey updates an existing provider key
func (m *keyManager) UpdateKey(ctx context.Context, scope, provider string, key map[string]string, config map[string]any, isFallback *bool, priority *int) (*credentials.ProviderKey, error) {
	var encryptedKey string
	if len(key) > 0 {
		if err := m.ValidateKey(ctx, provider, key, config); err != nil {
			return nil, err
		}

		// Serialize and encrypt new key
		keyJSON, err := json.Marshal(key)
		if err != nil {
			return nil, fmt.Errorf("failed to serialize key: %w", err)
		}

		encrypted, err := m.encryption.EncryptCredential(string(keyJSON))
		if err != nil {
			return nil, fmt.Errorf("failed to encrypt key: %w", err)
		}
		encryptedKey = encrypted
	}
	updated, err := m.updateCredential(ctx, scope, provider, func(providerKey *credentials.ProviderKey) error {
		if encryptedKey != "" {
			providerKey.EncryptedCredential = encryptedKey
		}
		if config != nil {
			providerKey.Config = config
		}
		if isFallback != nil {
			providerKey.IsFallback = *isFallback
		}
		if priority != nil {
			providerKey.Priority = *priority
		}
		providerKey.UpdatedAt = time.Now()
		return providerKey.Validate()
	})
	if err != nil {
		return nil, fmt.Errorf("failed to store key: %w", err)
	}

	log.Info().
		Str("scope", scope).
		Str("provider", provider).
		Msg("Provider key updated")

	return updated, nil
}

// DeleteKey removes a provider key
func (m *keyManager) DeleteKey(ctx context.Context, scope, provider string) error {
	if scope == "" || provider == "" {
		return fmt.Errorf("%w and %w", ErrScopeRequired, ErrProviderRequired)
	}

	if err := m.repository.Delete(ctx, scope, provider, 0); err != nil {
		if errors.Is(err, credentials.ErrNotFound) {
			return ErrKeyNotFound
		}
		return fmt.Errorf("failed to delete key: %w", err)
	}

	log.Info().
		Str("scope", scope).
		Str("provider", provider).
		Msg("Provider key deleted")

	return nil
}

// AddGlobalKey adds a gateway-wide key for a provider
func (m *keyManager) AddGlobalKey(ctx context.Context, provider string, key map[string]string, config map[string]any, rateLimit *credentials.RateLimitConfig) (*credentials.ProviderKey, error) {
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
	globalKey := &credentials.ProviderKey{
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

	created, err := m.repository.Create(ctx, *globalKey)
	if err != nil {
		return nil, fmt.Errorf("failed to store global key: %w", err)
	}

	log.Info().
		Str("provider", provider).
		Msg("Global provider key set")

	return &created.Key, nil
}

// GetGlobalKey retrieves the global key for a provider
func (m *keyManager) GetGlobalKey(ctx context.Context, provider string) (*credentials.ProviderKey, error) {
	if provider == "" {
		return nil, ErrProviderRequired
	}

	record, err := m.repository.Get(ctx, "*", provider)
	if err != nil {
		if errors.Is(err, credentials.ErrNotFound) {
			return nil, ErrKeyNotFound
		}
		return nil, fmt.Errorf("failed to get global key: %w", err)
	}

	return &record.Key, nil
}

// UpdateGlobalKey updates an existing global provider key
func (m *keyManager) UpdateGlobalKey(ctx context.Context, provider string, key map[string]string, config map[string]any, rateLimit *credentials.RateLimitConfig) (*credentials.ProviderKey, error) {
	var encryptedKey string
	if len(key) > 0 {
		if err := m.ValidateKey(ctx, provider, key, config); err != nil {
			return nil, err
		}

		// Serialize and encrypt new key
		keyJSON, err := json.Marshal(key)
		if err != nil {
			return nil, fmt.Errorf("failed to serialize key: %w", err)
		}

		encrypted, err := m.encryption.EncryptCredential(string(keyJSON))
		if err != nil {
			return nil, fmt.Errorf("failed to encrypt key: %w", err)
		}
		encryptedKey = encrypted
	}
	updated, err := m.updateCredential(ctx, "*", provider, func(globalKey *credentials.ProviderKey) error {
		if encryptedKey != "" {
			globalKey.EncryptedCredential = encryptedKey
		}
		if config != nil {
			globalKey.Config = config
		}
		if rateLimit != nil {
			globalKey.RateLimit = rateLimit
		}
		globalKey.UpdatedAt = time.Now()
		return globalKey.Validate()
	})
	if err != nil {
		return nil, fmt.Errorf("failed to store global key: %w", err)
	}

	log.Info().
		Str("provider", provider).
		Msg("Global provider key updated")

	return updated, nil
}

// DeleteGlobalKey removes a global key
func (m *keyManager) DeleteGlobalKey(ctx context.Context, provider string) error {
	if provider == "" {
		return ErrProviderRequired
	}

	if err := m.repository.Delete(ctx, "*", provider, 0); err != nil {
		if errors.Is(err, credentials.ErrNotFound) {
			return ErrKeyNotFound
		}
		return fmt.Errorf("failed to delete global key: %w", err)
	}

	log.Info().
		Str("provider", provider).
		Msg("Global provider key deleted")

	return nil
}

// ListGlobalKeys lists all global keys
func (m *keyManager) ListGlobalKeys(ctx context.Context) ([]*credentials.ProviderKey, error) {
	records, err := m.repository.ListScope(ctx, "*", providerCredentialScanLimit)
	if err != nil {
		return nil, fmt.Errorf("failed to list global keys: %w", err)
	}
	keys := providerKeysFromRecords(records)
	sort.Slice(keys, func(i, j int) bool { return keys[i].Provider < keys[j].Provider })

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

// RecordUsage records usage of a provider key
func (m *keyManager) RecordUsage(ctx context.Context, scope string, provider string, _ *Usage) error {
	_, err := m.updateCredential(ctx, scope, provider, func(providerKey *credentials.ProviderKey) error {
		now := time.Now()
		providerKey.LastUsed = &now
		providerKey.UsageCount++
		providerKey.UpdatedAt = now
		return providerKey.Validate()
	})
	if err != nil {
		return fmt.Errorf("failed to update key: %w", err)
	}
	return nil
}

func providerKeysFromRecords(records []credentials.Record) []*credentials.ProviderKey {
	keys := make([]*credentials.ProviderKey, len(records))
	for index := range records {
		key := records[index].Key
		keys[index] = &key
	}
	return keys
}

func (m *keyManager) updateCredential(
	ctx context.Context,
	scope string,
	provider string,
	mutate func(*credentials.ProviderKey) error,
) (*credentials.ProviderKey, error) {
	for attempt := 0; attempt < maxCredentialUpdateAttempts; attempt++ {
		record, err := m.repository.Get(ctx, scope, provider)
		if err != nil {
			if errors.Is(err, credentials.ErrNotFound) {
				return nil, ErrKeyNotFound
			}
			return nil, err
		}
		if err := mutate(&record.Key); err != nil {
			return nil, err
		}
		updated, err := m.repository.Update(ctx, record.Key, record.Revision)
		if errors.Is(err, credentials.ErrConflict) {
			backoff := credentialConflictBackoff(attempt)
			timer := time.NewTimer(backoff)
			select {
			case <-ctx.Done():
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				return nil, ctx.Err()
			case <-timer.C:
			}
			continue
		}
		if err != nil {
			return nil, err
		}
		return &updated.Key, nil
	}
	return nil, credentials.ErrConflict
}

func credentialConflictBackoff(attempt int) time.Duration {
	const maximum = 5 * time.Millisecond
	backoff := 50 * time.Microsecond
	for step := 0; step < attempt && backoff < maximum; step++ {
		backoff *= 2
	}
	if backoff > maximum {
		return maximum
	}
	return backoff
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
