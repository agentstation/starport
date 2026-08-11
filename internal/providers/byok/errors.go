package byok

import "errors"

// Validation errors
var (
	// ErrRepositoryRequired is returned when the credential repository is missing.
	ErrRepositoryRequired = errors.New("credential repository is required")
	// ErrCredentialValidatorRequired is returned when catalog-driven inference
	// credential validation is unavailable.
	ErrCredentialValidatorRequired = errors.New("inference credential validator is required")
	// ErrScopeRequired is returned when scope is not provided
	ErrScopeRequired = errors.New("scope is required")
	// ErrProviderRequired is returned when provider is not provided
	ErrProviderRequired = errors.New("provider is required")
	// ErrScopeAndProviderRequired is returned when both are missing
	ErrScopeAndProviderRequired = errors.New("scope and provider are required")
	// ErrKeysRequired is returned when keys are not provided
	ErrKeysRequired = errors.New("keys are required")
	// ErrInvalidProvider is returned when provider is not supported
	ErrInvalidProvider = errors.New("invalid provider")
	// ErrKeyNotFound is returned when a key is not found
	ErrKeyNotFound = errors.New("key not found")
	// ErrInvalidKeyFormat is returned when key format is invalid
	ErrInvalidKeyFormat = errors.New("invalid key format")
	// ErrEncryptionFailed is returned when encryption fails
	ErrEncryptionFailed = errors.New("encryption failed")
	// ErrDecryptionFailed is returned when decryption fails
	ErrDecryptionFailed = errors.New("decryption failed")
	// ErrNotImplemented is returned when a feature is not implemented
	ErrNotImplemented = errors.New("not implemented")
	// ErrKeyRotationNotImplemented is returned when key rotation is not implemented
	ErrKeyRotationNotImplemented = errors.New("key rotation not implemented")
)
