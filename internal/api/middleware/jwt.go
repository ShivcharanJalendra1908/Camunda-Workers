// internal/api/middleware/jwt.go
// Complete JWT authentication package with middleware, validation, and utilities

package middleware

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"camunda-workers/internal/common/config"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

// ============================================================================
// CONTEXT KEY TYPE
// ============================================================================

// contextKey is a custom type for context keys to avoid collisions
type contextKey string

// Context keys for storing values in context
const (
	claimsKey contextKey = "claims"
)

// ============================================================================
// CLAIMS STRUCTURE
// ============================================================================

// Claims represents the structure of JWT token claims
type Claims struct {
	UserID           string   `json:"userId"`
	Email            string   `json:"email"`
	SessionID        string   `json:"sessionId"`
	SourceSystem     string   `json:"sourceSystem"`
	Roles            []string `json:"roles"`
	SubscriptionTier string   `json:"subscriptionTier"`
	jwt.RegisteredClaims
}

// Valid implements the jwt.Claims interface for custom validation
func (c Claims) Valid() error {
	// Check if token is expired
	if c.ExpiresAt != nil && c.ExpiresAt.Time.Before(time.Now()) {
		return fmt.Errorf("token has expired")
	}

	// Check if token is being used before valid time
	if c.NotBefore != nil && c.NotBefore.Time.After(time.Now()) {
		return fmt.Errorf("token is not yet valid")
	}

	// Validate required fields
	if c.UserID == "" {
		return fmt.Errorf("userId claim is required")
	}
	if c.SessionID == "" {
		return fmt.Errorf("sessionId claim is required")
	}

	return nil
}

// ============================================================================
// JWT SERVICE
// ============================================================================

// JWTService handles JWT token operations
type JWTService struct {
	secret           []byte
	issuer           string
	expiryHours      int
	refreshExpiryHrs int
}

// NewJWTService creates a new JWT service
func NewJWTService(jwtConfig config.JWTConfig) *JWTService {
	return &JWTService{
		secret:           []byte(jwtConfig.Secret),
		issuer:           jwtConfig.Issuer,
		expiryHours:      jwtConfig.ExpiryHours,
		refreshExpiryHrs: jwtConfig.RefreshExpiryHours,
	}
}

// GenerateToken creates a new JWT token
func (s *JWTService) GenerateToken(userID, email, sessionID, sourceSystem string, roles []string, subscriptionTier string) (string, error) {
	now := time.Now()
	claims := Claims{
		UserID:           userID,
		Email:            email,
		SessionID:        sessionID,
		SourceSystem:     sourceSystem,
		Roles:            roles,
		SubscriptionTier: subscriptionTier,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(now.Add(time.Hour * time.Duration(s.expiryHours))),
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			Issuer:    s.issuer,
			Subject:   userID,
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(s.secret)
}

// GenerateRefreshToken creates a refresh token with longer expiry
func (s *JWTService) GenerateRefreshToken(userID, sessionID string) (string, error) {
	now := time.Now()
	claims := Claims{
		UserID:    userID,
		SessionID: sessionID,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(now.Add(time.Hour * time.Duration(s.refreshExpiryHrs))),
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			Issuer:    s.issuer,
			Subject:   userID,
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(s.secret)
}

// ValidateToken validates and parses a JWT token
func (s *JWTService) ValidateToken(tokenString string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(tokenString, &Claims{}, func(token *jwt.Token) (interface{}, error) {
		// Validate signing method
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return s.secret, nil
	})

	if err != nil {
		return nil, err
	}

	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return nil, fmt.Errorf("invalid token claims")
	}

	return claims, nil
}

// RefreshAccessToken generates a new access token from a refresh token
func (s *JWTService) RefreshAccessToken(refreshToken string) (string, error) {
	claims, err := s.ValidateToken(refreshToken)
	if err != nil {
		return "", fmt.Errorf("invalid refresh token: %w", err)
	}

	// For refresh tokens, we should fetch full user data from database
	// This is a simplified version
	return s.GenerateToken(
		claims.UserID,
		claims.Email,
		claims.SessionID,
		claims.SourceSystem,
		claims.Roles,
		claims.SubscriptionTier,
	)
}

// ============================================================================
// JWT AUTHENTICATION MIDDLEWARE
// ============================================================================

// JWTAuth validates JWT tokens and extracts claims
func JWTAuth(jwtConfig config.JWTConfig) gin.HandlerFunc {
	jwtService := NewJWTService(jwtConfig)

	return func(c *gin.Context) {
		// Extract token from Authorization header
		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			respondWithError(c, http.StatusUnauthorized, "AUTH_MISSING_TOKEN", "missing authorization header", nil)
			c.Abort()
			return
		}

		// Check Bearer prefix
		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) != 2 || parts[0] != "Bearer" {
			respondWithError(c, http.StatusUnauthorized, "AUTH_INVALID_FORMAT", "invalid authorization header format", nil)
			c.Abort()
			return
		}

		tokenString := parts[1]

		// Validate token
		claims, err := jwtService.ValidateToken(tokenString)
		if err != nil {
			respondWithError(c, http.StatusUnauthorized, "AUTH_INVALID_TOKEN", "invalid token", map[string]interface{}{
				"details": err.Error(),
			})
			c.Abort()
			return
		}

		// Store claims in context
		c.Set("claims", claims)
		c.Set("userId", claims.UserID)
		c.Set("email", claims.Email)
		c.Set("sessionId", claims.SessionID)
		c.Set("sourceSystem", claims.SourceSystem)
		c.Set("subscriptionTier", claims.SubscriptionTier)
		c.Set("roles", claims.Roles)

		// Add to request context as well (using custom type to avoid collisions)
		ctx := context.WithValue(c.Request.Context(), claimsKey, claims)
		c.Request = c.Request.WithContext(ctx)

		c.Next()
	}
}

// ============================================================================
// AUTHORIZATION MIDDLEWARE
// ============================================================================

// RequireRole checks if user has required role
func RequireRole(requiredRole string) gin.HandlerFunc {
	return func(c *gin.Context) {
		claims := ExtractClaims(c)
		if claims == nil {
			respondWithError(c, http.StatusUnauthorized, "AUTH_REQUIRED", "authentication required", nil)
			c.Abort()
			return
		}

		if !hasRole(claims.Roles, requiredRole) {
			respondWithError(c, http.StatusForbidden, "AUTH_INSUFFICIENT_PERMISSIONS", "insufficient permissions", map[string]interface{}{
				"required": requiredRole,
				"has":      claims.Roles,
			})
			c.Abort()
			return
		}

		c.Next()
	}
}

// RequireAnyRole checks if user has any of the required roles
func RequireAnyRole(requiredRoles ...string) gin.HandlerFunc {
	return func(c *gin.Context) {
		claims := ExtractClaims(c)
		if claims == nil {
			respondWithError(c, http.StatusUnauthorized, "AUTH_REQUIRED", "authentication required", nil)
			c.Abort()
			return
		}

		hasAnyRole := false
		for _, required := range requiredRoles {
			if hasRole(claims.Roles, required) {
				hasAnyRole = true
				break
			}
		}

		if !hasAnyRole {
			respondWithError(c, http.StatusForbidden, "AUTH_INSUFFICIENT_PERMISSIONS", "insufficient permissions", map[string]interface{}{
				"required_any": requiredRoles,
				"has":          claims.Roles,
			})
			c.Abort()
			return
		}

		c.Next()
	}
}

// RequireAllRoles checks if user has all required roles
func RequireAllRoles(requiredRoles ...string) gin.HandlerFunc {
	return func(c *gin.Context) {
		claims := ExtractClaims(c)
		if claims == nil {
			respondWithError(c, http.StatusUnauthorized, "AUTH_REQUIRED", "authentication required", nil)
			c.Abort()
			return
		}

		for _, required := range requiredRoles {
			if !hasRole(claims.Roles, required) {
				respondWithError(c, http.StatusForbidden, "AUTH_INSUFFICIENT_PERMISSIONS", "insufficient permissions", map[string]interface{}{
					"required_all": requiredRoles,
					"has":          claims.Roles,
					"missing":      required,
				})
				c.Abort()
				return
			}
		}

		c.Next()
	}
}

// RequireSubscriptionTier checks if user has minimum subscription tier
func RequireSubscriptionTier(minTier string) gin.HandlerFunc {
	tierLevels := map[string]int{
		"free":       1,
		"basic":      2,
		"premium":    3,
		"enterprise": 4,
	}

	return func(c *gin.Context) {
		claims := ExtractClaims(c)
		if claims == nil {
			respondWithError(c, http.StatusUnauthorized, "AUTH_REQUIRED", "authentication required", nil)
			c.Abort()
			return
		}

		userLevel, ok := tierLevels[claims.SubscriptionTier]
		if !ok {
			userLevel = 0
		}

		requiredLevel := tierLevels[minTier]

		if userLevel < requiredLevel {
			respondWithError(c, http.StatusForbidden, "SUBSCRIPTION_INSUFFICIENT", "subscription tier insufficient", map[string]interface{}{
				"required": minTier,
				"current":  claims.SubscriptionTier,
			})
			c.Abort()
			return
		}

		c.Next()
	}
}

// ============================================================================
// OPTIONAL JWT MIDDLEWARE (doesn't fail if no token)
// ============================================================================

// OptionalJWTAuth extracts claims if token exists but doesn't fail if missing
func OptionalJWTAuth(jwtConfig config.JWTConfig) gin.HandlerFunc {
	jwtService := NewJWTService(jwtConfig)

	return func(c *gin.Context) {
		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			c.Next()
			return
		}

		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) != 2 || parts[0] != "Bearer" {
			c.Next()
			return
		}

		claims, err := jwtService.ValidateToken(parts[1])
		if err == nil {
			c.Set("claims", claims)
			c.Set("userId", claims.UserID)
			c.Set("email", claims.Email)
			c.Set("sessionId", claims.SessionID)
			c.Set("sourceSystem", claims.SourceSystem)
			c.Set("subscriptionTier", claims.SubscriptionTier)
			c.Set("roles", claims.Roles)
		}

		c.Next()
	}
}

// ============================================================================
// HELPER FUNCTIONS
// ============================================================================

// ExtractClaims retrieves claims from context
func ExtractClaims(c *gin.Context) *Claims {
	claims, exists := c.Get("claims")
	if !exists {
		return nil
	}
	return claims.(*Claims)
}

// GetUserID retrieves user ID from context
func GetUserID(c *gin.Context) string {
	if userID, exists := c.Get("userId"); exists {
		return userID.(string)
	}
	return ""
}

// GetSessionID retrieves session ID from context
func GetSessionID(c *gin.Context) string {
	if sessionID, exists := c.Get("sessionId"); exists {
		return sessionID.(string)
	}
	return ""
}

// GetSourceSystem retrieves source system from context
func GetSourceSystem(c *gin.Context) string {
	if sourceSystem, exists := c.Get("sourceSystem"); exists {
		return sourceSystem.(string)
	}
	return ""
}

// GetRoles retrieves roles from context
func GetRoles(c *gin.Context) []string {
	if roles, exists := c.Get("roles"); exists {
		return roles.([]string)
	}
	return []string{}
}

// HasRole checks if user has a specific role
func HasRole(c *gin.Context, role string) bool {
	claims := ExtractClaims(c)
	if claims == nil {
		return false
	}
	return hasRole(claims.Roles, role)
}

// hasRole checks if role exists in roles slice
func hasRole(roles []string, role string) bool {
	for _, r := range roles {
		if r == role {
			return true
		}
	}
	return false
}

// respondWithError sends standardized error response
func respondWithError(c *gin.Context, status int, code, message string, details interface{}) {
	response := gin.H{
		"error":   message,
		"code":    code,
		"request": c.GetString("requestId"),
	}
	if details != nil {
		response["details"] = details
	}
	c.JSON(status, response)
}

// ============================================================================
// TOKEN BLACKLIST (for logout functionality)
// ============================================================================

// TokenBlacklist interface for implementing token revocation
type TokenBlacklist interface {
	Add(token string, expiry time.Duration) error
	IsBlacklisted(token string) bool
}

// BlacklistMiddleware checks if token is blacklisted
func BlacklistMiddleware(blacklist TokenBlacklist) gin.HandlerFunc {
	return func(c *gin.Context) {
		authHeader := c.GetHeader("Authorization")
		if authHeader != "" {
			parts := strings.SplitN(authHeader, " ", 2)
			if len(parts) == 2 {
				token := parts[1]
				if blacklist.IsBlacklisted(token) {
					respondWithError(c, http.StatusUnauthorized, "AUTH_TOKEN_REVOKED", "token has been revoked", nil)
					c.Abort()
					return
				}
			}
		}
		c.Next()
	}
}
