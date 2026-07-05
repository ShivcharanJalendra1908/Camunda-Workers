package encryption

import (
	"fmt"
	"reflect"
	"strings"

	"camunda-workers/internal/crypto"
)

// FLEService provides field-level encryption for PII data.
// It wraps the existing crypto.Encryptor and adds per-field key isolation,
// struct-level encrypt/decrypt helpers, and type-aware operations.
type FLEService struct {
	fieldKeys map[string]*crypto.Encryptor // field name -> encryptor
	enabled   bool
}

// FieldConfig holds the encryption configuration for a single field.
type FieldConfig struct {
	Key string `mapstructure:"key"` // base64-encoded 64-byte key
}

// FLEConfig is the top-level FLE configuration.
type FLEConfig struct {
	Enabled bool                    `mapstructure:"fle_enabled"`
	Fields  map[string]FieldConfig  `mapstructure:"fields"`
}

// NewFLEService creates an FLEService from the provided configuration.
// Each field gets its own Encryptor with an isolated key.
func NewFLEService(cfg FLEConfig) (*FLEService, error) {
	if !cfg.Enabled {
		return &FLEService{enabled: false}, nil
	}

	fieldKeys := make(map[string]*crypto.Encryptor)
	for fieldName, fieldCfg := range cfg.Fields {
		if fieldCfg.Key == "" {
			continue
		}
		enc, err := crypto.NewEncryptor(fieldCfg.Key)
		if err != nil {
			return nil, fmt.Errorf("FLE: failed to create encryptor for field %q: %w", fieldName, err)
		}
		fieldKeys[fieldName] = enc
	}

	return &FLEService{
		fieldKeys: fieldKeys,
		enabled:   true,
	}, nil
}

// Enabled returns whether FLE is active.
func (f *FLEService) Enabled() bool {
	return f.enabled
}

// EncryptField encrypts a single field value using the key registered for fieldName.
// Empty strings are returned as-is (no encryption performed).
func (f *FLEService) EncryptField(fieldName, plaintext string) (string, error) {
	if !f.enabled || plaintext == "" {
		return plaintext, nil
	}
	enc, ok := f.fieldKeys[fieldName]
	if !ok {
		return plaintext, nil // field not configured for encryption
	}
	ciphertext, err := enc.Encrypt([]byte(plaintext))
	if err != nil {
		return "", fmt.Errorf("FLE: encrypt field %q: %w", fieldName, err)
	}
	return ciphertext, nil
}

// DecryptField decrypts a single field value using the key registered for fieldName.
func (f *FLEService) DecryptField(fieldName, ciphertext string) (string, error) {
	if !f.enabled {
		return ciphertext, nil
	}
	enc, ok := f.fieldKeys[fieldName]
	if !ok {
		return ciphertext, nil // field not configured for encryption
	}
	plaintext, err := enc.Decrypt(ciphertext)
	if err != nil {
		return "", fmt.Errorf("FLE: decrypt field %q: %w", fieldName, err)
	}
	return string(plaintext), nil
}

// EncryptProfileData encrypts PII fields in a map[string]interface{} profile.
// Fields tagged with `fle:"fieldname"` in the User struct are encrypted.
// The map keys must match the JSON tags of the struct fields.
func (f *FLEService) EncryptProfileData(data map[string]interface{}) error {
	if !f.enabled || data == nil {
		return nil
	}

	fleFields := getFLEFieldMap()
	for jsonKey, fieldName := range fleFields {
		val, exists := data[jsonKey]
		if !exists || val == nil {
			continue
		}
		strVal, ok := val.(string)
		if !ok {
			continue // skip non-string fields
		}
		if strVal == "" {
			continue
		}
		encrypted, err := f.EncryptField(fieldName, strVal)
		if err != nil {
			return err
		}
		data[jsonKey] = encrypted
	}
	return nil
}

// DecryptProfileData decrypts PII fields in a map[string]interface{} profile.
func (f *FLEService) DecryptProfileData(data map[string]interface{}) error {
	if !f.enabled || data == nil {
		return nil
	}

	fleFields := getFLEFieldMap()
	for jsonKey, fieldName := range fleFields {
		val, exists := data[jsonKey]
		if !exists || val == nil {
			continue
		}
		strVal, ok := val.(string)
		if !ok {
			continue
		}
		if strVal == "" {
			continue
		}
		decrypted, err := f.DecryptField(fieldName, strVal)
		if err != nil {
			return err
		}
		data[jsonKey] = decrypted
	}
	return nil
}

// EncryptStructFields encrypts all fields tagged with `fle:"fieldname"` in a struct.
// The struct is modified in place. Only string and *string fields are supported.
func (f *FLEService) EncryptStructFields(v interface{}) error {
	if !f.enabled || v == nil {
		return nil
	}

	val := reflect.ValueOf(v)
	if val.Kind() == reflect.Ptr {
		val = val.Elem()
	}
	if val.Kind() != reflect.Struct {
		return fmt.Errorf("FLE: expected struct, got %T", v)
	}

	typ := val.Type()
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		fleTag := field.Tag.Get("fle")
		if fleTag == "" {
			continue
		}

		fieldVal := val.Field(i)
		if !fieldVal.CanSet() {
			continue
		}

		switch fieldVal.Kind() {
		case reflect.String:
			str := fieldVal.String()
			if str == "" {
				continue
			}
			encrypted, err := f.EncryptField(fleTag, str)
			if err != nil {
				return fmt.Errorf("FLE: encrypt struct field %s: %w", field.Name, err)
			}
			fieldVal.SetString(encrypted)

		case reflect.Ptr:
			if fieldVal.IsNil() {
				continue
			}
			if fieldVal.Elem().Kind() == reflect.String {
				str := fieldVal.Elem().String()
				if str == "" {
					continue
				}
				encrypted, err := f.EncryptField(fleTag, str)
				if err != nil {
					return fmt.Errorf("FLE: encrypt struct field %s: %w", field.Name, err)
				}
				fieldVal.Elem().SetString(encrypted)
			}
		}
	}
	return nil
}

// DecryptStructFields decrypts all fields tagged with `fle:"fieldname"` in a struct.
func (f *FLEService) DecryptStructFields(v interface{}) error {
	if !f.enabled || v == nil {
		return nil
	}

	val := reflect.ValueOf(v)
	if val.Kind() == reflect.Ptr {
		val = val.Elem()
	}
	if val.Kind() != reflect.Struct {
		return fmt.Errorf("FLE: expected struct, got %T", v)
	}

	typ := val.Type()
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		fleTag := field.Tag.Get("fle")
		if fleTag == "" {
			continue
		}

		fieldVal := val.Field(i)
		if !fieldVal.CanSet() {
			continue
		}

		switch fieldVal.Kind() {
		case reflect.String:
			str := fieldVal.String()
			if str == "" {
				continue
			}
			decrypted, err := f.DecryptField(fleTag, str)
			if err != nil {
				return fmt.Errorf("FLE: decrypt struct field %s: %w", field.Name, err)
			}
			fieldVal.SetString(decrypted)

		case reflect.Ptr:
			if fieldVal.IsNil() {
				continue
			}
			if fieldVal.Elem().Kind() == reflect.String {
				str := fieldVal.Elem().String()
				if str == "" {
					continue
				}
				decrypted, err := f.DecryptField(fleTag, str)
				if err != nil {
					return fmt.Errorf("FLE: decrypt struct field %s: %w", field.Name, err)
				}
				fieldVal.Elem().SetString(decrypted)
			}
		}
	}
	return nil
}

// getFLEFieldMap returns a mapping of JSON tag -> FLE field name for User model fields.
// This is the single source of truth for which fields are encrypted.
func getFLEFieldMap() map[string]string {
	return map[string]string{
		"email":        "email",
		"name":         "name",
		"phone":        "phone",
		"location":     "location",
		"company":      "company",
		"jobTitle":     "jobTitle",
		"businessName": "businessName",
		"cinNumber":    "cinNumber",
		"gstNumber":    "gstNumber",
	}
}

// GetFLEFieldNames returns the list of field names that are encrypted.
func GetFLEFieldNames() []string {
	fleMap := getFLEFieldMap()
	names := make([]string, 0, len(fleMap))
	seen := make(map[string]bool)
	for _, fieldName := range fleMap {
		if !seen[fieldName] {
			names = append(names, fieldName)
			seen[fieldName] = true
		}
	}
	return names
}

// IsEncryptedField checks if a given JSON key corresponds to an FLE-encrypted field.
func IsEncryptedField(jsonKey string) bool {
	fleMap := getFLEFieldMap()
	_, exists := fleMap[jsonKey]
	return exists
}

// SanitizeForLog removes or masks FLE-encrypted field values for safe logging.
// Non-FLE fields are passed through unchanged.
func SanitizeForLog(data map[string]interface{}) map[string]interface{} {
	if data == nil {
		return nil
	}
	sanitized := make(map[string]interface{}, len(data))
	fleMap := getFLEFieldMap()
	for k, v := range data {
		if _, isFLE := fleMap[k]; isFLE {
			if str, ok := v.(string); ok && len(str) > 8 {
				sanitized[k] = str[:4] + "****" + str[len(str)-4:]
			} else {
				sanitized[k] = "****"
			}
		} else {
			sanitized[k] = v
		}
	}
	return sanitized
}

// FieldsFromMap extracts only the FLE-relevant fields from a data map.
func FieldsFromMap(data map[string]interface{}) map[string]interface{} {
	if data == nil {
		return nil
	}
	result := make(map[string]interface{})
	fleMap := getFLEFieldMap()
	for jsonKey := range fleMap {
		if val, exists := data[jsonKey]; exists {
			result[jsonKey] = val
		}
	}
	return result
}

// HasFLEFields checks if a data map contains any FLE-encrypted fields.
func HasFLEFields(data map[string]interface{}) bool {
	if data == nil {
		return false
	}
	fleMap := getFLEFieldMap()
	for jsonKey := range fleMap {
		if _, exists := data[jsonKey]; exists {
			return true
		}
	}
	return false
}

// FilterNonFLEFields returns a new map with only non-FLE fields.
func FilterNonFLEFields(data map[string]interface{}) map[string]interface{} {
	if data == nil {
		return nil
	}
	result := make(map[string]interface{})
	fleMap := getFLEFieldMap()
	for k, v := range data {
		if _, isFLE := fleMap[k]; !isFLE {
			result[k] = v
		}
	}
	return result
}

// MergeMaps merges src into dst. Non-FLE fields from src override dst.
// FLE fields from src are only added if dst doesn't already have them.
func MergeMaps(dst, src map[string]interface{}) map[string]interface{} {
	if dst == nil {
		dst = make(map[string]interface{})
	}
	if src == nil {
		return dst
	}
	fleMap := getFLEFieldMap()
	for k, v := range src {
		if _, isFLE := fleMap[k]; isFLE {
			if _, exists := dst[k]; !exists {
				dst[k] = v
			}
		} else {
			dst[k] = v
		}
	}
	return dst
}

// StringHasPrefix checks if a string starts with a given prefix (utility for ciphertext detection).
func StringHasPrefix(s, prefix string) bool {
	return strings.HasPrefix(s, prefix)
}
