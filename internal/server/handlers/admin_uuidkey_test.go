package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/agentstation/uuidkey"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentstation/starport/internal/storage"
)

func TestAdminHandler_CreateKey_UUIDKeyFormat(t *testing.T) {
	store := storage.NewMockStore()
	handler := NewAdminHandler(store)
	logger := zerolog.Nop()

	reqBody := map[string]interface{}{
		"name":        "Test API Key",
		"description": "Test key for uuidkey format verification",
		"scopes":      []string{"chat:write", "models:read"},
	}

	body, err := json.Marshal(reqBody)
	require.NoError(t, err)

	req := httptest.NewRequest("POST", "/api/v1/admin/keys", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	// Add logger to context (some handlers expect it)
	ctx := logger.WithContext(req.Context())
	req = req.WithContext(ctx)

	handler.CreateKey(rec, req)

	assert.Equal(t, http.StatusCreated, rec.Code)

	var response map[string]interface{}
	err = json.Unmarshal(rec.Body.Bytes(), &response)
	require.NoError(t, err)

	// Extract the key data
	keyData, ok := response["key"].(map[string]interface{})
	require.True(t, ok, "response should contain key object")

	// Get the actual API key value
	apiKeyStr, ok := keyData["key"].(string)
	require.True(t, ok, "key object should contain key string")

	// Verify the key format
	t.Logf("Generated key: %s", apiKeyStr)

	// The key should start with STARPORT_
	assert.True(t, strings.HasPrefix(apiKeyStr, "STARPORT_"), "key should have STARPORT prefix")

	// Parse the key to verify it's valid uuidkey format
	parts := strings.Split(apiKeyStr, "_")
	assert.Len(t, parts, 3, "key should have 3 parts: prefix, key, entropy")

	assert.Equal(t, "STARPORT", parts[0], "prefix should be STARPORT")
	assert.NotEmpty(t, parts[1], "key part should not be empty")
	assert.NotEmpty(t, parts[2], "entropy part should not be empty")

	// The key part should be a valid Base32-Crockford encoded string
	// It should be uppercase and only contain valid Crockford alphabet characters
	validChars := "0123456789ABCDEFGHJKMNPQRSTVWXYZ"
	for _, char := range parts[1] {
		assert.Contains(t, validChars, string(char), "key should only contain Base32-Crockford characters")
	}

	// Verify the key can be parsed by uuidkey
	apiKey, err := uuidkey.ParseAPIKey(apiKeyStr)
	assert.NoError(t, err, "should be able to parse the generated key")
	assert.Equal(t, "STARPORT", apiKey.Prefix)
	assert.NotEmpty(t, apiKey.Key)
	assert.NotEmpty(t, apiKey.Entropy)

	// Verify the key ID format
	keyID, ok := keyData["id"].(string)
	require.True(t, ok, "key object should contain id")
	// The ID should be STARPORT_ followed by the base32 key (without entropy)
	assert.True(t, strings.HasPrefix(keyID, "STARPORT_"), "key ID should start with STARPORT_")
	// The key ID should be shorter than the full key (no entropy part)
	assert.Less(t, len(keyID), len(apiKeyStr), "key ID should be shorter than full key")

	// Verify hash was generated (we can't check the mapping without knowing the internal hash)
	// But we can verify the key was stored
	storedKey, err := store.Get(ctx, storage.APIKeyKey(keyID))
	assert.NoError(t, err, "key should be stored")
	assert.NotEmpty(t, storedKey, "stored key data should not be empty")
}

func TestChatUIHandler_GenerateKey_UUIDKeyFormat(t *testing.T) {
	// This test would be similar but for the ChatUI handler
	// The ChatUI package would need to export its handler for testing
	// or we could add an integration test that tests the HTTP endpoint
}