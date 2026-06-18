package keycloaksignin

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"

	cerrors "camunda-workers/internal/common/errors"
	"camunda-workers/internal/common/logger"

	"camunda-workers/internal/common/auth/provider/keycloak"
	"camunda-workers/internal/common/auth/resolver"

	"github.com/redis/go-redis/v9"
)

type ServiceDependencies struct {
	KeycloakProvider *keycloak.Provider
	Resolver         resolver.Resolver
	Redis            *redis.Client
	Logger           logger.Logger
}

type Service struct {
	config   *Config
	keycloak *keycloak.Provider
	resolver resolver.Resolver
	redis    *redis.Client
	logger   logger.Logger
}

func NewService(deps ServiceDependencies, config *Config) *Service {
	return &Service{
		config:   config,
		keycloak: deps.KeycloakProvider,
		resolver: deps.Resolver,
		redis:    deps.Redis,
		logger:   deps.Logger,
	}
}

func (s *Service) Execute(ctx context.Context, input *Input) (*Output, error) {
	switch input.Action {
	case "initiate":
		return s.handleInitiate(ctx, input)
	case "callback":
		return s.handleCallback(ctx, input)
	default:
		return nil, &cerrors.StandardError{
			Code:      "INVALID_ACTION",
			Message:   fmt.Sprintf("Unknown action: %s", input.Action),
			Retryable: false,
			Timestamp: time.Now(),
		}
	}
}

func (s *Service) handleInitiate(ctx context.Context, input *Input) (*Output, error) {
	// 1. Generate state
	state, err := generateState()
	if err != nil {
		return nil, &cerrors.StandardError{
			Code:      "STATE_GENERATION_FAILED",
			Message:   "Failed to generate OAuth state",
			Details:   err.Error(),
			Retryable: true,
			Timestamp: time.Now(),
		}
	}

	// 2. Generate PKCE
	pkce, err := generatePKCE()
	if err != nil {
		return nil, &cerrors.StandardError{
			Code:      "PKCE_GENERATION_FAILED",
			Message:   "Failed to generate PKCE challenge",
			Details:   err.Error(),
			Retryable: true,
			Timestamp: time.Now(),
		}
	}

	// 3. Validate redirectUrl at initiation time (reject early before OAuth flow)
	if input.RedirectURL != "" && !isAllowedRedirectDomain(input.RedirectURL, s.config.AllowedRedirectDomains) {
		return nil, &cerrors.StandardError{
			Code:      "INVALID_REDIRECT_URL",
			Message:   "The specified redirect URL is not allowed",
			Details:   fmt.Sprintf("redirectUrl=%s", input.RedirectURL),
			Retryable: false,
			Timestamp: time.Now(),
		}
	}

	// 4. Store state + PKCE verifier + redirectUrl in Redis
	stateKey := fmt.Sprintf("oauth:state:%s", state)
	session := PKCESession{
		Verifier: pkce.Verifier,
	}
	if input.RedirectURL != "" {
		session.RedirectURL = input.RedirectURL
	}
	sessionJSON, err := json.Marshal(session)
	if err != nil {
		return nil, &cerrors.StandardError{
			Code:      "STATE_STORAGE_FAILED",
			Message:   "Failed to serialize OAuth session",
			Details:   err.Error(),
			Retryable: true,
			Timestamp: time.Now(),
		}
	}
	if err := s.redis.Set(ctx, stateKey, sessionJSON, s.config.StateTTL).Err(); err != nil {
		// Fail-open: losing the redirect destination is a UX degradation,
		// not a correctness failure. Log WARN and proceed with static default.
		s.logger.Warn("Failed to store OAuth state with redirectUrl, proceeding without it", map[string]interface{}{
			"state":       state,
			"redirectUrl": input.RedirectURL,
			"error":       err.Error(),
		})
		// Fall back to plain verifier-only write
		if fallbackErr := s.redis.Set(ctx, stateKey, pkce.Verifier, s.config.StateTTL).Err(); fallbackErr != nil {
			return nil, &cerrors.StandardError{
				Code:      "STATE_STORAGE_FAILED",
				Message:   "Failed to store OAuth state",
				Details:   fallbackErr.Error(),
				Retryable: true,
				Timestamp: time.Now(),
			}
		}
	}

	// 5. Build authorization URL
	authURL := s.keycloak.AuthCodeURL(state, pkce.Challenge)

	s.logger.Info("OAuth initiate successful", map[string]interface{}{
		"state":       state,
		"authUrl":     authURL,
		"hasRedirect": input.RedirectURL != "",
	})

	return &Output{
		AuthorizationURL: authURL,
		State:            state,
	}, nil
}

func (s *Service) handleCallback(ctx context.Context, input *Input) (*Output, error) {
	// 1. Retrieve and validate state
	// stateKey := fmt.Sprintf("oauth:state:%s", input.State)
	// verifier, err := s.redis.Get(ctx, stateKey).Result()
	// if err != nil {
	// 	return nil, &cerrors.StandardError{
	// 		Code:      "INVALID_STATE",
	// 		Message:   "Invalid or expired OAuth state",
	// 		Details:   err.Error(),
	// 		Retryable: false,
	// 		Timestamp: time.Now(),
	// 	}
	// }
	// LAGAO YEH
	stateKey := fmt.Sprintf("oauth:state:%s", input.State)
	atomicGetDel := redis.NewScript(`
    local val = redis.call('GET', KEYS[1])
    if val == false then
        return nil
    end
    redis.call('DEL', KEYS[1])
    return val
`)
	sessionRaw, err := atomicGetDel.Run(ctx, s.redis, []string{stateKey}).Text()
	if err != nil {
		return nil, &cerrors.StandardError{
			Code:      "INVALID_STATE",
			Message:   "Invalid or expired OAuth state",
			Details:   err.Error(),
			Retryable: false,
			Timestamp: time.Now(),
		}
	}

	// Parse PKCE session: try structured JSON first, fall back to plain verifier
	var verifier string
	var pkceSession PKCESession
	if err := json.Unmarshal([]byte(sessionRaw), &pkceSession); err == nil && pkceSession.Verifier != "" {
		verifier = pkceSession.Verifier
	} else {
		// Backward compatibility: old entries stored the verifier as a plain string
		verifier = sessionRaw
	}

	// 3. Exchange authorization code for tokens
	identity, err := s.keycloak.ExchangeCode(ctx, input.Code, verifier)
	if err != nil {
		s.logger.Error("Token exchange error details", map[string]interface{}{
			"error":    err.Error(),
			"code":     input.Code[:20],
			"verifier": verifier[:10],
		})
		return nil, &cerrors.StandardError{
			Code:      "TOKEN_EXCHANGE_FAILED",
			Message:   "Failed to exchange authorization code",
			Details:   err.Error(),
			Retryable: false,
			Timestamp: time.Now(),
		}
	}

	s.logger.Info("Token exchange successful", map[string]interface{}{
		"provider":      identity.Provider,
		"email":         identity.Email,
		"emailVerified": identity.EmailVerified,
	})

	// 4. Resolve user (create if new)
	userID, err := s.resolver.Resolve(ctx, identity)
	if err != nil {
		return nil, &cerrors.StandardError{
			Code:      "USER_RESOLUTION_FAILED",
			Message:   "Failed to resolve user identity",
			Details:   err.Error(),
			Retryable: true,
			Timestamp: time.Now(),
		}
	}

	// TODO: Determine if user is new (check if just created)
	// For now, we'll check if this is first time seeing this provider_user_id
	isNewUser := false // You can enhance this logic

	return &Output{
		Success:         true,
		UserID:          userID,
		Email:           identity.Email,
		EmailVerified:   identity.EmailVerified,
		IsNewUser:       isNewUser,
		KeycloakUserID:  identity.ProviderUserID,
		IDToken:         identity.IDToken,
		AccessToken:     identity.AccessToken,
		RefreshToken:    identity.RefreshToken,
		AuthenticatedAt: time.Now(),
	}, nil
}

func (s *Service) TestConnection(ctx context.Context) error {
	return s.redis.Ping(ctx).Err()
}

// PKCE Helper Functions
func generateState() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func generatePKCE() (*PKCEData, error) {
	// Generate verifier
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return nil, err
	}
	verifier := base64.RawURLEncoding.EncodeToString(b)

	// Generate challenge
	hash := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(hash[:])

	return &PKCEData{
		Verifier:  verifier,
		Challenge: challenge,
	}, nil
}

func isAllowedRedirectDomain(rawURL string, allowed []string) bool {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return false
	}

	hostname := parsed.Hostname()

	if parsed.Scheme != "https" && hostname != "localhost" {
		return false
	}

	for _, domain := range allowed {
		if hostname == domain || strings.HasSuffix(hostname, "."+domain) {
			return true
		}
	}
	return false
}
