// scripts/generate-jwt.go
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Claims represents JWT token claims
type Claims struct {
	UserID           string   `json:"userId"`
	Email            string   `json:"email"`
	SessionID        string   `json:"sessionId"`
	SourceSystem     string   `json:"sourceSystem"`
	Roles            []string `json:"roles"`
	SubscriptionTier string   `json:"subscriptionTier"`
	jwt.RegisteredClaims
}

func main() {
	// Command line flags
	userID := flag.String("userId", "test-user-123", "User ID")
	email := flag.String("email", "test@example.com", "Email address")
	sessionID := flag.String("sessionId", "sess-"+generateRandomID(), "Session ID")
	sourceSystem := flag.String("sourceSystem", "web-app", "Source system")
	rolesStr := flag.String("roles", "user", "Comma-separated roles (e.g., user,admin)")
	subscriptionTier := flag.String("subscriptionTier", "premium", "Subscription tier")
	expiryHours := flag.Int("expiryHours", 24, "Token expiry in hours")
	secret := flag.String("secret", os.Getenv("JWT_SECRET"), "JWT secret (or set JWT_SECRET env var)")
	verbose := flag.Bool("verbose", false, "Show detailed token information")

	flag.Parse()

	// Validate secret
	if *secret == "" {
		fmt.Println("Error: JWT secret is required")
		fmt.Println("Set JWT_SECRET environment variable or use -secret flag")
		os.Exit(1)
	}

	if len(*secret) < 32 {
		fmt.Println("Warning: JWT secret should be at least 32 characters long")
	}

	// Parse roles
	roles := strings.Split(*rolesStr, ",")
	for i := range roles {
		roles[i] = strings.TrimSpace(roles[i])
	}

	// Create claims
	now := time.Now()
	claims := Claims{
		UserID:           *userID,
		Email:            *email,
		SessionID:        *sessionID,
		SourceSystem:     *sourceSystem,
		Roles:            roles,
		SubscriptionTier: *subscriptionTier,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(now.Add(time.Hour * time.Duration(*expiryHours))),
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			Issuer:    "lemici-platform",
			Subject:   *userID,
		},
	}

	// Create token
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenString, err := token.SignedString([]byte(*secret))
	if err != nil {
		fmt.Printf("Error generating token: %v\n", err)
		os.Exit(1)
	}

	// Print token
	if *verbose {
		fmt.Println("=== JWT Token Generated ===")
		fmt.Printf("User ID:           %s\n", *userID)
		fmt.Printf("Email:             %s\n", *email)
		fmt.Printf("Session ID:        %s\n", *sessionID)
		fmt.Printf("Source System:     %s\n", *sourceSystem)
		fmt.Printf("Roles:             %v\n", roles)
		fmt.Printf("Subscription Tier: %s\n", *subscriptionTier)
		fmt.Printf("Issued At:         %s\n", now.Format(time.RFC3339))
		fmt.Printf("Expires At:        %s\n", now.Add(time.Hour*time.Duration(*expiryHours)).Format(time.RFC3339))
		fmt.Printf("Valid For:         %d hours\n", *expiryHours)
		fmt.Println("\n=== Token ===")
		fmt.Println(tokenString)
		fmt.Println("\n=== Usage ===")
		fmt.Printf("export TOKEN=\"%s\"\n", tokenString)
		fmt.Println("\ncurl -X POST http://localhost:8080/api/v1/ai/query \\")
		fmt.Println("  -H \"Authorization: Bearer $TOKEN\" \\")
		fmt.Println("  -H \"Content-Type: application/json\" \\")
		fmt.Println("  -d '{\"question\":\"test\"}'")
	} else {
		// Just print the token for easy export
		fmt.Println(tokenString)
	}
}

func generateRandomID() string {
	return fmt.Sprintf("%d", time.Now().UnixNano()%1000000)
}