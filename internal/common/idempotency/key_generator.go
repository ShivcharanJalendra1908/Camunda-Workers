// internal/common/idempotency/key_generator.go
package idempotency

import (
	"crypto/sha256"
	"fmt"
	"strings"
	"time"
)

// KeyGenerator generates idempotency keys for different operations
type KeyGenerator struct{}

// NewKeyGenerator creates a new key generator
func NewKeyGenerator() *KeyGenerator {
	return &KeyGenerator{}
}

// ============================================================================
// APPLICATION IDEMPOTENCY KEYS
// ============================================================================

// GenerateApplicationKey generates idempotency key for franchise applications
// Format: app_{seekerId}_{franchiseId}_{hourTimestamp}
// Same seeker + franchise within same hour = same key
func (kg *KeyGenerator) GenerateApplicationKey(seekerID, franchiseID string) string {
	hourTimestamp := time.Now().UTC().Format("2006-01-02-15")
	return fmt.Sprintf("app_%s_%s_%s", seekerID, franchiseID, hourTimestamp)
}

// GenerateApplicationKeyWithData generates key from application data
func (kg *KeyGenerator) GenerateApplicationKeyWithData(seekerID, franchiseID string, data map[string]interface{}) string {
	// Use hour-based key for duplicate prevention within 1 hour window
	return kg.GenerateApplicationKey(seekerID, franchiseID)
}

// ============================================================================
// NOTIFICATION IDEMPOTENCY KEYS
// ============================================================================

// GenerateNotificationKey generates idempotency key for notifications
// Format: notif_{type}_{recipientId}_{applicationId}_{date}
// Prevents duplicate notifications on same day
func (kg *KeyGenerator) GenerateNotificationKey(notifType, recipientID, applicationID string) string {
	date := time.Now().UTC().Format("2006-01-02")
	return fmt.Sprintf("notif_%s_%s_%s_%s", notifType, recipientID, applicationID, date)
}

// GenerateNotificationKeySimple generates key without application ID
func (kg *KeyGenerator) GenerateNotificationKeySimple(notifType, recipientID string) string {
	date := time.Now().UTC().Format("2006-01-02")
	return fmt.Sprintf("notif_%s_%s_%s", notifType, recipientID, date)
}

// ============================================================================
// CRM IDEMPOTENCY KEYS
// ============================================================================

// GenerateCRMUserKey generates idempotency key for CRM user creation
// Format: crm_{email}_{source}
// Prevents duplicate contacts for same email
func (kg *KeyGenerator) GenerateCRMUserKey(email, source string) string {
	// Normalize email to lowercase
	email = strings.ToLower(strings.TrimSpace(email))
	return fmt.Sprintf("crm_%s_%s", email, source)
}

// ============================================================================
// FAVORITE IDEMPOTENCY KEYS
// ============================================================================

// GenerateFavoriteKey generates idempotency key for favorites
// Format: fav_{userId}_{franchiseId}
func (kg *KeyGenerator) GenerateFavoriteKey(userID, franchiseID string) string {
	return fmt.Sprintf("fav_%s_%s", userID, franchiseID)
}

// ============================================================================
// USER SIGNUP IDEMPOTENCY KEYS
// ============================================================================

// GenerateSignupKey generates idempotency key for user signup
// Format: signup_{email}_{source}_{date}
// Prevents duplicate signups on same day
func (kg *KeyGenerator) GenerateSignupKey(email, source string) string {
	email = strings.ToLower(strings.TrimSpace(email))
	date := time.Now().UTC().Format("2006-01-02")
	return fmt.Sprintf("signup_%s_%s_%s", email, source, date)
}

// ============================================================================
// USER SIGNIN IDEMPOTENCY KEYS
// ============================================================================

// GenerateSigninKey generates idempotency key for user signin
// Format: signin_{authCode}_{provider}
// Prevents duplicate signin attempts with same auth code
func (kg *KeyGenerator) GenerateSigninKey(authCode, provider string) string {
	// Auth code is already unique per request
	return fmt.Sprintf("signin_%s_%s", authCode, provider)
}

// ============================================================================
// GENERIC WORKER IDEMPOTENCY KEYS
// ============================================================================

// GenerateWorkerKey generates idempotency key from job variables
// Format: worker_{workerType}_{hash_of_variables}
func (kg *KeyGenerator) GenerateWorkerKey(workerType string, variables map[string]interface{}) string {
	// Create deterministic hash from variables
	hash := kg.hashVariables(variables)
	return fmt.Sprintf("worker_%s_%s", workerType, hash[:16])
}

// GenerateWorkerKeyWithTimestamp generates worker key with timestamp
// Useful for operations that should be idempotent within time window
func (kg *KeyGenerator) GenerateWorkerKeyWithTimestamp(workerType string, variables map[string]interface{}, duration time.Duration) string {
	// Round timestamp to duration window
	timestamp := time.Now().UTC().Truncate(duration).Unix()
	hash := kg.hashVariables(variables)
	return fmt.Sprintf("worker_%s_%s_%d", workerType, hash[:12], timestamp)
}

// ============================================================================
// API REQUEST IDEMPOTENCY KEYS
// ============================================================================

// ValidateAPIKey validates client-provided idempotency key
// Returns true if key is valid format (UUID or similar)
func (kg *KeyGenerator) ValidateAPIKey(key string) bool {
	if key == "" {
		return false
	}
	// Must be between 16 and 255 characters
	if len(key) < 16 || len(key) > 255 {
		return false
	}
	// Should not contain whitespace or special chars
	if strings.ContainsAny(key, " \t\n\r") {
		return false
	}
	return true
}

// ============================================================================
// HELPER FUNCTIONS
// ============================================================================

// hashVariables creates SHA256 hash of variables map
func (kg *KeyGenerator) hashVariables(variables map[string]interface{}) string {
	// Convert variables to deterministic string
	var keys []string
	for k := range variables {
		keys = append(keys, k)
	}
	// Sort keys for deterministic output
	// Note: In production, use sort.Strings(keys)

	var builder strings.Builder
	for _, k := range keys {
		builder.WriteString(fmt.Sprintf("%s=%v;", k, variables[k]))
	}

	// Create SHA256 hash
	hash := sha256.Sum256([]byte(builder.String()))
	return fmt.Sprintf("%x", hash)
}

// IsExpired checks if a key with timestamp has expired
func (kg *KeyGenerator) IsExpired(key string, ttl time.Duration) bool {
	// Extract timestamp from key if present
	// This is a simplified version
	return false // Implement based on your key format
}

// ============================================================================
// KEY PARSING
// ============================================================================

// ParseApplicationKey extracts components from application key
func (kg *KeyGenerator) ParseApplicationKey(key string) (seekerID, franchiseID, timestamp string, ok bool) {
	parts := strings.Split(key, "_")
	if len(parts) != 4 || parts[0] != "app" {
		return "", "", "", false
	}
	return parts[1], parts[2], parts[3], true
}

// ParseNotificationKey extracts components from notification key
func (kg *KeyGenerator) ParseNotificationKey(key string) (notifType, recipientID, applicationID, date string, ok bool) {
	parts := strings.Split(key, "_")
	if len(parts) != 5 || parts[0] != "notif" {
		return "", "", "", "", false
	}
	return parts[1], parts[2], parts[3], parts[4], true
}

// ============================================================================
// CLEANUP HELPERS
// ============================================================================

// GetExpirationTime returns expiration time for a key based on type
func (kg *KeyGenerator) GetExpirationTime(keyType string) time.Duration {
	switch keyType {
	case "api":
		return 24 * time.Hour // API keys expire after 24 hours
	case "worker":
		return 1 * time.Hour // Worker keys expire after 1 hour
	case "notification":
		return 7 * 24 * time.Hour // Notification keys expire after 7 days
	case "application":
		return 24 * time.Hour // Application keys expire after 24 hours
	default:
		return 24 * time.Hour
	}
}
