package storage

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Serialize converts any value to bytes using JSON encoding
func Serialize(v any) ([]byte, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("failed to serialize value: %w", err)
	}
	return data, nil
}

// Deserialize converts bytes to the specified type using JSON decoding
func Deserialize(data []byte, v any) error {
	if err := json.Unmarshal(data, v); err != nil {
		return fmt.Errorf("failed to deserialize value: %w", err)
	}
	return nil
}

// SerializeString converts a string to bytes
func SerializeString(s string) []byte {
	return []byte(s)
}

// DeserializeString converts bytes to string
func DeserializeString(data []byte) string {
	return string(data)
}

// SerializeInt64 converts an int64 to bytes
func SerializeInt64(n int64) []byte {
	return []byte(fmt.Sprintf("%d", n))
}

// DeserializeInt64 converts bytes to int64
func DeserializeInt64(data []byte) (int64, error) {
	var n int64
	if err := json.Unmarshal(data, &n); err != nil {
		// Try parsing as string
		s := string(data)
		// Check for decimal point to reject float values
		if strings.Contains(s, ".") {
			return 0, fmt.Errorf("failed to deserialize int64: value contains decimal point")
		}
		_, err = fmt.Sscanf(s, "%d", &n)
		if err != nil {
			return 0, fmt.Errorf("failed to deserialize int64: %w", err)
		}
	}
	return n, nil
}

// SerializeModel is an alias for Serialize for clarity when working with models
func SerializeModel(v any) ([]byte, error) {
	return Serialize(v)
}

// DeserializeModel is an alias for Deserialize for clarity when working with models
func DeserializeModel(data []byte, v any) error {
	return Deserialize(data, v)
}
