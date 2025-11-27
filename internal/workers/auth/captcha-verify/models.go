package captchaverify

import (
	"camunda-workers/internal/common/logger"
)

type Input struct {
	CaptchaID    string                 `json:"captchaId"`
	CaptchaValue string                 `json:"captchaValue"`
	ClientIP     string                 `json:"clientIp"`
	UserAgent    string                 `json:"userAgent"`
	SessionID    string                 `json:"sessionId,omitempty"`
	Metadata     map[string]interface{} `json:"metadata,omitempty"`
}

type Output struct {
	Valid             bool   `json:"valid"`
	Message           string `json:"message"`
	Reason            string `json:"reason,omitempty"`
	AttemptsRemaining int    `json:"attemptsRemaining,omitempty"`
}

type ServiceDependencies struct {
	Logger logger.Logger
}
