package chatui

import (
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

func TestHandler_GenerateKey_UUIDKeyFormat(t *testing.T) {
	logger := zerolog.Nop()
	store := storage.NewMockStore()

	config := Config{
		Title:       "Test Chat",
		Theme:       "light",
		AllowKeyGen: true,
		APIBaseURL:  "http://localhost:8080",
		Store:       store,
	}

	handler, err := NewHandler(&logger, config)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/generate-key", nil)
	rec := httptest.NewRecorder()

	handler.GenerateKey(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	var response map[string]interface{}
	err = json.Unmarshal(rec.Body.Bytes(), &response)
	require.NoError(t, err)

	// Get the actual API key value
	apiKeyStr, ok := response["key"].(string)
	require.True(t, ok, "response should contain key string")

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
	assert.NotEmpty(t, apiKey.Checksum)

	// Verify the key ID format
	keyID, ok := response["key_id"].(string)
	require.True(t, ok, "response should contain key_id")
	assert.True(t, strings.HasPrefix(keyID, "STARPORT_"), "key ID should start with STARPORT_")
	assert.Less(t, len(keyID), len(apiKeyStr), "key ID should be shorter than full key")

	// Verify the key was stored
	ctx := req.Context()
	storedKey, err := store.Get(ctx, storage.APIKeyKey(keyID))
	assert.NoError(t, err, "key should be stored")
	assert.NotEmpty(t, storedKey, "stored key data should not be empty")

	// Verify scopes
	scopes, ok := response["scopes"].([]interface{})
	require.True(t, ok, "response should contain scopes")
	assert.Contains(t, scopes, "chat:write")
	assert.Contains(t, scopes, "models:read")
}
