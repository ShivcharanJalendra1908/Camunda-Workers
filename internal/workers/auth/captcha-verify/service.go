package captchaverify

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"camunda-workers/internal/common/errors"
	"camunda-workers/internal/common/logger"

	"github.com/go-redis/redis/v8"
)

type Service struct {
	config      *Config
	logger      logger.Logger
	redisClient *redis.Client
}

type CaptchaData struct {
	Value       string    `json:"value"`
	CreatedAt   time.Time `json:"createdAt"`
	Attempts    int       `json:"attempts"`
	MaxAttempts int       `json:"maxAttempts"`
	ClientIP    string    `json:"clientIp"`
	ExpiresAt   time.Time `json:"expiresAt"`
	Used        bool      `json:"used"`
}

func NewService(deps ServiceDependencies, config *Config) *Service {
	var redisClient *redis.Client
	if config.RedisHost != "" {
		redisClient = redis.NewClient(&redis.Options{
			Addr:     fmt.Sprintf("%s:%d", config.RedisHost, config.RedisPort),
			Password: config.RedisPassword,
			DB:       config.RedisDB,
		})
	}

	return &Service{
		config:      config,
		logger:      deps.Logger,
		redisClient: redisClient,
	}
}

func (s *Service) Execute(ctx context.Context, input *Input) (*Output, error) {
	s.logger.Info("Executing captcha verification", map[string]interface{}{
		"captchaId": input.CaptchaID,
		"clientIp":  input.ClientIP,
	})

	// Check if Redis is configured
	if s.redisClient == nil {
		return nil, &errors.StandardError{
			Code:      "REDIS_NOT_CONFIGURED",
			Message:   "Captcha service requires Redis configuration",
			Details:   "Redis client is not initialized",
			Retryable: false,
			Timestamp: time.Now(),
		}
	}

	// Validate captcha ID format
	if !strings.HasPrefix(input.CaptchaID, "cap_") {
		return &Output{
			Valid:   false,
			Message: "Invalid captcha ID format",
			Reason:  "INVALID_FORMAT",
		}, nil
	}

	// Get captcha from Redis
	captchaData, err := s.getCaptcha(ctx, input.CaptchaID)
	if err != nil {
		return &Output{
			Valid:   false,
			Message: "Captcha not found or expired",
			Reason:  "NOT_FOUND",
		}, nil
	}

	// Check if captcha is expired
	if time.Now().After(captchaData.ExpiresAt) {
		s.deleteCaptcha(ctx, input.CaptchaID)
		return &Output{
			Valid:   false,
			Message: "Captcha has expired",
			Reason:  "EXPIRED",
		}, nil
	}

	// Check if captcha has already been used
	if captchaData.Used {
		return &Output{
			Valid:   false,
			Message: "Captcha has already been used",
			Reason:  "ALREADY_USED",
		}, nil
	}

	// Check attempts
	if captchaData.Attempts >= captchaData.MaxAttempts {
		s.deleteCaptcha(ctx, input.CaptchaID)
		return &Output{
			Valid:             false,
			Message:           "Maximum verification attempts exceeded",
			Reason:            "MAX_ATTEMPTS_EXCEEDED",
			AttemptsRemaining: 0,
		}, nil
	}

	// Verify client IP matches (optional security check)
	if s.config.VerifyClientIP && captchaData.ClientIP != "" && captchaData.ClientIP != input.ClientIP {
		s.incrementAttempts(ctx, input.CaptchaID, captchaData)
		return &Output{
			Valid:             false,
			Message:           "Client IP mismatch",
			Reason:            "IP_MISMATCH",
			AttemptsRemaining: captchaData.MaxAttempts - captchaData.Attempts - 1,
		}, nil
	}

	// Verify captcha value (case-insensitive)
	inputValue := strings.ToUpper(strings.TrimSpace(input.CaptchaValue))
	storedValue := strings.ToUpper(captchaData.Value)

	if inputValue != storedValue {
		s.incrementAttempts(ctx, input.CaptchaID, captchaData)
		attemptsLeft := captchaData.MaxAttempts - captchaData.Attempts - 1

		if attemptsLeft <= 0 {
			s.deleteCaptcha(ctx, input.CaptchaID)
		}

		return &Output{
			Valid:             false,
			Message:           "Incorrect captcha value",
			Reason:            "INCORRECT_VALUE",
			AttemptsRemaining: attemptsLeft,
		}, nil
	}

	// Mark captcha as used
	err = s.markUsed(ctx, input.CaptchaID, captchaData)
	if err != nil {
		s.logger.Warn("Failed to mark captcha as used", map[string]interface{}{
			"captchaId": input.CaptchaID,
			"error":     err.Error(),
		})
	}

	s.logger.Info("Captcha verification successful", map[string]interface{}{
		"captchaId": input.CaptchaID,
		"clientIp":  input.ClientIP,
	})

	return &Output{
		Valid:   true,
		Message: "Captcha verified successfully",
		Reason:  "SUCCESS",
	}, nil
}

// Redis operations

func (s *Service) getCaptcha(ctx context.Context, id string) (*CaptchaData, error) {
	key := fmt.Sprintf("captcha:%s", id)
	data, err := s.redisClient.Get(ctx, key).Result()
	if err != nil {
		if err == redis.Nil {
			return nil, fmt.Errorf("captcha not found")
		}
		return nil, fmt.Errorf("failed to get captcha: %w", err)
	}

	var captcha CaptchaData
	if err := json.Unmarshal([]byte(data), &captcha); err != nil {
		return nil, fmt.Errorf("failed to unmarshal captcha: %w", err)
	}

	return &captcha, nil
}

func (s *Service) saveCaptcha(ctx context.Context, id string, captcha *CaptchaData) error {
	key := fmt.Sprintf("captcha:%s", id)
	data, err := json.Marshal(captcha)
	if err != nil {
		return fmt.Errorf("failed to marshal captcha: %w", err)
	}

	// Calculate TTL based on expiry
	ttl := time.Until(captcha.ExpiresAt)
	if ttl <= 0 {
		ttl = time.Duration(s.config.ExpiryMinutes) * time.Minute
	}

	err = s.redisClient.Set(ctx, key, data, ttl).Err()
	if err != nil {
		return fmt.Errorf("failed to save captcha: %w", err)
	}

	return nil
}

func (s *Service) deleteCaptcha(ctx context.Context, id string) {
	key := fmt.Sprintf("captcha:%s", id)
	s.redisClient.Del(ctx, key)
}

func (s *Service) incrementAttempts(ctx context.Context, id string, captcha *CaptchaData) {
	captcha.Attempts++
	err := s.saveCaptcha(ctx, id, captcha)
	if err != nil {
		s.logger.Warn("Failed to increment attempts", map[string]interface{}{
			"captchaId": id,
			"error":     err.Error(),
		})
	}
}

func (s *Service) markUsed(ctx context.Context, id string, captcha *CaptchaData) error {
	captcha.Used = true
	return s.saveCaptcha(ctx, id, captcha)
}

// CreateCaptcha creates a new captcha for testing or API usage
func (s *Service) CreateCaptcha(ctx context.Context, id, value, clientIP string) error {
	if s.redisClient == nil {
		return fmt.Errorf("redis client not configured")
	}

	data := &CaptchaData{
		Value:       value,
		CreatedAt:   time.Now(),
		Attempts:    0,
		MaxAttempts: s.config.MaxAttempts,
		ClientIP:    clientIP,
		ExpiresAt:   time.Now().Add(time.Duration(s.config.ExpiryMinutes) * time.Minute),
		Used:        false,
	}

	return s.saveCaptcha(ctx, id, data)
}

// TestConnection tests the Redis connection
func (s *Service) TestConnection(ctx context.Context) error {
	if s.redisClient == nil {
		return fmt.Errorf("redis client not configured")
	}

	testCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	_, err := s.redisClient.Ping(testCtx).Result()
	if err != nil {
		return fmt.Errorf("redis connection failed: %w", err)
	}

	return nil
}

