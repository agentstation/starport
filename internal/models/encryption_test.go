package models

import (
	"strings"
	"testing"
)

func TestEncryptionService_EncryptDecrypt(t *testing.T) {
	// Generate a test master key
	masterKey, err := GenerateMasterKey()
	if err != nil {
		t.Fatalf("Failed to generate master key: %v", err)
	}

	service, err := NewEncryptionService(masterKey)
	if err != nil {
		t.Fatalf("Failed to create encryption service: %v", err)
	}

	tests := []struct {
		name      string
		plaintext string
		wantErr   bool
	}{
		{
			name:      "simple credential",
			plaintext: "sk-1234567890abcdef",
			wantErr:   false,
		},
		{
			name:      "complex credential with special chars",
			plaintext: "sk_test_4eC39HqLyjWDarjtT1zdp7dc!@#$%^&*()",
			wantErr:   false,
		},
		{
			name:      "long credential",
			plaintext: strings.Repeat("a", 1000),
			wantErr:   false,
		},
		{
			name:      "empty credential",
			plaintext: "",
			wantErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Encrypt
			encrypted, err := service.EncryptCredential(tt.plaintext)
			if (err != nil) != tt.wantErr {
				t.Errorf("EncryptCredential() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.wantErr {
				return
			}

			// Verify encrypted is different from plaintext
			if encrypted == tt.plaintext {
				t.Error("Encrypted credential should be different from plaintext")
			}

			// Verify it's base64 encoded
			if encrypted == "" {
				t.Error("Encrypted credential should not be empty")
			}

			// Decrypt
			decrypted, err := service.DecryptCredential(encrypted)
			if err != nil {
				t.Errorf("DecryptCredential() error = %v", err)
				return
			}

			// Verify decrypted matches original
			if decrypted != tt.plaintext {
				t.Errorf("Decrypted credential = %v, want %v", decrypted, tt.plaintext)
			}
		})
	}
}

func TestEncryptionService_DifferentEncryptions(t *testing.T) {
	masterKey, err := GenerateMasterKey()
	if err != nil {
		t.Fatalf("Failed to generate master key: %v", err)
	}

	service, err := NewEncryptionService(masterKey)
	if err != nil {
		t.Fatalf("Failed to create encryption service: %v", err)
	}

	plaintext := "sk-test-credential"

	// Encrypt the same plaintext multiple times
	encrypted1, err := service.EncryptCredential(plaintext)
	if err != nil {
		t.Fatalf("First encryption failed: %v", err)
	}

	encrypted2, err := service.EncryptCredential(plaintext)
	if err != nil {
		t.Fatalf("Second encryption failed: %v", err)
	}

	// Encrypted values should be different (due to random salt/nonce)
	if encrypted1 == encrypted2 {
		t.Error("Multiple encryptions of same plaintext should produce different ciphertexts")
	}

	// Both should decrypt to the same value
	decrypted1, err := service.DecryptCredential(encrypted1)
	if err != nil {
		t.Fatalf("First decryption failed: %v", err)
	}

	decrypted2, err := service.DecryptCredential(encrypted2)
	if err != nil {
		t.Fatalf("Second decryption failed: %v", err)
	}

	if decrypted1 != plaintext || decrypted2 != plaintext {
		t.Error("Both encryptions should decrypt to the original plaintext")
	}
}

func TestEncryptionService_InvalidDecryption(t *testing.T) {
	masterKey, err := GenerateMasterKey()
	if err != nil {
		t.Fatalf("Failed to generate master key: %v", err)
	}

	service, err := NewEncryptionService(masterKey)
	if err != nil {
		t.Fatalf("Failed to create encryption service: %v", err)
	}

	tests := []struct {
		name      string
		encrypted string
		wantErr   bool
	}{
		{
			name:      "empty string",
			encrypted: "",
			wantErr:   true,
		},
		{
			name:      "invalid base64",
			encrypted: "not-base64!@#$%",
			wantErr:   true,
		},
		{
			name:      "valid base64 but too short",
			encrypted: "dGVzdA==", // "test" in base64
			wantErr:   true,
		},
		{
			name:      "random valid base64",
			encrypted: "dGhpcyBpcyBhIGxvbmdlciB0ZXN0IHN0cmluZyB0aGF0IHNob3VsZCBmYWlsIGRlY3J5cHRpb24=",
			wantErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := service.DecryptCredential(tt.encrypted)
			if (err != nil) != tt.wantErr {
				t.Errorf("DecryptCredential() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestEncryptionService_DifferentKeys(t *testing.T) {
	// Create two different services with different keys
	key1, _ := GenerateMasterKey()
	key2, _ := GenerateMasterKey()

	service1, _ := NewEncryptionService(key1)
	service2, _ := NewEncryptionService(key2)

	plaintext := "sk-test-credential"

	// Encrypt with service1
	encrypted, err := service1.EncryptCredential(plaintext)
	if err != nil {
		t.Fatalf("Encryption failed: %v", err)
	}

	// Try to decrypt with service2 (different key)
	_, err = service2.DecryptCredential(encrypted)
	if err == nil {
		t.Error("Decryption with different key should fail")
	}

	// Decrypt with correct service should work
	decrypted, err := service1.DecryptCredential(encrypted)
	if err != nil {
		t.Errorf("Decryption with correct key failed: %v", err)
	}
	if decrypted != plaintext {
		t.Errorf("Decrypted value = %v, want %v", decrypted, plaintext)
	}
}

func TestNewEncryptionService_InvalidKey(t *testing.T) {
	tests := []struct {
		name    string
		key     []byte
		wantErr bool
	}{
		{
			name:    "nil key",
			key:     nil,
			wantErr: true,
		},
		{
			name:    "empty key",
			key:     []byte{},
			wantErr: true,
		},
		{
			name:    "short key",
			key:     []byte("too short"),
			wantErr: true,
		},
		{
			name:    "valid 32 byte key",
			key:     make([]byte, 32),
			wantErr: false,
		},
		{
			name:    "longer key",
			key:     make([]byte, 64),
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewEncryptionService(tt.key)
			if (err != nil) != tt.wantErr {
				t.Errorf("NewEncryptionService() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestDeriveKeyFromPassword(t *testing.T) {
	password := "test-password-123"

	// Derive key multiple times - should be deterministic
	key1 := DeriveKeyFromPassword(password)
	key2 := DeriveKeyFromPassword(password)

	if len(key1) != 32 {
		t.Errorf("Derived key length = %d, want 32", len(key1))
	}

	// Keys should be identical for same password
	for i := range key1 {
		if key1[i] != key2[i] {
			t.Error("DeriveKeyFromPassword should be deterministic")
			break
		}
	}

	// Different password should produce different key
	key3 := DeriveKeyFromPassword("different-password")
	same := true
	for i := range key1 {
		if key1[i] != key3[i] {
			same = false
			break
		}
	}
	if same {
		t.Error("Different passwords should produce different keys")
	}
}

func TestGenerateMasterKey(t *testing.T) {
	// Generate multiple keys
	key1, err := GenerateMasterKey()
	if err != nil {
		t.Fatalf("GenerateMasterKey() error = %v", err)
	}

	key2, err := GenerateMasterKey()
	if err != nil {
		t.Fatalf("GenerateMasterKey() error = %v", err)
	}

	// Check length
	if len(key1) != 32 {
		t.Errorf("Generated key length = %d, want 32", len(key1))
	}

	// Keys should be different
	same := true
	for i := range key1 {
		if key1[i] != key2[i] {
			same = false
			break
		}
	}
	if same {
		t.Error("Generated keys should be different")
	}
}

func BenchmarkEncryptCredential(b *testing.B) {
	masterKey, _ := GenerateMasterKey()
	service, _ := NewEncryptionService(masterKey)
	plaintext := "sk-test-1234567890abcdef"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := service.EncryptCredential(plaintext)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkDecryptCredential(b *testing.B) {
	masterKey, _ := GenerateMasterKey()
	service, _ := NewEncryptionService(masterKey)
	plaintext := "sk-test-1234567890abcdef"
	encrypted, _ := service.EncryptCredential(plaintext)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := service.DecryptCredential(encrypted)
		if err != nil {
			b.Fatal(err)
		}
	}
}
