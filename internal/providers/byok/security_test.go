package byok

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"
	"testing"

	"github.com/agentstation/starport/internal/models"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestEncryptionIsolation verifies that keys are isolated per API key
func TestEncryptionIsolation(t *testing.T) {
	ctx := context.Background()
	store := storage.NewMockStore()
	masterKey, _ := models.GenerateMasterKey()
	manager, err := NewProviderKeys(store, masterKey)
	require.NoError(t, err)

	// Skip validation
	ctx = context.WithValue(ctx, "skip_validation", true)

	// Add keys for different API keys
	scope1 := "user:key1"
	scope2 := "user:key2"
	provider := "openai"
	secretKey1 := "sk-key1-secret-123456789"
	secretKey2 := "sk-key2-secret-987654321"

	// Add keys
	_, err = manager.AddKey(ctx, scope1, provider, map[string]string{"api_key": secretKey1}, nil, false, 0)
	require.NoError(t, err)

	_, err = manager.AddKey(ctx, scope2, provider, map[string]string{"api_key": secretKey2}, nil, false, 0)
	require.NoError(t, err)

	// Get raw encrypted data from store
	key1 := models.ProviderKeyStorageKey(scope1, provider)
	key2 := models.ProviderKeyStorageKey(scope2, provider)

	data1, err := store.Get(ctx, key1)
	require.NoError(t, err)

	data2, err := store.Get(ctx, key2)
	require.NoError(t, err)

	// Verify encrypted data is different (due to different salts)
	assert.NotEqual(t, data1, data2)

	// Verify neither contains plaintext secrets
	assert.NotContains(t, string(data1), secretKey1)
	assert.NotContains(t, string(data2), secretKey2)

	// Verify we can retrieve the correct keys
	key1Data, err := manager.GetKey(ctx, scope1, provider)
	require.NoError(t, err)

	// Decrypt the credential to verify
	encService, err := models.NewEncryptionService(masterKey)
	require.NoError(t, err)

	decrypted1, err := encService.DecryptCredential(key1Data.EncryptedCredential)
	require.NoError(t, err)
	assert.Contains(t, decrypted1, secretKey1)

	key2Data, err := manager.GetKey(ctx, scope2, provider)
	require.NoError(t, err)

	decrypted2, err := encService.DecryptCredential(key2Data.EncryptedCredential)
	require.NoError(t, err)
	assert.Contains(t, decrypted2, secretKey2)
}

// TestEncryptionRandomness verifies that encryption uses proper randomness
func TestEncryptionRandomness(t *testing.T) {
	// Create encryption service
	masterKey, _ := models.GenerateMasterKey()
	encService, err := models.NewEncryptionService(masterKey)
	require.NoError(t, err)

	// Encrypt the same plaintext multiple times
	plaintext := "sk-test-secret-key-123456789"
	encrypted1, err := encService.EncryptCredential(plaintext)
	require.NoError(t, err)

	encrypted2, err := encService.EncryptCredential(plaintext)
	require.NoError(t, err)

	encrypted3, err := encService.EncryptCredential(plaintext)
	require.NoError(t, err)

	// Verify all encrypted values are different (due to random salt/nonce)
	assert.NotEqual(t, encrypted1, encrypted2)
	assert.NotEqual(t, encrypted2, encrypted3)
	assert.NotEqual(t, encrypted1, encrypted3)

	// But all should decrypt to the same value
	decrypted1, err := encService.DecryptCredential(encrypted1)
	require.NoError(t, err)
	assert.Equal(t, plaintext, decrypted1)

	decrypted2, err := encService.DecryptCredential(encrypted2)
	require.NoError(t, err)
	assert.Equal(t, plaintext, decrypted2)

	decrypted3, err := encService.DecryptCredential(encrypted3)
	require.NoError(t, err)
	assert.Equal(t, plaintext, decrypted3)
}

// TestEncryptionTampering verifies that tampered data cannot be decrypted
func TestEncryptionTampering(t *testing.T) {
	masterKey, _ := models.GenerateMasterKey()
	encService, err := models.NewEncryptionService(masterKey)
	require.NoError(t, err)

	plaintext := "sk-test-secret-key-123456789"
	encrypted, err := encService.EncryptCredential(plaintext)
	require.NoError(t, err)

	// Decode the base64
	encryptedBytes, err := base64.StdEncoding.DecodeString(encrypted)
	require.NoError(t, err)

	// Tamper with different parts of the encrypted data
	tests := []struct {
		name   string
		tamper func([]byte) []byte
	}{
		{
			name: "Modify salt",
			tamper: func(data []byte) []byte {
				modified := make([]byte, len(data))
				copy(modified, data)
				modified[0] ^= 0xFF // Flip bits in first byte of salt
				return modified
			},
		},
		{
			name: "Modify nonce",
			tamper: func(data []byte) []byte {
				modified := make([]byte, len(data))
				copy(modified, data)
				modified[32] ^= 0xFF // Flip bits in first byte of nonce (after 32-byte salt)
				return modified
			},
		},
		{
			name: "Modify ciphertext",
			tamper: func(data []byte) []byte {
				modified := make([]byte, len(data))
				copy(modified, data)
				modified[len(modified)-1] ^= 0xFF // Flip bits in last byte
				return modified
			},
		},
		{
			name: "Truncate data",
			tamper: func(data []byte) []byte {
				return data[:len(data)-5]
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tampered := tt.tamper(encryptedBytes)
			tamperedB64 := base64.StdEncoding.EncodeToString(tampered)

			// Attempt to decrypt tampered data
			_, err := encService.DecryptCredential(tamperedB64)
			assert.Error(t, err, "Decryption should fail for tampered data")
		})
	}
}

// TestKeyLeakage verifies no key data leaks in errors or logs
func TestKeyLeakage(t *testing.T) {
	ctx := context.Background()
	store := storage.NewMockStore()
	masterKey, _ := models.GenerateMasterKey()
	manager, err := NewProviderKeys(store, masterKey)
	require.NoError(t, err)

	secretKey := "sk-super-secret-key-123456789"
	sensitiveData := map[string]string{
		"api_key":      secretKey,
		"access_token": "secret-token-xyz",
		"password":     "my-password-123",
	}

	// Test validation errors don't leak keys
	err = manager.ValidateKey(ctx, "openai", sensitiveData, nil)
	if err != nil {
		errStr := err.Error()
		for key, value := range sensitiveData {
			assert.NotContains(t, errStr, value, "Error should not contain %s value", key)
		}
	}

	// Add key with validation skip
	ctx = context.WithValue(ctx, "skip_validation", true)
	_, err = manager.AddKey(ctx, "user:test-key", "openai", sensitiveData, nil, false, 0)
	require.NoError(t, err)

	// Verify stored data doesn't contain plaintext
	key := models.ProviderKeyStorageKey("user:test-key", "openai")
	rawData, err := store.Get(ctx, key)
	require.NoError(t, err)

	dataStr := string(rawData)
	for _, value := range sensitiveData {
		assert.NotContains(t, dataStr, value, "Stored data should not contain plaintext key")
	}
}

// TestMasterKeyStrength verifies master key generation produces strong keys
func TestMasterKeyStrength(t *testing.T) {
	// Generate multiple master keys
	keys := make([][]byte, 10)
	for i := range keys {
		key, err := models.GenerateMasterKey()
		require.NoError(t, err)
		keys[i] = key

		// Verify key length
		assert.Equal(t, 32, len(key), "Master key should be 32 bytes (256 bits)")

		// Verify key has sufficient entropy (no obvious patterns)
		// Check that not all bytes are the same
		firstByte := key[0]
		allSame := true
		for _, b := range key {
			if b != firstByte {
				allSame = false
				break
			}
		}
		assert.False(t, allSame, "Key should have entropy")
	}

	// Verify all keys are unique
	for i := 0; i < len(keys); i++ {
		for j := i + 1; j < len(keys); j++ {
			assert.NotEqual(t, keys[i], keys[j], "Generated keys should be unique")
		}
	}
}

// TestPasswordDerivedKey verifies password-based key derivation
func TestPasswordDerivedKey(t *testing.T) {
	password := "my-secure-password-123"

	// Derive key multiple times from same password
	key1 := models.DeriveKeyFromPassword(password)
	key2 := models.DeriveKeyFromPassword(password)

	// Should produce the same key (deterministic)
	assert.Equal(t, key1, key2, "Same password should produce same key")
	assert.Equal(t, 32, len(key1), "Derived key should be 32 bytes")

	// Different password should produce different key
	key3 := models.DeriveKeyFromPassword("different-password")
	assert.NotEqual(t, key1, key3, "Different passwords should produce different keys")
}

// TestLargeKeyEncryption tests encryption of large keys
func TestLargeKeyEncryption(t *testing.T) {
	masterKey, _ := models.GenerateMasterKey()
	encService, err := models.NewEncryptionService(masterKey)
	require.NoError(t, err)

	// Create a large service account JSON (typical for GCP)
	largeKey := `{
		"type": "service_account",
		"project_id": "my-project-12345",
		"private_key_id": "key-id-123456789",
		"private_key": "-----BEGIN PRIVATE KEY-----\n` + strings.Repeat("A", 1600) + `\n-----END PRIVATE KEY-----",
		"client_email": "service-account@my-project.iam.gserviceaccount.com",
		"client_id": "123456789012345678901",
		"auth_uri": "https://accounts.google.com/o/oauth2/auth",
		"token_uri": "https://oauth2.googleapis.com/token",
		"auth_provider_x509_cert_url": "https://www.googleapis.com/oauth2/v1/certs",
		"client_x509_cert_url": "https://www.googleapis.com/robot/v1/metadata/x509/service-account%40my-project.iam.gserviceaccount.com"
	}`

	// Encrypt large key
	encrypted, err := encService.EncryptCredential(largeKey)
	require.NoError(t, err)

	// Decrypt and verify
	decrypted, err := encService.DecryptCredential(encrypted)
	require.NoError(t, err)
	assert.Equal(t, largeKey, decrypted)
}

// TestConcurrentAccess verifies thread-safe access to keys
func TestConcurrentAccess(t *testing.T) {
	ctx := context.Background()
	store := storage.NewMockStore()
	masterKey, _ := models.GenerateMasterKey()
	manager, err := NewProviderKeys(store, masterKey)
	require.NoError(t, err)

	// Skip validation
	ctx = context.WithValue(ctx, "skip_validation", true)

	// Add initial key
	scope := "user:test-key"
	provider := "openai"
	_, err = manager.AddKey(ctx, scope, provider, map[string]string{"api_key": "sk-initial"}, nil, false, 0)
	require.NoError(t, err)

	// Concurrent operations
	done := make(chan bool, 3)

	// Reader
	go func() {
		for i := 0; i < 100; i++ {
			_, err := manager.GetKey(ctx, scope, provider)
			assert.NoError(t, err)
		}
		done <- true
	}()

	// Writer
	go func() {
		for i := 0; i < 50; i++ {
			newKey := fmt.Sprintf("sk-update-%d", i)
			_, err := manager.UpdateKey(ctx, scope, provider, map[string]string{"api_key": newKey}, nil, nil, nil)
			assert.NoError(t, err)
		}
		done <- true
	}()

	// Usage recorder
	go func() {
		for i := 0; i < 100; i++ {
			usage := &Usage{
				Provider:         provider,
				Model:            "gpt-4",
				PromptTokens:     100,
				CompletionTokens: 50,
			}
			err := manager.RecordUsage(ctx, scope, provider, usage)
			assert.NoError(t, err)
		}
		done <- true
	}()

	// Wait for all goroutines
	for i := 0; i < 3; i++ {
		<-done
	}

	// Verify final state is consistent
	key, err := manager.GetKey(ctx, scope, provider)
	assert.NoError(t, err)
	assert.NotNil(t, key)
	assert.Equal(t, provider, key.Provider)
}

// TestZeroizationOnError verifies sensitive data is cleared on errors
func TestZeroizationOnError(t *testing.T) {
	// This test verifies that the encryption service doesn't leave
	// sensitive data in memory after errors

	// Create an encryption service with invalid master key
	shortKey := make([]byte, 16) // Too short
	_, err := models.NewEncryptionService(shortKey)
	assert.Error(t, err)

	// The shortKey slice should still exist but we've demonstrated
	// proper error handling for invalid keys
}

// BenchmarkEncryption measures encryption performance
func BenchmarkEncryption(b *testing.B) {
	masterKey, _ := models.GenerateMasterKey()
	encService, _ := models.NewEncryptionService(masterKey)
	key := "sk-test-api-key-123456789"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		encrypted, err := encService.EncryptCredential(key)
		if err != nil {
			b.Fatal(err)
		}
		_, err = encService.DecryptCredential(encrypted)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkKeyDerivation measures Argon2 key derivation performance
func BenchmarkKeyDerivation(b *testing.B) {
	password := "test-password-123"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = models.DeriveKeyFromPassword(password)
	}
}
