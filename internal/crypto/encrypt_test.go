package crypto

import (
	"bytes"
	"encoding/base64"
	"strings"
	"testing"
)

func TestGenerateKey(t *testing.T) {
	key, err := GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}

	// Should be base64-encoded 64 bytes = 88 chars with padding
	if len(key) == 0 {
		t.Fatal("GenerateKey() returned empty key")
	}

	// Verify it's valid base64
	enc, err := NewEncryptor(key)
	if err != nil {
		t.Fatalf("Generated key rejected by NewEncryptor: %v", err)
	}
	if enc == nil {
		t.Fatal("NewEncryptor returned nil")
	}
}

func TestNewEncryptor_InvalidKey(t *testing.T) {
	tests := []struct {
		name    string
		key     string
		wantErr bool
	}{
		{"empty key", "", true},
		{"too short", "dGVzdA==", true}, // "test" base64
		{"not base64", "!!!not-base64!!!", true},
		{"valid 64 bytes", func() string { k, _ := GenerateKey(); return k }(), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewEncryptor(tt.key)
			if (err != nil) != tt.wantErr {
				t.Errorf("NewEncryptor() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestEncryptDecrypt_RoundTrip(t *testing.T) {
	key, _ := GenerateKey()
	enc, err := NewEncryptor(key)
	if err != nil {
		t.Fatalf("NewEncryptor() error = %v", err)
	}

	tests := []struct {
		name      string
		plaintext string
	}{
		{"simple string", "hello world"},
		{"empty string", ""},
		{"unicode", "नमस्ते दुनिया"},
		{"json", `{"token":"abc123","ip":"192.168.1.1"}`},
		{"long string", strings.Repeat("a", 10000)},
		{"binary-like", "\x00\x01\x02\xff\xfe\xfd"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ciphertext, err := enc.Encrypt([]byte(tt.plaintext))
			if err != nil {
				t.Fatalf("Encrypt() error = %v", err)
			}

			if ciphertext == "" {
				t.Fatal("Encrypt() returned empty ciphertext")
			}

			// Ciphertext should not contain plaintext (except maybe for empty)
			if tt.plaintext != "" && strings.Contains(ciphertext, tt.plaintext) {
				t.Error("Ciphertext contains plaintext!")
			}

			decrypted, err := enc.Decrypt(ciphertext)
			if err != nil {
				t.Fatalf("Decrypt() error = %v", err)
			}

			if !bytes.Equal(decrypted, []byte(tt.plaintext)) {
				t.Errorf("Decrypt() = %q, want %q", decrypted, tt.plaintext)
			}
		})
	}
}

func TestDecrypt_TamperedData(t *testing.T) {
	key, _ := GenerateKey()
	enc, _ := NewEncryptor(key)

	ciphertext, _ := enc.Encrypt([]byte("secret data"))

	// Decode base64, tamper with raw bytes, re-encode
	raw, _ := base64.StdEncoding.DecodeString(ciphertext)
	raw[10] ^= 0xff // flip a byte in the nonce/ciphertext
	tampered := base64.StdEncoding.EncodeToString(raw)

	_, err := enc.Decrypt(tampered)
	if err != ErrTamperedData {
		t.Errorf("Decrypt() on tampered data: got %v, want ErrTamperedData", err)
	}
}

func TestDecrypt_WrongKey(t *testing.T) {
	key1, _ := GenerateKey()
	key2, _ := GenerateKey()

	enc1, _ := NewEncryptor(key1)
	enc2, _ := NewEncryptor(key2)

	ciphertext, _ := enc1.Encrypt([]byte("secret data"))

	_, err := enc2.Decrypt(ciphertext)
	if err == nil {
		t.Error("Decrypt() with wrong key should fail")
	}
}

func TestDecrypt_InvalidBase64(t *testing.T) {
	key, _ := GenerateKey()
	enc, _ := NewEncryptor(key)

	_, err := enc.Decrypt("!!!not-base64!!!")
	if err == nil {
		t.Error("Decrypt() with invalid base64 should fail")
	}
}

func TestEncrypt_DifferentNonces(t *testing.T) {
	key, _ := GenerateKey()
	enc, _ := NewEncryptor(key)

	// Same plaintext encrypted twice should produce different ciphertext (different nonce)
	c1, _ := enc.Encrypt([]byte("same input"))
	c2, _ := enc.Encrypt([]byte("same input"))

	if c1 == c2 {
		t.Error("Two encryptions of same plaintext produced identical ciphertext (nonce reuse?)")
	}

	// Both should decrypt to same plaintext
	d1, _ := enc.Decrypt(c1)
	d2, _ := enc.Decrypt(c2)

	if !bytes.Equal(d1, d2) {
		t.Error("Decrypted values differ for same plaintext")
	}
}

func BenchmarkEncrypt(b *testing.B) {
	key, _ := GenerateKey()
	enc, _ := NewEncryptor(key)
	plaintext := []byte("benchmark test data for encryption performance")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		enc.Encrypt(plaintext)
	}
}

func BenchmarkDecrypt(b *testing.B) {
	key, _ := GenerateKey()
	enc, _ := NewEncryptor(key)
	plaintext := []byte("benchmark test data for decryption performance")
	ciphertext, _ := enc.Encrypt(plaintext)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		enc.Decrypt(ciphertext)
	}
}
