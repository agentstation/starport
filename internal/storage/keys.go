package storage

// Key prefixes for different data types
const (
	// KeyPrefixResponse is the prefix for cached responses
	KeyPrefixResponse = "response:"

	// KeyPrefixModel is the prefix for model metadata
	KeyPrefixModel = "model:"
)

// Helper functions for key generation

// ResponseKey generates a storage key for cached responses
func ResponseKey(cacheKey string) string {
	return KeyPrefixResponse + cacheKey
}

// ModelKey generates a storage key for model metadata
func ModelKey(modelID string) string {
	return KeyPrefixModel + modelID
}
