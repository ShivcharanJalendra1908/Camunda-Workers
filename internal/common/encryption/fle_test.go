package encryption

import (
	"testing"

	"camunda-workers/internal/crypto"
)

func TestFLEService_EncryptDecryptField(t *testing.T) {
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("Failed to generate key: %v", err)
	}

	svc, err := NewFLEService(FLEConfig{
		Enabled: true,
		Fields: map[string]FieldConfig{
			"email": {Key: key},
			"phone": {Key: key},
		},
	})
	if err != nil {
		t.Fatalf("Failed to create FLE service: %v", err)
	}

	tests := []struct {
		name      string
		field     string
		plaintext string
	}{
		{"email encryption", "email", "user@example.com"},
		{"phone encryption", "phone", "+1234567890"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			encrypted, err := svc.EncryptField(tt.field, tt.plaintext)
			if err != nil {
				t.Fatalf("EncryptField failed: %v", err)
			}
			if encrypted == tt.plaintext {
				t.Error("EncryptField: ciphertext equals plaintext")
			}
			if encrypted == "" {
				t.Error("EncryptField: ciphertext is empty")
			}

			decrypted, err := svc.DecryptField(tt.field, encrypted)
			if err != nil {
				t.Fatalf("DecryptField failed: %v", err)
			}
			if decrypted != tt.plaintext {
				t.Errorf("DecryptField: got %q, want %q", decrypted, tt.plaintext)
			}
		})
	}

	t.Run("empty string passthrough", func(t *testing.T) {
		encrypted, err := svc.EncryptField("email", "")
		if err != nil {
			t.Errorf("EncryptField empty: unexpected error: %v", err)
		}
		if encrypted != "" {
			t.Errorf("EncryptField empty: expected empty, got %q", encrypted)
		}
	})

	t.Run("unconfigured field passthrough", func(t *testing.T) {
		plaintext := "Hindi Name Test"
		encrypted, err := svc.EncryptField("unconfigured", plaintext)
		if err != nil {
			t.Errorf("EncryptField unconfigured: unexpected error: %v", err)
		}
		if encrypted != plaintext {
			t.Errorf("EncryptField unconfigured: expected pass-through, got %q", encrypted)
		}
	})
}

func TestFLEService_UnconfiguredField(t *testing.T) {
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("Failed to generate key: %v", err)
	}

	svc, err := NewFLEService(FLEConfig{
		Enabled: true,
		Fields: map[string]FieldConfig{
			"email": {Key: key},
		},
	})
	if err != nil {
		t.Fatalf("Failed to create FLE service: %v", err)
	}

	// Unconfigured field should pass through unchanged
	plaintext := "test value"
	encrypted, err := svc.EncryptField("unconfigured", plaintext)
	if err != nil {
		t.Errorf("EncryptField unconfigured: unexpected error: %v", err)
	}
	if encrypted != plaintext {
		t.Errorf("EncryptField unconfigured: expected pass-through, got %q", encrypted)
	}
}

func TestFLEService_WrongKey(t *testing.T) {
	key1, _ := crypto.GenerateKey()
	key2, _ := crypto.GenerateKey()

	svc1, _ := NewFLEService(FLEConfig{
		Enabled: true,
		Fields:  map[string]FieldConfig{"email": {Key: key1}},
	})
	svc2, _ := NewFLEService(FLEConfig{
		Enabled: true,
		Fields:  map[string]FieldConfig{"email": {Key: key2}},
	})

	encrypted, err := svc1.EncryptField("email", "test@example.com")
	if err != nil {
		t.Fatalf("EncryptField failed: %v", err)
	}

	_, err = svc2.DecryptField("email", encrypted)
	if err == nil {
		t.Error("DecryptField with wrong key: expected error, got nil")
	}
}

func TestFLEService_TamperedData(t *testing.T) {
	key, _ := crypto.GenerateKey()
	svc, _ := NewFLEService(FLEConfig{
		Enabled: true,
		Fields:  map[string]FieldConfig{"email": {Key: key}},
	})

	encrypted, err := svc.EncryptField("email", "test@example.com")
	if err != nil {
		t.Fatalf("EncryptField failed: %v", err)
	}

	// Tamper with the ciphertext
	tampered := encrypted[:len(encrypted)-2] + "XX"
	_, err = svc.DecryptField("email", tampered)
	if err == nil {
		t.Error("DecryptField tampered: expected error, got nil")
	}
}

func TestFLEService_Disabled(t *testing.T) {
	svc, err := NewFLEService(FLEConfig{Enabled: false})
	if err != nil {
		t.Fatalf("Failed to create FLE service: %v", err)
	}

	if svc.Enabled() {
		t.Error("Expected FLE to be disabled")
	}

	// Should pass through unchanged
	plaintext := "test@example.com"
	encrypted, err := svc.EncryptField("email", plaintext)
	if err != nil {
		t.Errorf("EncryptField disabled: unexpected error: %v", err)
	}
	if encrypted != plaintext {
		t.Errorf("EncryptField disabled: expected pass-through, got %q", encrypted)
	}
}

func TestFLEService_EncryptProfileData(t *testing.T) {
	key, _ := crypto.GenerateKey()
	svc, _ := NewFLEService(FLEConfig{
		Enabled: true,
		Fields:  map[string]FieldConfig{"email": {Key: key}, "phone": {Key: key}},
	})

	data := map[string]interface{}{
		"email":        "user@example.com",
		"phone":        "+1234567890",
		"name":         "John Doe",
		"theme":        "dark",
	}

	err := svc.EncryptProfileData(data)
	if err != nil {
		t.Fatalf("EncryptProfileData failed: %v", err)
	}

	// email and phone should be encrypted
	if data["email"] == "user@example.com" {
		t.Error("EncryptProfileData: email not encrypted")
	}
	if data["phone"] == "+1234567890" {
		t.Error("EncryptProfileData: phone not encrypted")
	}
	// name is not in FLE config, should pass through
	if data["name"] != "John Doe" {
		t.Error("EncryptProfileData: non-FLE field was modified")
	}
	// theme is not in FLE config
	if data["theme"] != "dark" {
		t.Error("EncryptProfileData: non-FLE field was modified")
	}

	// Decrypt
	err = svc.DecryptProfileData(data)
	if err != nil {
		t.Fatalf("DecryptProfileData failed: %v", err)
	}
	if data["email"] != "user@example.com" {
		t.Errorf("DecryptProfileData: email = %q, want %q", data["email"], "user@example.com")
	}
	if data["phone"] != "+1234567890" {
		t.Errorf("DecryptProfileData: phone = %q, want %q", data["phone"], "+1234567890")
	}
}

func TestFLEService_EncryptStructFields(t *testing.T) {
	key, _ := crypto.GenerateKey()
	svc, _ := NewFLEService(FLEConfig{
		Enabled: true,
		Fields:  map[string]FieldConfig{"email": {Key: key}},
	})

	type TestUser struct {
		Email string `fle:"email" json:"email"`
		Name  string `json:"name"`
	}

	user := &TestUser{
		Email: "test@example.com",
		Name:  "John Doe",
	}

	err := svc.EncryptStructFields(user)
	if err != nil {
		t.Fatalf("EncryptStructFields failed: %v", err)
	}

	if user.Email == "test@example.com" {
		t.Error("EncryptStructFields: email not encrypted")
	}
	if user.Name != "John Doe" {
		t.Error("EncryptStructFields: non-FLE field was modified")
	}

	err = svc.DecryptStructFields(user)
	if err != nil {
		t.Fatalf("DecryptStructFields failed: %v", err)
	}
	if user.Email != "test@example.com" {
		t.Errorf("DecryptStructFields: email = %q, want %q", user.Email, "test@example.com")
	}
}

func TestFLEService_NilData(t *testing.T) {
	key, _ := crypto.GenerateKey()
	svc, _ := NewFLEService(FLEConfig{
		Enabled: true,
		Fields:  map[string]FieldConfig{"email": {Key: key}},
	})

	if err := svc.EncryptProfileData(nil); err != nil {
		t.Errorf("EncryptProfileData nil: %v", err)
	}
	if err := svc.DecryptProfileData(nil); err != nil {
		t.Errorf("DecryptProfileData nil: %v", err)
	}
}

func TestGetFLEFieldMap(t *testing.T) {
	fleMap := getFLEFieldMap()
	if len(fleMap) == 0 {
		t.Error("getFLEFieldMap: empty map")
	}

	requiredFields := []string{"email", "name", "phone", "location"}
	for _, field := range requiredFields {
		if _, exists := fleMap[field]; !exists {
			t.Errorf("getFLEFieldMap: missing required field %q", field)
		}
	}
}

func TestIsEncryptedField(t *testing.T) {
	if !IsEncryptedField("email") {
		t.Error("IsEncryptedField: email should be encrypted")
	}
	if IsEncryptedField("theme") {
		t.Error("IsEncryptedField: theme should not be encrypted")
	}
}

func TestSanitizeForLog(t *testing.T) {
	data := map[string]interface{}{
		"email": "user@example.com",
		"name":  "John Doe",
		"theme": "dark",
		"role":  "admin",
	}

	sanitized := SanitizeForLog(data)
	if sanitized["email"] == "user@example.com" {
		t.Error("SanitizeForLog: email not sanitized")
	}
	// name IS in FLE map, so it should be sanitized
	if sanitized["name"] == "John Doe" {
		t.Error("SanitizeForLog: name should be sanitized (it's in FLE map)")
	}
	// theme is NOT in FLE map, should pass through
	if sanitized["theme"] != "dark" {
		t.Error("SanitizeForLog: theme was modified")
	}
	if sanitized["role"] != "admin" {
		t.Error("SanitizeForLog: role was modified")
	}
}

func BenchmarkFLEEncryptDecrypt(b *testing.B) {
	key, _ := crypto.GenerateKey()
	svc, _ := NewFLEService(FLEConfig{
		Enabled: true,
		Fields:  map[string]FieldConfig{"email": {Key: key}},
	})

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		encrypted, _ := svc.EncryptField("email", "user@example.com")
		svc.DecryptField("email", encrypted)
	}
}
