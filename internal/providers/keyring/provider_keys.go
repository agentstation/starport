package keyring

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/google/uuid"
	"github.com/rs/zerolog/log"

	"github.com/agentstation/starport/internal/credentials"
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

// AddKey adds a new provider key for an account scope
func (m *keyManager) AddKey(ctx context.Context, scope, provider string, key map[string]string, config map[string]any, isFallback bool, priority int) (*credentials.ProviderKey, error) {
	// Validate inputs
	if scope == "" {
		return nil, ErrScopeRequired
	}
	if scope == SharedScope {
		return nil, ErrScopeIsShared
	}
	if provider == "" {
		return nil, ErrProviderRequired
	}

	encryptedKey, err := m.encryptKey(ctx, provider, key, config)
	if err != nil {
		return nil, err
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

// GetKeys retrieves provider keys from one exact scope. A shared credential
// is managed through the explicit shared-credential methods and is never
// merged into an account lookup; credential order across scopes belongs to
// the router.
func (m *keyManager) GetKeys(ctx context.Context, scope, provider string) ([]*credentials.ProviderKey, error) {
	if scope == "" || provider == "" {
		return nil, ErrScopeAndProviderRequired
	}

	records, err := m.repository.ListScope(ctx, scope, providerCredentialScanLimit)
	if err != nil {
		return nil, fmt.Errorf("failed to list scoped keys: %w", err)
	}

	var keys []*credentials.ProviderKey
	for _, record := range records {
		providerKey := record.Key
		if providerKey.Provider != provider {
			continue
		}
		keys = append(keys, &providerKey)
	}

	// Sort by priority (lower number = higher priority)
	// Gateway keys typically have higher priority values (lower precedence)
	sort.Slice(keys, func(i, j int) bool {
		return keys[i].Priority < keys[j].Priority
	})

	return keys, nil
}

// ResolveStoredMaterial decrypts and validates one exact account record against
// the provider contract from the leased runtime generation.
func (m *keyManager) ResolveStoredMaterial(
	ctx context.Context,
	scope string,
	provider catalogs.Provider,
) (credentials.Material, error) {
	if scope == "" {
		return credentials.Material{}, ErrScopeRequired
	}
	if scope == SharedScope {
		return credentials.Material{}, ErrScopeIsShared
	}
	if provider.ID == "" {
		return credentials.Material{}, ErrProviderRequired
	}
	record, err := m.repository.Get(ctx, scope, string(provider.ID))
	if err != nil {
		if errors.Is(err, credentials.ErrNotFound) {
			return credentials.Material{}, ErrKeyNotFound
		}
		return credentials.Material{}, fmt.Errorf("read scoped provider credential: %w", err)
	}
	return m.decryptMaterial(provider, record.Key.EncryptedCredential, record.Key.Config,
		fmt.Sprintf("stored:%d", record.Revision))
}

// ResolveSharedMaterial decrypts the first shared credential the named
// account may spend: open, or granted to that account. An empty account is an
// anonymous caller, which only an open credential serves.
func (m *keyManager) ResolveSharedMaterial(
	ctx context.Context,
	accountID string,
	provider catalogs.Provider,
) (credentials.Material, error) {
	if provider.ID == "" {
		return credentials.Material{}, ErrProviderRequired
	}
	record, err := m.repository.Get(ctx, SharedScope, string(provider.ID))
	if err != nil {
		if errors.Is(err, credentials.ErrNotFound) {
			return credentials.Material{}, ErrKeyNotFound
		}
		return credentials.Material{}, fmt.Errorf("read shared provider credential: %w", err)
	}
	for _, credential := range record.Key.Shared {
		if !credential.Usable(accountID) {
			continue
		}
		return m.decryptMaterial(provider, credential.EncryptedCredential, credential.Config,
			fmt.Sprintf("stored:%d:%s", record.Revision, credential.ID))
	}
	return credentials.Material{}, ErrKeyNotFound
}

// decryptMaterial turns one encrypted credential value into request-bound
// material under the provider's catalog contract.
func (m *keyManager) decryptMaterial(
	provider catalogs.Provider,
	encryptedCredential string,
	config map[string]any,
	version string,
) (credentials.Material, error) {
	decrypted, err := m.encryption.DecryptCredential(encryptedCredential)
	if err != nil {
		return credentials.Material{}, fmt.Errorf("%w: decrypt stored provider credential", ErrDecryptionFailed)
	}
	var secretValues map[string]string
	if err := json.Unmarshal([]byte(decrypted), &secretValues); err != nil {
		return credentials.Material{}, fmt.Errorf("%w: decode stored provider credential", ErrDecryptionFailed)
	}
	material, err := buildCredentialMaterial(provider, secretValues, config)
	if err != nil {
		return credentials.Material{}, err
	}
	return credentials.NewMaterial(
		material.Profile(),
		materialValues(material),
		credentials.MaterialMetadata{Version: version},
	), nil
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

// UpdateKey updates an existing provider key at an account scope
func (m *keyManager) UpdateKey(ctx context.Context, scope, provider string, key map[string]string, config map[string]any, isFallback *bool, priority *int) (*credentials.ProviderKey, error) {
	if scope == SharedScope {
		return nil, ErrScopeIsShared
	}
	var encryptedKey string
	if len(key) > 0 {
		encrypted, err := m.encryptKey(ctx, provider, key, config)
		if err != nil {
			return nil, err
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

// DeleteKey removes a provider key at an account scope
func (m *keyManager) DeleteKey(ctx context.Context, scope, provider string) error {
	if scope == "" || provider == "" {
		return fmt.Errorf("%w and %w", ErrScopeRequired, ErrProviderRequired)
	}
	if scope == SharedScope {
		return ErrScopeIsShared
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

// AddSharedCredential appends one shared credential to the provider's list at
// SharedScope, creating the record when the provider holds none yet. Access
// defaults to open: a credential the operator applies without saying
// otherwise serves every account.
func (m *keyManager) AddSharedCredential(ctx context.Context, provider string, key map[string]string, config map[string]any, params SharedCredentialParams) (*credentials.SharedCredential, error) {
	if provider == "" {
		return nil, ErrProviderRequired
	}
	access, err := credentials.ParseAccess(string(params.Access))
	if err != nil {
		return nil, err
	}
	encryptedKey, err := m.encryptKey(ctx, provider, key, config)
	if err != nil {
		return nil, err
	}

	now := time.Now()
	credential := credentials.SharedCredential{
		ID:                  uuid.NewString(),
		Label:               params.Label,
		EncryptedCredential: encryptedKey,
		Config:              config,
		RateLimit:           params.RateLimit,
		Access:              access,
		Grants:              append([]string(nil), params.Grants...),
		CreatedAt:           now,
		UpdatedAt:           now,
	}
	if err := credential.Validate(); err != nil {
		return nil, fmt.Errorf("invalid shared credential: %w", err)
	}

	for attempt := 0; attempt < maxCredentialUpdateAttempts; attempt++ {
		record, err := m.repository.Get(ctx, SharedScope, provider)
		switch {
		case errors.Is(err, credentials.ErrNotFound):
			created := credentials.ProviderKey{
				Scope:     SharedScope,
				Provider:  provider,
				Shared:    []credentials.SharedCredential{credential},
				CreatedAt: now,
				UpdatedAt: now,
			}
			if _, err := m.repository.Create(ctx, created); err != nil {
				if errors.Is(err, credentials.ErrConflict) {
					// Another writer created the record first; append instead.
					continue
				}
				return nil, fmt.Errorf("failed to store shared credential: %w", err)
			}
		case err != nil:
			return nil, fmt.Errorf("read shared provider credential: %w", err)
		default:
			record.Key.Shared = append(record.Key.Shared, credential)
			record.Key.UpdatedAt = now
			if err := record.Key.Validate(); err != nil {
				return nil, fmt.Errorf("invalid shared credential: %w", err)
			}
			if _, err := m.repository.Update(ctx, record.Key, record.Revision); err != nil {
				if errors.Is(err, credentials.ErrConflict) {
					if waitErr := waitCredentialConflict(ctx, attempt); waitErr != nil {
						return nil, waitErr
					}
					continue
				}
				return nil, fmt.Errorf("failed to store shared credential: %w", err)
			}
		}

		log.Info().
			Str("provider", provider).
			Str("access", string(access)).
			Msg("Shared provider credential added")
		result := credentials.CloneSharedCredential(credential)
		return &result, nil
	}
	return nil, credentials.ErrConflict
}

// GetSharedCredentials lists the provider's shared credentials in stored
// order. A provider with no shared record has an empty list, not an error.
func (m *keyManager) GetSharedCredentials(ctx context.Context, provider string) ([]credentials.SharedCredential, error) {
	if provider == "" {
		return nil, ErrProviderRequired
	}
	record, err := m.repository.Get(ctx, SharedScope, provider)
	if err != nil {
		if errors.Is(err, credentials.ErrNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("read shared provider credential: %w", err)
	}
	return record.Key.Shared, nil
}

// UpdateSharedCredential mutates one shared credential by id.
func (m *keyManager) UpdateSharedCredential(ctx context.Context, provider, credentialID string, update SharedCredentialUpdate) (*credentials.SharedCredential, error) {
	if provider == "" {
		return nil, ErrProviderRequired
	}
	if credentialID == "" {
		return nil, ErrKeyNotFound
	}
	var encryptedKey string
	if len(update.Key) > 0 {
		encrypted, err := m.encryptKey(ctx, provider, update.Key, update.Config)
		if err != nil {
			return nil, err
		}
		encryptedKey = encrypted
	}
	if update.Access != nil {
		if _, err := credentials.ParseAccess(string(*update.Access)); err != nil {
			return nil, err
		}
	}

	updated, err := m.updateCredential(ctx, SharedScope, provider, func(providerKey *credentials.ProviderKey) error {
		index := indexOfSharedCredential(providerKey.Shared, credentialID)
		if index < 0 {
			return ErrKeyNotFound
		}
		credential := &providerKey.Shared[index]
		if encryptedKey != "" {
			credential.EncryptedCredential = encryptedKey
		}
		if update.Config != nil {
			credential.Config = update.Config
		}
		if update.Label != nil {
			credential.Label = *update.Label
		}
		if update.Access != nil {
			credential.Access = *update.Access
		}
		if update.Grants != nil {
			credential.Grants = append([]string(nil), (*update.Grants)...)
		}
		if update.RateLimit != nil {
			credential.RateLimit = update.RateLimit
		}
		now := time.Now()
		credential.UpdatedAt = now
		providerKey.UpdatedAt = now
		return providerKey.Validate()
	})
	if err != nil {
		if errors.Is(err, ErrKeyNotFound) {
			return nil, ErrKeyNotFound
		}
		return nil, fmt.Errorf("failed to store shared credential: %w", err)
	}

	log.Info().
		Str("provider", provider).
		Msg("Shared provider credential updated")

	index := indexOfSharedCredential(updated.Shared, credentialID)
	if index < 0 {
		return nil, ErrKeyNotFound
	}
	result := credentials.CloneSharedCredential(updated.Shared[index])
	return &result, nil
}

// DeleteSharedCredential removes one shared credential by id, and removes the
// provider's record when its last credential goes.
func (m *keyManager) DeleteSharedCredential(ctx context.Context, provider, credentialID string) error {
	if provider == "" {
		return ErrProviderRequired
	}
	if credentialID == "" {
		return ErrKeyNotFound
	}
	for attempt := 0; attempt < maxCredentialUpdateAttempts; attempt++ {
		record, err := m.repository.Get(ctx, SharedScope, provider)
		if err != nil {
			if errors.Is(err, credentials.ErrNotFound) {
				return ErrKeyNotFound
			}
			return fmt.Errorf("read shared provider credential: %w", err)
		}
		index := indexOfSharedCredential(record.Key.Shared, credentialID)
		if index < 0 {
			return ErrKeyNotFound
		}
		if len(record.Key.Shared) == 1 {
			err = m.repository.Delete(ctx, SharedScope, provider, record.Revision)
		} else {
			record.Key.Shared = append(record.Key.Shared[:index], record.Key.Shared[index+1:]...)
			record.Key.UpdatedAt = time.Now()
			_, err = m.repository.Update(ctx, record.Key, record.Revision)
		}
		if err != nil {
			if errors.Is(err, credentials.ErrConflict) {
				if waitErr := waitCredentialConflict(ctx, attempt); waitErr != nil {
					return waitErr
				}
				continue
			}
			return fmt.Errorf("failed to delete shared credential: %w", err)
		}

		log.Info().
			Str("provider", provider).
			Msg("Shared provider credential deleted")
		return nil
	}
	return credentials.ErrConflict
}

// ListShared lists every provider's shared record, sorted by provider.
func (m *keyManager) ListShared(ctx context.Context) ([]*credentials.ProviderKey, error) {
	records, err := m.repository.ListScope(ctx, SharedScope, providerCredentialScanLimit)
	if err != nil {
		return nil, fmt.Errorf("failed to list shared credentials: %w", err)
	}
	keys := providerKeysFromRecords(records)
	sort.Slice(keys, func(i, j int) bool { return keys[i].Provider < keys[j].Provider })

	return keys, nil
}

func indexOfSharedCredential(shared []credentials.SharedCredential, credentialID string) int {
	for index := range shared {
		if shared[index].ID == credentialID {
			return index
		}
	}
	return -1
}

// encryptKey validates one credential value against the provider's catalog
// contract, then serializes and encrypts it.
func (m *keyManager) encryptKey(ctx context.Context, provider string, key map[string]string, config map[string]any) (string, error) {
	if err := m.ValidateKey(ctx, provider, key, config); err != nil {
		return "", err
	}
	keyJSON, err := json.Marshal(key)
	if err != nil {
		return "", fmt.Errorf("failed to serialize key: %w", err)
	}
	encrypted, err := m.encryption.EncryptCredential(string(keyJSON))
	if err != nil {
		return "", fmt.Errorf("failed to encrypt key: %w", err)
	}
	return encrypted, nil
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
			if waitErr := waitCredentialConflict(ctx, attempt); waitErr != nil {
				return nil, waitErr
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

// waitCredentialConflict sleeps one conflict backoff or returns the context's
// cancellation, whichever comes first.
func waitCredentialConflict(ctx context.Context, attempt int) error {
	timer := time.NewTimer(credentialConflictBackoff(attempt))
	select {
	case <-ctx.Done():
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		return ctx.Err()
	case <-timer.C:
		return nil
	}
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
