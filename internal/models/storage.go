package models

import (
	"fmt"
	"strings"
)

// Storage key prefixes
const (
	PrefixAPIKey      = "apikey"
	PrefixPreset      = "preset"
	PrefixCredential  = "credential"
	PrefixDefaultKey  = "default_key"
	PrefixRateLimit   = "ratelimit"
	PrefixFilter      = "filter"
)

// APIKeyStorageKey generates the storage key for an API key
func APIKeyStorageKey(hash string) string {
	return fmt.Sprintf("%s:%s", PrefixAPIKey, hash)
}

// PresetStorageKey generates the storage key for a preset
func PresetStorageKey(name string) string {
	return fmt.Sprintf("%s:%s", PrefixPreset, name)
}

// BYOKCredentialStorageKey generates the storage key for a BYOK credential
func BYOKCredentialStorageKey(apiKeyID, provider string) string {
	return fmt.Sprintf("%s:%s:%s", PrefixCredential, apiKeyID, provider)
}

// RateLimitStorageKey generates the storage key for rate limit state
func RateLimitStorageKey(key, window string) string {
	return fmt.Sprintf("%s:%s:%s", PrefixRateLimit, key, window)
}

// FilterStorageKey generates the storage key for a filter rule
func FilterStorageKey(name string) string {
	return fmt.Sprintf("%s:%s", PrefixFilter, name)
}

// ParseStorageKey parses a storage key and returns its components
func ParseStorageKey(key string) (prefix string, parts []string) {
	components := strings.Split(key, ":")
	if len(components) == 0 {
		return "", nil
	}
	return components[0], components[1:]
}

// IsAPIKeyStorageKey checks if a key is an API key storage key
func IsAPIKeyStorageKey(key string) bool {
	return strings.HasPrefix(key, PrefixAPIKey+":")
}

// IsPresetStorageKey checks if a key is a preset storage key
func IsPresetStorageKey(key string) bool {
	return strings.HasPrefix(key, PrefixPreset+":")
}

// IsBYOKCredentialStorageKey checks if a key is a BYOK credential storage key
func IsBYOKCredentialStorageKey(key string) bool {
	return strings.HasPrefix(key, PrefixCredential+":")
}

// IsRateLimitStorageKey checks if a key is a rate limit storage key
func IsRateLimitStorageKey(key string) bool {
	return strings.HasPrefix(key, PrefixRateLimit+":")
}

// ExtractAPIKeyHash extracts the API key hash from a storage key
func ExtractAPIKeyHash(key string) (string, error) {
	if !IsAPIKeyStorageKey(key) {
		return "", fmt.Errorf("not an API key storage key: %s", key)
	}
	parts := strings.SplitN(key, ":", 2)
	if len(parts) != 2 {
		return "", fmt.Errorf("invalid API key storage key format: %s", key)
	}
	return parts[1], nil
}

// ExtractPresetName extracts the preset name from a storage key
func ExtractPresetName(key string) (string, error) {
	if !IsPresetStorageKey(key) {
		return "", fmt.Errorf("not a preset storage key: %s", key)
	}
	parts := strings.SplitN(key, ":", 2)
	if len(parts) != 2 {
		return "", fmt.Errorf("invalid preset storage key format: %s", key)
	}
	return parts[1], nil
}

// ExtractBYOKCredentialParts extracts the API key ID and provider from a storage key
func ExtractBYOKCredentialParts(key string) (apiKeyID, provider string, err error) {
	if !IsBYOKCredentialStorageKey(key) {
		return "", "", fmt.Errorf("not a BYOK credential storage key: %s", key)
	}
	parts := strings.Split(key, ":")
	if len(parts) != 3 {
		return "", "", fmt.Errorf("invalid BYOK credential storage key format: %s", key)
	}
	return parts[1], parts[2], nil
}

// DefaultKeyStorageKey generates the storage key for a default provider key
func DefaultKeyStorageKey(provider string) string {
	return fmt.Sprintf("%s:%s", PrefixDefaultKey, provider)
}

// IsDefaultKeyStorageKey checks if a key is a default key storage key
func IsDefaultKeyStorageKey(key string) bool {
	return strings.HasPrefix(key, PrefixDefaultKey+":")
}