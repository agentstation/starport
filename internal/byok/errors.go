package byok

import "errors"

// Validation errors
var (
	// ErrStoreRequired is returned when store is not provided
	ErrStoreRequired = errors.New("store is required")
	// ErrAPIKeyIDRequired is returned when API key ID is not provided
	ErrAPIKeyIDRequired = errors.New("api key ID is required")
	// ErrProviderRequired is returned when provider is not provided
	ErrProviderRequired = errors.New("provider is required")
	// ErrAPIKeyAndProviderRequired is returned when both are missing
	ErrAPIKeyAndProviderRequired = errors.New("api key ID and provider are required")
	// ErrCredentialsRequired is returned when credentials are not provided
	ErrCredentialsRequired = errors.New("credentials are required")
	// ErrInvalidProvider is returned when provider is not supported
	ErrInvalidProvider = errors.New("invalid provider")
	// ErrCredentialNotFound is returned when a credential is not found
	ErrCredentialNotFound = errors.New("credential not found")
	// ErrInvalidCredentialFormat is returned when credential format is invalid
	ErrInvalidCredentialFormat = errors.New("invalid credential format")
	// ErrEncryptionFailed is returned when encryption fails
	ErrEncryptionFailed = errors.New("encryption failed")
	// ErrDecryptionFailed is returned when decryption fails
	ErrDecryptionFailed = errors.New("decryption failed")
	// ErrNotImplemented is returned when a feature is not implemented
	ErrNotImplemented = errors.New("not implemented")
	// ErrKeyRotationNotImplemented is returned when key rotation is not implemented
	ErrKeyRotationNotImplemented = errors.New("key rotation not implemented")
)
