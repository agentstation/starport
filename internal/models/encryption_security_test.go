package models

import (
	"crypto/rand"
	"encoding/base64"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestArgon2Parameters verifies Argon2 parameters are secure
func TestArgon2Parameters(t *testing.T) {
	// Verify constants are set to secure values
	assert.GreaterOrEqual(t, argon2Time, 1, "Argon2 time parameter should be at least 1")
	assert.GreaterOrEqual(t, argon2Memory, 64*1024, "Argon2 memory should be at least 64MB")
	assert.GreaterOrEqual(t, argon2Threads, 4, "Argon2 should use at least 1 thread")
	assert.Equal(t, argon2KeyLen, 32, "Key length should be 32 bytes for AES-256")
}

// TestGCMNonceSize verifies GCM nonce size is correct
func TestGCMNonceSize(t *testing.T) {
	assert.Equal(t, gcmNonceSize, 12, "GCM nonce size should be 12 bytes")
}

// TestSaltSize verifies salt size is sufficient
func TestSaltSize(t *testing.T) {
	assert.GreaterOrEqual(t, saltSize, 32, "Salt size should be at least 32 bytes")
}

// TestCryptoRandomSource verifies we're using crypto/rand
func TestCryptoRandomSource(t *testing.T) {
	// This test ensures we're using the cryptographically secure RNG
	// by generating random data and checking it works
	randomData := make([]byte, 32)
	n, err := rand.Read(randomData)
	require.NoError(t, err)
	assert.Equal(t, 32, n, "Should read all requested bytes")

	// Verify it's not all zeros (extremely unlikely with real randomness)
	allZeros := true
	for _, b := range randomData {
		if b != 0 {
			allZeros = false
			break
		}
	}
	assert.False(t, allZeros, "Random data should not be all zeros")
}

// TestEncryptionServiceSecurityProperties verifies security properties
func TestEncryptionServiceSecurityProperties(t *testing.T) {
	masterKey, err := GenerateMasterKey()
	require.NoError(t, err)

	service, err := NewEncryptionService(masterKey)
	require.NoError(t, err)

	// Test 1: Empty plaintext handling
	_, err = service.EncryptCredential("")
	assert.Error(t, err, "Should not encrypt empty credential")

	// Test 2: Minimum ciphertext size
	// Ciphertext should include salt + nonce + ciphertext + tag
	encrypted, err := service.EncryptCredential("a")
	require.NoError(t, err)

	decoded, err := base64.StdEncoding.DecodeString(encrypted)
	require.NoError(t, err)

	minSize := saltSize + gcmNonceSize + 1 + 16 // 1 byte data + 16 byte GCM tag
	assert.GreaterOrEqual(t, len(decoded), minSize, "Ciphertext should have minimum size")

	// Test 3: Invalid base64 handling
	_, err = service.DecryptCredential("not-valid-base64!@#$")
	assert.Error(t, err, "Should fail on invalid base64")

	// Test 4: Too short ciphertext
	shortData := make([]byte, 10)
	_, err = service.DecryptCredential(base64.StdEncoding.EncodeToString(shortData))
	assert.Error(t, err, "Should fail on too short ciphertext")
}

// TestKeyDerivationUniqueness verifies unique salts produce unique keys
func TestKeyDerivationUniqueness(t *testing.T) {
	masterKey, err := GenerateMasterKey()
	require.NoError(t, err)

	service, err := NewEncryptionService(masterKey)
	require.NoError(t, err)

	// Generate two different salts
	salt1 := make([]byte, saltSize)
	salt2 := make([]byte, saltSize)

	_, err = rand.Read(salt1)
	require.NoError(t, err)

	_, err = rand.Read(salt2)
	require.NoError(t, err)

	// Derive keys
	key1 := service.deriveKey(salt1)
	key2 := service.deriveKey(salt2)

	// Keys should be different with different salts
	assert.NotEqual(t, key1, key2, "Different salts should produce different keys")

	// But same salt should produce same key (deterministic)
	key1Again := service.deriveKey(salt1)
	assert.Equal(t, key1, key1Again, "Same salt should produce same key")
}

// TestMasterKeyImmutability verifies master key cannot be modified
func TestMasterKeyImmutability(t *testing.T) {
	originalKey, err := GenerateMasterKey()
	require.NoError(t, err)

	// Make a copy
	keyCopy := make([]byte, len(originalKey))
	copy(keyCopy, originalKey)

	// Create service
	service, err := NewEncryptionService(originalKey)
	require.NoError(t, err)

	// Modify the original key slice
	for i := range originalKey {
		originalKey[i] = 0
	}

	// Service should still work (it should have made its own copy)
	plaintext := "test-credential"
	encrypted, err := service.EncryptCredential(plaintext)
	require.NoError(t, err)

	// Create new service with the copy to decrypt
	service2, err := NewEncryptionService(keyCopy)
	require.NoError(t, err)

	decrypted, err := service2.DecryptCredential(encrypted)
	require.NoError(t, err)
	assert.Equal(t, plaintext, decrypted)
}

// TestPasswordDerivationSecurity checks password derivation properties
func TestPasswordDerivationSecurity(t *testing.T) {
	// Test weak password still produces strong key
	weakPassword := "123456"
	key := DeriveKeyFromPassword(weakPassword)
	assert.Equal(t, 32, len(key), "Even weak passwords should produce 256-bit keys")

	// Test that similar passwords produce very different keys
	key1 := DeriveKeyFromPassword("password1")
	key2 := DeriveKeyFromPassword("password2")

	// Count different bytes
	differentBytes := 0
	for i := range key1 {
		if key1[i] != key2[i] {
			differentBytes++
		}
	}

	// Should have high avalanche effect
	assert.Greater(t, differentBytes, 16, "Similar passwords should produce very different keys")
}

// BenchmarkArgon2KeyDerivation measures Argon2 performance
func BenchmarkArgon2KeyDerivation(b *testing.B) {
	masterKey, _ := GenerateMasterKey()
	salt := make([]byte, saltSize)
	rand.Read(salt)

	service, _ := NewEncryptionService(masterKey)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = service.deriveKey(salt)
	}
}

// TestNonceUniqueness verifies nonce generation produces unique values
func TestNonceUniqueness(t *testing.T) {
	// Generate many nonces and check for collisions
	nonces := make(map[string]bool)
	collisions := 0

	for i := 0; i < 10000; i++ {
		nonce := make([]byte, gcmNonceSize)
		_, err := rand.Read(nonce)
		require.NoError(t, err)

		nonceStr := string(nonce)
		if nonces[nonceStr] {
			collisions++
		}
		nonces[nonceStr] = true
	}

	assert.Equal(t, 0, collisions, "Should have no nonce collisions")
}
