package captchaverify

import (
	"context"
	"strings"
	"sync"
	"time"

	"camunda-workers/internal/common/logger"
)

type Service struct {
	config       *Config
	logger       logger.Logger
	captchaStore *CaptchaStore
}

type CaptchaStore struct {
	mu       sync.RWMutex
	captchas map[string]*CaptchaData
}

type CaptchaData struct {
	Value       string
	CreatedAt   time.Time
	Attempts    int
	MaxAttempts int
	ClientIP    string
	ExpiresAt   time.Time
	Used        bool
}

func NewService(deps ServiceDependencies, config *Config) *Service {
	store := &CaptchaStore{
		captchas: make(map[string]*CaptchaData),
	}

	service := &Service{
		config:       config,
		logger:       deps.Logger,
		captchaStore: store,
	}

	// Start cleanup routine
	go service.cleanupExpiredCaptchas()

	return service
}

func (s *Service) Execute(ctx context.Context, input *Input) (*Output, error) {
	s.logger.Info("Executing captcha verification", map[string]interface{}{
		"captchaId": input.CaptchaID,
		"clientIp":  input.ClientIP,
	})

	// Validate captcha ID format
	if !strings.HasPrefix(input.CaptchaID, "cap_") {
		return &Output{
			Valid:   false,
			Message: "Invalid captcha ID format",
			Reason:  "INVALID_FORMAT",
		}, nil
	}

	// Get captcha from store
	captchaData := s.captchaStore.Get(input.CaptchaID)
	if captchaData == nil {
		return &Output{
			Valid:   false,
			Message: "Captcha not found or expired",
			Reason:  "NOT_FOUND",
		}, nil
	}

	// Check if captcha is expired
	if time.Now().After(captchaData.ExpiresAt) {
		s.captchaStore.Delete(input.CaptchaID)
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
		s.captchaStore.Delete(input.CaptchaID)
		return &Output{
			Valid:             false,
			Message:           "Maximum verification attempts exceeded",
			Reason:            "MAX_ATTEMPTS_EXCEEDED",
			AttemptsRemaining: 0,
		}, nil
	}

	// Verify client IP matches (optional security check)
	if s.config.VerifyClientIP && captchaData.ClientIP != "" && captchaData.ClientIP != input.ClientIP {
		s.captchaStore.IncrementAttempts(input.CaptchaID)
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
		s.captchaStore.IncrementAttempts(input.CaptchaID)
		attemptsLeft := captchaData.MaxAttempts - captchaData.Attempts - 1

		if attemptsLeft <= 0 {
			s.captchaStore.Delete(input.CaptchaID)
		}

		return &Output{
			Valid:             false,
			Message:           "Incorrect captcha value",
			Reason:            "INCORRECT_VALUE",
			AttemptsRemaining: attemptsLeft,
		}, nil
	}

	// Mark captcha as used
	s.captchaStore.MarkUsed(input.CaptchaID)

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

// CaptchaStore methods
func (cs *CaptchaStore) Get(id string) *CaptchaData {
	cs.mu.RLock()
	defer cs.mu.RUnlock()
	return cs.captchas[id]
}

func (cs *CaptchaStore) Delete(id string) {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	delete(cs.captchas, id)
}

func (cs *CaptchaStore) IncrementAttempts(id string) {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	if captcha, exists := cs.captchas[id]; exists {
		captcha.Attempts++
	}
}

func (cs *CaptchaStore) MarkUsed(id string) {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	if captcha, exists := cs.captchas[id]; exists {
		captcha.Used = true
	}
}

func (cs *CaptchaStore) Set(id string, data *CaptchaData) {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	cs.captchas[id] = data
}

func (s *Service) cleanupExpiredCaptchas() {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		s.captchaStore.mu.Lock()
		now := time.Now()
		for id, captcha := range s.captchaStore.captchas {
			if now.After(captcha.ExpiresAt) {
				delete(s.captchaStore.captchas, id)
				s.logger.Debug("Cleaned up expired captcha", map[string]interface{}{
					"captchaId": id,
				})
			}
		}
		s.captchaStore.mu.Unlock()
	}
}

// Helper method for testing - create captcha
func (s *Service) CreateCaptcha(id, value, clientIP string, expiryMinutes int) {
	data := &CaptchaData{
		Value:       value,
		CreatedAt:   time.Now(),
		Attempts:    0,
		MaxAttempts: s.config.MaxAttempts,
		ClientIP:    clientIP,
		ExpiresAt:   time.Now().Add(time.Duration(expiryMinutes) * time.Minute),
		Used:        false,
	}
	s.captchaStore.Set(id, data)
}
