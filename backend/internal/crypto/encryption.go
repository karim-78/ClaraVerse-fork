package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"

	"golang.org/x/crypto/hkdf"
	"crypto/sha256"
)

// EncryptionService handles encryption/decryption of user data
type EncryptionService struct {
	masterKey []byte
}

// NewEncryptionService creates a new encryption service with the given master key
// masterKey should be a 32-byte hex-encoded string (64 characters)
func NewEncryptionService(masterKeyHex string) (*EncryptionService, error) {
	if masterKeyHex == "" {
		return nil, errors.New("encryption master key is required")
	}

	masterKey, err := hex.DecodeString(masterKeyHex)
	if err != nil {
		return nil, fmt.Errorf("invalid master key format (must be hex): %w", err)
	}

	if len(masterKey) != 32 {
		return nil, fmt.Errorf("master key must be 32 bytes (64 hex characters), got %d bytes", len(masterKey))
	}

	return &EncryptionService{
		masterKey: masterKey,
	}, nil
}

// DeriveUserKey derives a unique encryption key for a specific user
// using HKDF (HMAC-based Key Derivation Function)
func (e *EncryptionService) DeriveUserKey(userID string) ([]byte, error) {
	if userID == "" {
		return nil, errors.New("user ID is required for key derivation")
	}

	// Use HKDF to derive a user-specific key
	hkdfReader := hkdf.New(sha256.New, e.masterKey, []byte(userID), []byte("claraverse-user-encryption"))

	userKey := make([]byte, 32) // AES-256 requires 32-byte key
	if _, err := io.ReadFull(hkdfReader, userKey); err != nil {
		return nil, fmt.Errorf("failed to derive user key: %w", err)
	}

	return userKey, nil
}

// Encrypt encrypts plaintext using AES-256-GCM with a user-specific key
// Returns base64-encoded ciphertext (nonce prepended)
func (e *EncryptionService) Encrypt(userID string, plaintext []byte) (string, error) {
	if len(plaintext) == 0 {
		return "", nil // Return empty string for empty input
	}

	// Derive user-specific key
	userKey, err := e.DeriveUserKey(userID)
	if err != nil {
		return "", err
	}

	// Create AES cipher
	block, err := aes.NewCipher(userKey)
	if err != nil {
		return "", fmt.Errorf("failed to create cipher: %w", err)
	}

	// Create GCM mode
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("failed to create GCM: %w", err)
	}

	// Generate random nonce
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("failed to generate nonce: %w", err)
	}

	// Encrypt and prepend nonce
	ciphertext := gcm.Seal(nonce, nonce, plaintext, nil)

	// Return as base64
	return base64.StdEncoding.EncodeToString(ciphertext), nil
}

// Decrypt decrypts base64-encoded ciphertext using AES-256-GCM
func (e *EncryptionService) Decrypt(userID string, ciphertextB64 string) ([]byte, error) {
	if ciphertextB64 == "" {
		return nil, nil // Return nil for empty input
	}

	// Decode base64
	ciphertext, err := base64.StdEncoding.DecodeString(ciphertextB64)
	if err != nil {
		return nil, fmt.Errorf("failed to decode ciphertext: %w", err)
	}

	// Derive user-specific key
	userKey, err := e.DeriveUserKey(userID)
	if err != nil {
		return nil, err
	}

	// Create AES cipher
	block, err := aes.NewCipher(userKey)
	if err != nil {
		return nil, fmt.Errorf("failed to create cipher: %w", err)
	}

	// Create GCM mode
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("failed to create GCM: %w", err)
	}

	// Extract nonce
	nonceSize := gcm.NonceSize()
	if len(ciphertext) < nonceSize {
		return nil, errors.New("ciphertext too short")
	}

	nonce, ciphertext := ciphertext[:nonceSize], ciphertext[nonceSize:]

	// Decrypt
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to decrypt: %w", err)
	}

	return plaintext, nil
}

// EncryptString is a convenience method for encrypting strings
func (e *EncryptionService) EncryptString(userID string, plaintext string) (string, error) {
	return e.Encrypt(userID, []byte(plaintext))
}

// DecryptString is a convenience method for decrypting to strings
func (e *EncryptionService) DecryptString(userID string, ciphertext string) (string, error) {
	plaintext, err := e.Decrypt(userID, ciphertext)
	if err != nil {
		return "", err
	}
	return string(plaintext), nil
}

// EncryptJSON encrypts a JSON byte slice
func (e *EncryptionService) EncryptJSON(userID string, jsonData []byte) (string, error) {
	return e.Encrypt(userID, jsonData)
}

// DecryptJSON decrypts to a JSON byte slice
func (e *EncryptionService) DecryptJSON(userID string, ciphertext string) ([]byte, error) {
	return e.Decrypt(userID, ciphertext)
}

// GenerateMasterKey generates a new random 32-byte master key (for setup)
func GenerateMasterKey() (string, error) {
	key := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return "", fmt.Errorf("failed to generate key: %w", err)
	}
	return hex.EncodeToString(key), nil
}
