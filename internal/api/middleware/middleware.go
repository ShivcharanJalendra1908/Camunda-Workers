// internal/api/middleware/middleware.go
package middleware

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	apierrors "camunda-workers/internal/api/errors"
	"camunda-workers/internal/common/config"
	"camunda-workers/internal/common/errors"
	"camunda-workers/internal/common/logger"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
	"golang.org/x/time/rate"
)

// ============================================================================
// REQUEST ID MIDDLEWARE
// ============================================================================

func RequestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		requestID := c.GetHeader("X-Request-ID")
		if requestID == "" {
			requestID = uuid.New().String()
		}
		c.Set("requestId", requestID)
		c.Header("X-Request-ID", requestID)
		c.Next()
	}
}

// ============================================================================
// DISTRIBUTED TRACING MIDDLEWARE
// ============================================================================

func TracingMiddleware() gin.HandlerFunc {
	tracer := otel.Tracer("api-gateway")

	return func(c *gin.Context) {
		ctx := otel.GetTextMapPropagator().Extract(
			c.Request.Context(),
			propagation.HeaderCarrier(c.Request.Header),
		)

		ctx, span := tracer.Start(ctx, c.Request.Method+" "+c.Request.URL.Path,
			trace.WithSpanKind(trace.SpanKindServer),
		)
		defer span.End()

		traceID := span.SpanContext().TraceID().String()
		spanID := span.SpanContext().SpanID().String()

		c.Set("traceId", traceID)
		c.Set("spanId", spanID)
		c.Header("X-Trace-ID", traceID)
		c.Header("X-Span-ID", spanID)

		span.SetAttributes(
			attribute.String("http.method", c.Request.Method),
			attribute.String("http.url", c.Request.URL.String()),
			attribute.String("http.route", c.Request.URL.Path),
			attribute.String("http.client_ip", c.ClientIP()),
			attribute.String("http.user_agent", c.Request.UserAgent()),
		)

		c.Request = c.Request.WithContext(ctx)
		c.Next()

		span.SetAttributes(
			attribute.Int("http.status_code", c.Writer.Status()),
			attribute.Int("http.response_size", c.Writer.Size()),
		)

		if len(c.Errors) > 0 {
			span.RecordError(c.Errors.Last())
			span.SetAttributes(attribute.Bool("error", true))
		}
	}
}

// ============================================================================
// LOGGER MIDDLEWARE
// ============================================================================

func Logger(log logger.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		path := c.Request.URL.Path
		method := c.Request.Method

		c.Next()

		latency := time.Since(start)
		statusCode := c.Writer.Status()
		clientIP := c.ClientIP()
		requestID := c.GetString("requestId")

		logData := map[string]interface{}{
			"method":    method,
			"path":      path,
			"status":    statusCode,
			"latency":   latency.Milliseconds(),
			"clientIP":  clientIP,
			"requestId": requestID,
			"userAgent": c.Request.UserAgent(),
		}

		if statusCode >= 500 {
			if len(c.Errors) > 0 {
				logData["error"] = c.Errors.Last().Error()
			}
			log.Error("API Request Failed", logData)
		} else if statusCode >= 400 {
			log.Warn("API Request Client Error", logData)
		} else {
			log.Info("API Request", logData)
		}
	}
}

// ============================================================================
// CUSTOM RECOVERY MIDDLEWARE
// ============================================================================

func Recovery(log logger.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if err := recover(); err != nil {
				log.Error("Panic recovered", map[string]interface{}{
					"error":     fmt.Sprintf("%v", err),
					"path":      c.Request.URL.Path,
					"method":    c.Request.Method,
					"requestId": c.GetString("requestId"),
				})

				c.JSON(http.StatusInternalServerError, gin.H{
					"success":   false,
					"error":     "Internal server error",
					"message":   "An unexpected error occurred",
					"requestId": c.GetString("requestId"),
				})

				c.Abort()
			}
		}()
		c.Next()
	}
}

// ============================================================================
// CORS MIDDLEWARE
// ============================================================================

func CORS(corsConfig config.CORSConfig) gin.HandlerFunc {
	return func(c *gin.Context) {
		origin := c.Request.Header.Get("Origin")

		originAllowed := false
		if len(corsConfig.AllowOrigins) > 0 {
			cleanOrigin := strings.TrimSuffix(origin, "/")
			for _, allowedOrigin := range corsConfig.AllowOrigins {
				cleanAllowed := strings.TrimSuffix(allowedOrigin, "/")
				if cleanAllowed == cleanOrigin || allowedOrigin == "*" {
					if origin != "" {
						c.Header("Access-Control-Allow-Origin", origin)
					} else if allowedOrigin == "*" {
						c.Header("Access-Control-Allow-Origin", "*")
					}
					originAllowed = true
					break
				}
			}
		}

		// Only set other CORS headers if the origin is allowed
		if originAllowed {
			if len(corsConfig.AllowMethods) > 0 {
				c.Header("Access-Control-Allow-Methods", strings.Join(corsConfig.AllowMethods, ", "))
			}

			if len(corsConfig.AllowHeaders) > 0 {
				c.Header("Access-Control-Allow-Headers", strings.Join(corsConfig.AllowHeaders, ", "))
			}

			if len(corsConfig.ExposeHeaders) > 0 {
				c.Header("Access-Control-Expose-Headers", strings.Join(corsConfig.ExposeHeaders, ", "))
			}

			if corsConfig.AllowCredentials && origin != "" {
				c.Header("Access-Control-Allow-Credentials", "true")
			}

			if corsConfig.MaxAge > 0 {
				c.Header("Access-Control-Max-Age", fmt.Sprintf("%d", corsConfig.MaxAge))
			}
		}

		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}

		c.Next()
	}
}

// ============================================================================
// RATE LIMITER MIDDLEWARE
// ============================================================================

type visitor struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

type RateLimiterImpl struct {
	visitors map[string]*visitor
	mu       sync.RWMutex
	rps      int
	burst    int
}

func NewRateLimiter(rps, burst int) *RateLimiterImpl {
	rl := &RateLimiterImpl{
		visitors: make(map[string]*visitor),
		rps:      rps,
		burst:    burst,
	}

	go rl.cleanupVisitors()

	return rl
}

func (rl *RateLimiterImpl) getVisitor(ip string) *rate.Limiter {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	v, exists := rl.visitors[ip]
	if !exists {
		limiter := rate.NewLimiter(rate.Limit(rl.rps), rl.burst)
		rl.visitors[ip] = &visitor{limiter, time.Now()}
		return limiter
	}

	v.lastSeen = time.Now()
	return v.limiter
}

func (rl *RateLimiterImpl) cleanupVisitors() {
	for {
		time.Sleep(5 * time.Minute)

		rl.mu.Lock()
		for ip, v := range rl.visitors {
			if time.Since(v.lastSeen) > 10*time.Minute {
				delete(rl.visitors, ip)
			}
		}
		rl.mu.Unlock()
	}
}

func RateLimiter(rateLimitConfig config.RateLimitConfig) gin.HandlerFunc {
	if !rateLimitConfig.Enabled {
		return func(c *gin.Context) {
			c.Next()
		}
	}

	limiter := NewRateLimiter(rateLimitConfig.RequestsPerSecond, rateLimitConfig.Burst)

	return func(c *gin.Context) {
		ip := c.ClientIP()

		if !limiter.getVisitor(ip).Allow() {
			c.JSON(http.StatusTooManyRequests, gin.H{
				"success": false,
				"error":   "rate limit exceeded",
				"code":    "RATE_LIMIT_EXCEEDED",
				"message": fmt.Sprintf("Maximum %d requests per second allowed", rateLimitConfig.RequestsPerSecond),
			})
			c.Abort()
			return
		}

		c.Next()
	}
}

// ============================================================================
// SECURITY HEADERS MIDDLEWARE
// ============================================================================

func SecurityHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("X-Frame-Options", "DENY")
		c.Header("X-XSS-Protection", "1; mode=block")
		c.Header("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		c.Header("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline' https://fonts.googleapis.com; script-src 'self' 'unsafe-inline'; font-src 'self' https://fonts.gstatic.com; img-src 'self' data: https://lenici.com;")
		c.Next()
	}
}

// ============================================================================
// TIMEOUT MIDDLEWARE
// ============================================================================

func Timeout(timeout time.Duration) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), timeout)
		defer cancel()

		c.Request = c.Request.WithContext(ctx)
		c.Next()
	}
}

// ============================================================================
// INPUT VALIDATION MIDDLEWARE
// ============================================================================

func InputValidation() gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.Method == "POST" || c.Request.Method == "PUT" || c.Request.Method == "PATCH" {
			contentType := c.GetHeader("Content-Type")
			if !strings.Contains(contentType, "application/json") &&
				!strings.Contains(contentType, "multipart/form-data") {
				c.JSON(http.StatusBadRequest, gin.H{
					"success": false,
					"error":   "Invalid Content-Type",
					"message": "Content-Type must be application/json",
				})
				c.Abort()
				return
			}
		}

		const maxBodySize = 10 * 1024 * 1024
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBodySize)

		if page := c.Query("page"); page != "" {
			if pageNum := parseInt(page); pageNum < 1 || pageNum > 10000 {
				c.JSON(http.StatusBadRequest, gin.H{
					"success": false,
					"error":   "Invalid page parameter",
					"message": "page must be between 1 and 10000",
				})
				c.Abort()
				return
			}
		}

		if limit := c.Query("limit"); limit != "" {
			if limitNum := parseInt(limit); limitNum < 1 || limitNum > 100 {
				c.JSON(http.StatusBadRequest, gin.H{
					"success": false,
					"error":   "Invalid limit parameter",
					"message": "limit must be between 1 and 100",
				})
				c.Abort()
				return
			}
		}

		c.Next()
	}
}

// ============================================================================
// IDEMPOTENCY MIDDLEWARE (FIXED)
// ============================================================================

func IdempotencyMiddleware(redisClient *redis.Client) gin.HandlerFunc {
	return func(c *gin.Context) {
		method := c.Request.Method
		if method == http.MethodGet || method == http.MethodHead || method == http.MethodOptions {
			c.Next()
			return
		}

		idempotencyKey := c.GetHeader("X-Idempotency-Key")

		if idempotencyKey == "" {
			idempotencyKey = uuid.New().String()
			c.Header("X-Idempotency-Key", idempotencyKey)
			c.Next()
			return
		}

		if !validateIdempotencyKey(idempotencyKey) {
			c.JSON(http.StatusBadRequest, gin.H{
				"success":   false,
				"error":     "invalid_idempotency_key",
				"message":   "Idempotency key must be a valid UUID or 16-255 characters",
				"requestId": c.GetString("requestId"),
			})
			c.Abort()
			return
		}

		var bodyBytes []byte
		if c.Request.Body != nil {
			bodyBytes, _ = io.ReadAll(c.Request.Body)
			c.Request.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))
		}

		cacheKey := generateIdempotencyCacheKey(c, idempotencyKey, bodyBytes)
		ctx := c.Request.Context()

		cachedResponse, exists, err := checkCachedResponse(ctx, redisClient, cacheKey)
		if err != nil {
			// Log but continue
			c.Next()
			return
		}

		if exists {
			c.Header("X-Idempotency-Status", "cached")
			c.JSON(http.StatusOK, cachedResponse)
			c.Abort()
			return
		}

		// ✅ FIXED: Proper response capture
		blw := &bodyLogWriter{body: bytes.NewBufferString(""), ResponseWriter: c.Writer}
		c.Writer = blw

		c.Next()

		// Only cache successful responses (2xx)
		if c.Writer.Status() >= 200 && c.Writer.Status() < 300 {
			var responseBody map[string]interface{}
			if err := json.Unmarshal(blw.body.Bytes(), &responseBody); err == nil {
				c.Header("X-Idempotency-Status", "processed")
				c.Header("X-Idempotency-Key", idempotencyKey)

				ttl := getTTLForEndpoint(c.Request.URL.Path)
				go storeCachedResponse(context.Background(), redisClient, cacheKey, responseBody, ttl)
			}
		}
	}
}

// ✅ FIXED: Better response writer
type bodyLogWriter struct {
	gin.ResponseWriter
	body *bytes.Buffer
}

func (w bodyLogWriter) Write(b []byte) (int, error) {
	w.body.Write(b)
	return w.ResponseWriter.Write(b)
}

func (w bodyLogWriter) WriteString(s string) (int, error) {
	w.body.WriteString(s)
	return w.ResponseWriter.WriteString(s)
}

// ============================================================================
// ERROR HANDLER MIDDLEWARE (MUST BE LAST)
// ============================================================================

func ErrorHandler(log logger.Logger, frontendBaseURL string) gin.HandlerFunc {
	// Ensure base URL doesn't end with a slash for clean concatenation
	baseURL := strings.TrimSuffix(frontendBaseURL, "/")

	return func(c *gin.Context) {
		c.Next()

		if len(c.Errors) == 0 || c.Writer.Written() {
			return
		}

		lastErr := c.Errors.Last().Err

		// 1. Handle typed AppError
		if appErr, ok := lastErr.(*apierrors.AppError); ok {
			log.Error("Typed API error", map[string]interface{}{
				"error_code":  appErr.Code,
				"log_message": appErr.LogMessage,
				"path":        c.Request.URL.Path,
				"method":      c.Request.Method,
				"requestId":   c.GetString("requestId"),
			})

			if appErr.RedirectTo != "" {
				clearAuthCookies(c)
				// Ensure RedirectTo starts with a slash
				redirectPath := appErr.RedirectTo
				if !strings.HasPrefix(redirectPath, "/") && !strings.HasPrefix(redirectPath, "?") {
					redirectPath = "/" + redirectPath
				}
				c.Redirect(http.StatusFound, baseURL+redirectPath)
				return
			}

			c.JSON(appErr.StatusCode, gin.H{
				"error":     appErr.Message,
				"code":      appErr.Code,
				"requestId": c.GetString("requestId"),
			})
			return
		}

		// 2. Generic fallback (context-aware)
		log.Error("Untyped error", map[string]interface{}{
			"error":     lastErr.Error(),
			"path":      c.Request.URL.Path,
			"method":    c.Request.Method,
			"requestId": c.GetString("requestId"),
		})

		acceptsJSON := strings.Contains(c.GetHeader("Accept"), "application/json")
		isAuthFlow := strings.HasPrefix(c.Request.URL.Path, "/api/v1/auth") || strings.HasPrefix(c.Request.URL.Path, "/oauth")

		if acceptsJSON || !isAuthFlow {
			c.JSON(http.StatusInternalServerError, gin.H{
				"error":     "An unexpected error occurred. Please try again.",
				"code":      "INTERNAL_ERROR",
				"requestId": c.GetString("requestId"),
			})
			return
		}

		clearAuthCookies(c)
		c.Redirect(http.StatusFound, baseURL+"/login?error=unexpected")
	}
}

// clearAuthCookies clears auth-related cookies on error with proper domain scoping
func clearAuthCookies(c *gin.Context) {
	cookieNames := []string{"pkce_verifier", "oauth_state", "AUTH_SESSION_ID", "session_id"}
	domain := ".lemici.com" // Ensure this matches constants/config

	for _, name := range cookieNames {
		http.SetCookie(c.Writer, &http.Cookie{
			Name:     name,
			Value:    "",
			MaxAge:   -1,
			Path:     "/",
			Domain:   domain,
			HttpOnly: true,
			Secure:   true,
			SameSite: http.SameSiteLaxMode, // Consistent with successful login
		})
	}
}

// ============================================================================
// HELPER FUNCTIONS
// ============================================================================

func joinStrings(strs []string, sep string) string {
	if len(strs) == 0 {
		return ""
	}
	result := strs[0]
	for i := 1; i < len(strs); i++ {
		result += sep + strs[i]
	}
	return result
}

func validateIdempotencyKey(key string) bool {
	_, err := uuid.Parse(key)
	if err == nil {
		return true
	}

	if len(key) < 16 || len(key) > 255 {
		return false
	}

	if strings.ContainsAny(key, " \t\n\r") {
		return false
	}

	return true
}

func generateIdempotencyCacheKey(c *gin.Context, idempotencyKey string, bodyBytes []byte) string {
	method := c.Request.Method
	path := c.Request.URL.Path

	var bodyHash string
	if len(bodyBytes) > 0 {
		hash := sha256.Sum256(bodyBytes)
		bodyHash = hex.EncodeToString(hash[:])
	} else {
		bodyHash = "empty"
	}

	return fmt.Sprintf("idempotency:%s:%s:%s:%s", method, path, idempotencyKey, bodyHash)
}

func checkCachedResponse(ctx context.Context, redisClient *redis.Client, cacheKey string) (map[string]interface{}, bool, error) {
	val, err := redisClient.Get(ctx, cacheKey).Result()
	if err == redis.Nil {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("redis get failed: %w", err)
	}

	var response map[string]interface{}
	if err := json.Unmarshal([]byte(val), &response); err != nil {
		return nil, false, fmt.Errorf("failed to parse cached response: %w", err)
	}

	return response, true, nil
}

func storeCachedResponse(ctx context.Context, redisClient *redis.Client, cacheKey string, response map[string]interface{}, ttl time.Duration) {
	responseJSON, err := json.Marshal(response)
	if err != nil {
		return
	}

	redisClient.Set(ctx, cacheKey, responseJSON, ttl).Err()
}

func getTTLForEndpoint(path string) time.Duration {
	ttl := 24 * time.Hour

	if strings.Contains(path, "/auth/") {
		ttl = 1 * time.Hour
	} else if strings.Contains(path, "/applications/") {
		ttl = 24 * time.Hour
	} else if strings.Contains(path, "/crm/") {
		ttl = 7 * 24 * time.Hour
	}

	return ttl
}

func GetIdempotencyKeyFromContext(c *gin.Context) string {
	return c.GetHeader("X-Idempotency-Key")
}

func parseInt(s string) int {
	var i int
	fmt.Sscanf(s, "%d", &i)
	return i
}

// ============================================================================
// ERROR RESPONSE FORMATTING
// ============================================================================

type ErrorResponse struct {
	Success   bool                   `json:"success"`
	Error     string                 `json:"error"`
	ErrorCode string                 `json:"errorCode,omitempty"`
	Message   string                 `json:"message"`
	Details   string                 `json:"details,omitempty"`
	RequestID string                 `json:"requestId"`
	Timestamp string                 `json:"timestamp"`
	Path      string                 `json:"path"`
	Method    string                 `json:"method"`
	Context   map[string]interface{} `json:"context,omitempty"`
}

func FormatErrorResponse(c *gin.Context, statusCode int, errCode, message, details string) {
	requestID := c.GetString("requestId")
	if requestID == "" {
		requestID = uuid.New().String()
	}

	response := ErrorResponse{
		Success:   false,
		Error:     http.StatusText(statusCode),
		ErrorCode: errCode,
		Message:   message,
		Details:   details,
		RequestID: requestID,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Path:      c.Request.URL.Path,
		Method:    c.Request.Method,
	}

	c.JSON(statusCode, response)
}

func HandleStandardError(c *gin.Context, stdErr *errors.StandardError) {
	httpStatus := errors.GetHTTPStatus(stdErr.Code)
	userMessage := errors.GetUserFriendlyMessage(stdErr.Code)

	FormatErrorResponse(
		c,
		httpStatus,
		string(stdErr.Code),
		userMessage,
		stdErr.Details,
	)
}
