package storage

// Key prefixes for different data types
const (
	// KeyPrefixAPIKey is the prefix for API key storage
	KeyPrefixAPIKey = "apikey:"
	
	// KeyPrefixRateLimit is the prefix for rate limit counters
	KeyPrefixRateLimit = "ratelimit:"
	
	// KeyPrefixResponse is the prefix for cached responses
	KeyPrefixResponse = "response:"
	
	// KeyPrefixPreset is the prefix for preset storage
	KeyPrefixPreset = "preset:"
	
	// KeyPrefixCredential is the prefix for BYOK credentials
	KeyPrefixCredential = "credential:" // #nosec G101 -- This is a key prefix, not a credential
	
	// KeyPrefixDefaultKey is the prefix for default provider keys
	KeyPrefixDefaultKey = "defaultkey:"
	
	// KeyPrefixModel is the prefix for model metadata
	KeyPrefixModel = "model:"
)

// Helper functions for key generation

// APIKeyKey generates a storage key for an API key
func APIKeyKey(hash string) string {
	return KeyPrefixAPIKey + hash
}

// RateLimitKey generates a storage key for rate limiting
func RateLimitKey(identifier string) string {
	return KeyPrefixRateLimit + identifier
}

// ResponseKey generates a storage key for cached responses
func ResponseKey(cacheKey string) string {
	return KeyPrefixResponse + cacheKey
}

// PresetKey generates a storage key for a preset
func PresetKey(name string) string {
	return KeyPrefixPreset + name
}

// CredentialKey generates a storage key for a BYOK credential
func CredentialKey(apiKeyID, provider string) string {
	return KeyPrefixCredential + apiKeyID + ":" + provider
}

// DefaultKeyKey generates a storage key for a default provider key
func DefaultKeyKey(provider string) string {
	return KeyPrefixDefaultKey + provider
}

// ModelKey generates a storage key for model metadata
func ModelKey(modelID string) string {
	return KeyPrefixModel + modelID
}