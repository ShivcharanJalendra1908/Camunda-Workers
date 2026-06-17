package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
)

var (
	ErrInvalidKey       = errors.New("crypto: invalid key (must be 32 bytes base64-encoded)")
	ErrDecryptionFailed = errors.New("crypto: decryption failed")
	ErrTamperedData     = errors.New("crypto: data integrity check failed (HMAC mismatch)")
)

// Encryptor provides AES-256-GCM encryption with HMAC-SHA256 integrity verification.
type Encryptor struct {
	aesKey  []byte // 32 bytes for AES-256
	hmacKey []byte // 32 bytes for HMAC-SHA256
}

// NewEncryptor creates an Encryptor from a base64-encoded 32-byte key.
// The key is split into two 32-byte halves: first for AES, second for HMAC.
func NewEncryptor(base64Key string) (*Encryptor, error) {
	keyBytes, err := base64.StdEncoding.DecodeString(base64Key)
	if err != nil {
		return nil, fmt.Errorf("%w: base64 decode failed: %v", ErrInvalidKey, err)
	}

	// We need 64 bytes: 32 for AES-256 + 32 for HMAC-SHA256
	if len(keyBytes) < 64 {
		return nil, fmt.Errorf("%w: key too short (need 64 bytes, got %d)", ErrInvalidKey, len(keyBytes))
	}

	return &Encryptor{
		aesKey:  keyBytes[:32],
		hmacKey: keyBytes[32:64],
	}, nil
}

// Encrypt encrypts plaintext using AES-256-GCM and returns base64-encoded ciphertext
// with prepended 12-byte nonce. Format: base64(nonce + ciphertext)
func (e *Encryptor) Encrypt(plaintext []byte) (string, error) {
	block, err := aes.NewCipher(e.aesKey)
	if err != nil {
		return "", fmt.Errorf("aes.NewCipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("cipher.NewGCM: %w", err)
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("nonce generation: %w", err)
	}

	ciphertext := gcm.Seal(nonce, nonce, plaintext, nil) // nonce prepended

	// HMAC over ciphertext for integrity
	mac := hmac.New(sha256.New, e.hmacKey)
	mac.Write(ciphertext)
	tag := mac.Sum(nil) // 32 bytes

	// Format: ciphertext + HMAC tag
	result := append(ciphertext, tag...)
	return base64.StdEncoding.EncodeToString(result), nil
}

// Decrypt verifies HMAC-SHA256 integrity then decrypts AES-256-GCM ciphertext.
// Input must be base64-encoded ciphertext with prepended nonce.
func (e *Encryptor) Decrypt(encoded string) ([]byte, error) {
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("base64 decode: %w", err)
	}

	// Split: last 32 bytes are HMAC tag, rest is nonce + ciphertext
	if len(data) < 32+12+1 { // nonce(12) + at least 1 byte cipher + tag(32)
		return nil, ErrDecryptionFailed
	}

	tagStart := len(data) - 32
	sealed := data[:tagStart]
	receivedTag := data[tagStart:]

	// Verify HMAC
	mac := hmac.New(sha256.New, e.hmacKey)
	mac.Write(sealed)
	expectedTag := mac.Sum(nil)

	if !hmac.Equal(receivedTag, expectedTag) {
		return nil, ErrTamperedData
	}

	// Decrypt
	block, err := aes.NewCipher(e.aesKey)
	if err != nil {
		return nil, fmt.Errorf("aes.NewCipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("cipher.NewGCM: %w", err)
	}

	nonceSize := gcm.NonceSize()
	if len(sealed) < nonceSize {
		return nil, ErrDecryptionFailed
	}

	nonce, ciphertext := sealed[:nonceSize], sealed[nonceSize:]
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrDecryptionFailed, err)
	}

	return plaintext, nil
}

// GenerateKey generates a cryptographically secure 64-byte key encoded as base64.
// First 32 bytes: AES-256 key, last 32 bytes: HMAC-SHA256 key.
func GenerateKey() (string, error) {
	key := make([]byte, 64)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return "", fmt.Errorf("key generation: %w", err)
	}
	return base64.StdEncoding.EncodeToString(key), nil
}
