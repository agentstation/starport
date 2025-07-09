package models

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"

	"golang.org/x/crypto/argon2"
)

// Encryption configuration constants
const (
	// Argon2 parameters for key derivation
	argon2Time    = 1
	argon2Memory  = 64 * 1024 // 64MB
	argon2Threads = 4
	argon2KeyLen  = 32 // 256 bits for AES-256

	// AES-GCM parameters
	gcmNonceSize = 12
	saltSize     = 32
)

// EncryptionService provides encryption and decryption for sensitive data
type EncryptionService struct {
	masterKey []byte
}

// NewEncryptionService creates a new encryption service with the provided master key
func NewEncryptionService(masterKey []byte) (*EncryptionService, error) {
	if len(masterKey) < 32 {
		return nil, errors.New("master key must be at least 32 bytes")
	}
	// Make a copy of the key to ensure immutability
	keyCopy := make([]byte, len(masterKey))
	copy(keyCopy, masterKey)
	return &EncryptionService{
		masterKey: keyCopy,
	}, nil
}

// EncryptCredential encrypts a credential using AES-256-GCM with Argon2 key derivation
func (s *EncryptionService) EncryptCredential(plaintext string) (string, error) {
	if plaintext == "" {
		return "", errors.New("cannot encrypt empty credential")
	}

	// Generate a random salt
	salt := make([]byte, saltSize)
	if _, err := io.ReadFull(rand.Reader, salt); err != nil {
		return "", fmt.Errorf("failed to generate salt: %w", err)
	}

	// Derive encryption key using Argon2
	key := s.deriveKey(salt)

	// Create AES cipher
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("failed to create cipher: %w", err)
	}

	// Create GCM mode
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("failed to create GCM: %w", err)
	}

	// Generate nonce
	nonce := make([]byte, gcmNonceSize)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("failed to generate nonce: %w", err)
	}

	// Encrypt the plaintext
	ciphertext := gcm.Seal(nil, nonce, []byte(plaintext), nil) // #nosec G407 -- nonce is randomly generated above

	// Combine salt + nonce + ciphertext
	combined := make([]byte, 0, saltSize+gcmNonceSize+len(ciphertext))
	combined = append(combined, salt...)
	combined = append(combined, nonce...)
	combined = append(combined, ciphertext...)

	// Encode to base64 for storage
	return base64.StdEncoding.EncodeToString(combined), nil
}

// DecryptCredential decrypts a credential encrypted with EncryptCredential
func (s *EncryptionService) DecryptCredential(encrypted string) (string, error) {
	if encrypted == "" {
		return "", errors.New("cannot decrypt empty credential")
	}

	// Decode from base64
	combined, err := base64.StdEncoding.DecodeString(encrypted)
	if err != nil {
		return "", fmt.Errorf("failed to decode credential: %w", err)
	}

	// Check minimum length
	minLen := saltSize + gcmNonceSize + 16 // 16 is GCM tag size
	if len(combined) < minLen {
		return "", errors.New("invalid encrypted credential format")
	}

	// Extract components
	salt := combined[:saltSize]
	nonce := combined[saltSize : saltSize+gcmNonceSize]
	ciphertext := combined[saltSize+gcmNonceSize:]

	// Derive encryption key using Argon2
	key := s.deriveKey(salt)

	// Create AES cipher
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("failed to create cipher: %w", err)
	}

	// Create GCM mode
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("failed to create GCM: %w", err)
	}

	// Decrypt the ciphertext
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", fmt.Errorf("failed to decrypt credential: %w", err)
	}

	return string(plaintext), nil
}

// deriveKey derives an encryption key from the master key and salt using Argon2
func (s *EncryptionService) deriveKey(salt []byte) []byte {
	return argon2.IDKey(s.masterKey, salt, argon2Time, argon2Memory, argon2Threads, argon2KeyLen)
}

// GenerateMasterKey generates a new random master key
func GenerateMasterKey() ([]byte, error) {
	key := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return nil, fmt.Errorf("failed to generate master key: %w", err)
	}
	return key, nil
}

// DeriveKeyFromPassword derives a master key from a password using Argon2
func DeriveKeyFromPassword(password string) []byte {
	// Use SHA256 of password as salt for deterministic key generation
	salt := sha256.Sum256([]byte(password))
	return argon2.IDKey([]byte(password), salt[:], argon2Time*2, argon2Memory, argon2Threads, argon2KeyLen)
}