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

		if len(corsConfig.AllowOrigins) > 0 {
			allowed := false
			for _, allowedOrigin := range corsConfig.AllowOrigins {
				if allowedOrigin == "*" || allowedOrigin == origin {
					c.Header("Access-Control-Allow-Origin", allowedOrigin)
					allowed = true
					break
				}
			}
			if !allowed && len(corsConfig.AllowOrigins) > 0 {
				c.Header("Access-Control-Allow-Origin", corsConfig.AllowOrigins[0])
			}
		}

		if len(corsConfig.AllowMethods) > 0 {
			c.Header("Access-Control-Allow-Methods", joinStrings(corsConfig.AllowMethods, ", "))
		}

		if len(corsConfig.AllowHeaders) > 0 {
			c.Header("Access-Control-Allow-Headers", joinStrings(corsConfig.AllowHeaders, ", "))
		}

		if len(corsConfig.ExposeHeaders) > 0 {
			c.Header("Access-Control-Expose-Headers", joinStrings(corsConfig.ExposeHeaders, ", "))
		}

		if corsConfig.AllowCredentials {
			c.Header("Access-Control-Allow-Credentials", "true")
		}

		if corsConfig.MaxAge > 0 {
			c.Header("Access-Control-Max-Age", fmt.Sprintf("%d", corsConfig.MaxAge))
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
		c.Header("Content-Security-Policy", "default-src 'self'")
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

func ErrorHandler(log logger.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Next()

		// Only handle errors if response not already written
		if len(c.Errors) > 0 && !c.Writer.Written() {
			err := c.Errors.Last()

			log.Error("Request error", map[string]interface{}{
				"error":     err.Error(),
				"path":      c.Request.URL.Path,
				"method":    c.Request.Method,
				"requestId": c.GetString("requestId"),
			})

			c.JSON(http.StatusInternalServerError, gin.H{
				"success":   false,
				"error":     "internal server error",
				"requestId": c.GetString("requestId"),
			})
		}
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

// // internal/api/middleware/middleware.go
// package middleware

// import (
// 	"bytes"
// 	"context"
// 	"crypto/sha256"
// 	"encoding/hex"
// 	"encoding/json"
// 	"fmt"
// 	"io"
// 	"net/http"
// 	"strings"
// 	"sync"
// 	"time"

// 	"camunda-workers/internal/common/config"
// 	"camunda-workers/internal/common/errors"
// 	"camunda-workers/internal/common/logger"

// 	"github.com/gin-gonic/gin"
// 	"github.com/google/uuid"
// 	"github.com/redis/go-redis/v9"
// 	"go.opentelemetry.io/otel"
// 	"go.opentelemetry.io/otel/attribute"
// 	"go.opentelemetry.io/otel/propagation"
// 	"go.opentelemetry.io/otel/trace"
// 	"golang.org/x/time/rate"
// )

// // ============================================================================
// // REQUEST ID MIDDLEWARE
// // ============================================================================

// func RequestID() gin.HandlerFunc {
// 	return func(c *gin.Context) {
// 		requestID := c.GetHeader("X-Request-ID")
// 		if requestID == "" {
// 			requestID = uuid.New().String()
// 		}
// 		c.Set("requestId", requestID)
// 		c.Header("X-Request-ID", requestID)
// 		c.Next()
// 	}
// }

// // ============================================================================
// // DISTRIBUTED TRACING MIDDLEWARE (GAP #5)
// // ============================================================================

// func TracingMiddleware() gin.HandlerFunc {
// 	tracer := otel.Tracer("api-gateway")

// 	return func(c *gin.Context) {
// 		// Extract trace context from incoming request headers
// 		ctx := otel.GetTextMapPropagator().Extract(
// 			c.Request.Context(),
// 			propagation.HeaderCarrier(c.Request.Header),
// 		)

// 		// Start a new span
// 		ctx, span := tracer.Start(ctx, c.Request.Method+" "+c.Request.URL.Path,
// 			trace.WithSpanKind(trace.SpanKindServer),
// 		)
// 		defer span.End()

// 		// Generate trace ID if not present
// 		traceID := span.SpanContext().TraceID().String()
// 		spanID := span.SpanContext().SpanID().String()

// 		// Set trace IDs in context and headers
// 		c.Set("traceId", traceID)
// 		c.Set("spanId", spanID)
// 		c.Header("X-Trace-ID", traceID)
// 		c.Header("X-Span-ID", spanID)

// 		// Add HTTP attributes to span
// 		span.SetAttributes(
// 			attribute.String("http.method", c.Request.Method),
// 			attribute.String("http.url", c.Request.URL.String()),
// 			attribute.String("http.route", c.Request.URL.Path),
// 			attribute.String("http.client_ip", c.ClientIP()),
// 			attribute.String("http.user_agent", c.Request.UserAgent()),
// 		)

// 		// Replace request context with traced context
// 		c.Request = c.Request.WithContext(ctx)

// 		// Process request
// 		c.Next()

// 		// Add response attributes
// 		span.SetAttributes(
// 			attribute.Int("http.status_code", c.Writer.Status()),
// 			attribute.Int("http.response_size", c.Writer.Size()),
// 		)

// 		// Record error if present
// 		if len(c.Errors) > 0 {
// 			span.RecordError(c.Errors.Last())
// 			span.SetAttributes(attribute.Bool("error", true))
// 		}
// 	}
// }

// // ============================================================================
// // LOGGER MIDDLEWARE
// // ============================================================================

// func Logger(log logger.Logger) gin.HandlerFunc {
// 	return func(c *gin.Context) {
// 		start := time.Now()
// 		path := c.Request.URL.Path
// 		method := c.Request.Method

// 		c.Next()

// 		latency := time.Since(start)
// 		statusCode := c.Writer.Status()
// 		clientIP := c.ClientIP()
// 		requestID := c.GetString("requestId")

// 		// Log level based on status code
// 		logData := map[string]interface{}{
// 			"method":    method,
// 			"path":      path,
// 			"status":    statusCode,
// 			"latency":   latency.Milliseconds(),
// 			"clientIP":  clientIP,
// 			"requestId": requestID,
// 			"userAgent": c.Request.UserAgent(),
// 		}

// 		if statusCode >= 500 {
// 			// Log errors from context if any
// 			if len(c.Errors) > 0 {
// 				logData["error"] = c.Errors.Last().Error()
// 			}
// 			log.Error("API Request Failed", logData)
// 		} else if statusCode >= 400 {
// 			log.Warn("API Request Client Error", logData)
// 		} else {
// 			log.Info("API Request", logData)
// 		}
// 	}
// }

// // ============================================================================
// // CUSTOM RECOVERY MIDDLEWARE
// // ============================================================================

// func Recovery(log logger.Logger) gin.HandlerFunc {
// 	return func(c *gin.Context) {
// 		defer func() {
// 			if err := recover(); err != nil {
// 				// Log the panic
// 				log.Error("Panic recovered", map[string]interface{}{
// 					"error":     fmt.Sprintf("%v", err),
// 					"path":      c.Request.URL.Path,
// 					"method":    c.Request.Method,
// 					"requestId": c.GetString("requestId"),
// 				})

// 				// Return error response
// 				c.JSON(http.StatusInternalServerError, gin.H{
// 					"success":   false,
// 					"error":     "Internal server error",
// 					"message":   "An unexpected error occurred",
// 					"requestId": c.GetString("requestId"),
// 				})

// 				// Abort to prevent further middleware execution
// 				c.Abort()
// 			}
// 		}()
// 		c.Next()
// 	}
// }

// // ============================================================================
// // CORS MIDDLEWARE
// // ============================================================================

// func CORS(corsConfig config.CORSConfig) gin.HandlerFunc {
// 	return func(c *gin.Context) {
// 		origin := c.Request.Header.Get("Origin")

// 		// Set CORS headers
// 		if len(corsConfig.AllowOrigins) > 0 {
// 			allowed := false
// 			for _, allowedOrigin := range corsConfig.AllowOrigins {
// 				if allowedOrigin == "*" || allowedOrigin == origin {
// 					c.Header("Access-Control-Allow-Origin", allowedOrigin)
// 					allowed = true
// 					break
// 				}
// 			}
// 			if !allowed && len(corsConfig.AllowOrigins) > 0 {
// 				c.Header("Access-Control-Allow-Origin", corsConfig.AllowOrigins[0])
// 			}
// 		}

// 		if len(corsConfig.AllowMethods) > 0 {
// 			c.Header("Access-Control-Allow-Methods", joinStrings(corsConfig.AllowMethods, ", "))
// 		}

// 		if len(corsConfig.AllowHeaders) > 0 {
// 			c.Header("Access-Control-Allow-Headers", joinStrings(corsConfig.AllowHeaders, ", "))
// 		}

// 		if len(corsConfig.ExposeHeaders) > 0 {
// 			c.Header("Access-Control-Expose-Headers", joinStrings(corsConfig.ExposeHeaders, ", "))
// 		}

// 		if corsConfig.AllowCredentials {
// 			c.Header("Access-Control-Allow-Credentials", "true")
// 		}

// 		if corsConfig.MaxAge > 0 {
// 			c.Header("Access-Control-Max-Age", fmt.Sprintf("%d", corsConfig.MaxAge))
// 		}

// 		// Handle preflight requests
// 		if c.Request.Method == "OPTIONS" {
// 			c.AbortWithStatus(http.StatusNoContent)
// 			return
// 		}

// 		c.Next()
// 	}
// }

// // ============================================================================
// // RATE LIMITER MIDDLEWARE
// // ============================================================================

// type visitor struct {
// 	limiter  *rate.Limiter
// 	lastSeen time.Time
// }

// type RateLimiterImpl struct {
// 	visitors map[string]*visitor
// 	mu       sync.RWMutex
// 	rps      int
// 	burst    int
// }

// func NewRateLimiter(rps, burst int) *RateLimiterImpl {
// 	rl := &RateLimiterImpl{
// 		visitors: make(map[string]*visitor),
// 		rps:      rps,
// 		burst:    burst,
// 	}

// 	// Cleanup old visitors every 5 minutes
// 	go rl.cleanupVisitors()

// 	return rl
// }

// func (rl *RateLimiterImpl) getVisitor(ip string) *rate.Limiter {
// 	rl.mu.Lock()
// 	defer rl.mu.Unlock()

// 	v, exists := rl.visitors[ip]
// 	if !exists {
// 		limiter := rate.NewLimiter(rate.Limit(rl.rps), rl.burst)
// 		rl.visitors[ip] = &visitor{limiter, time.Now()}
// 		return limiter
// 	}

// 	v.lastSeen = time.Now()
// 	return v.limiter
// }

// func (rl *RateLimiterImpl) cleanupVisitors() {
// 	for {
// 		time.Sleep(5 * time.Minute)

// 		rl.mu.Lock()
// 		for ip, v := range rl.visitors {
// 			if time.Since(v.lastSeen) > 10*time.Minute {
// 				delete(rl.visitors, ip)
// 			}
// 		}
// 		rl.mu.Unlock()
// 	}
// }

// func RateLimiter(rateLimitConfig config.RateLimitConfig) gin.HandlerFunc {
// 	if !rateLimitConfig.Enabled {
// 		return func(c *gin.Context) {
// 			c.Next()
// 		}
// 	}

// 	limiter := NewRateLimiter(rateLimitConfig.RequestsPerSecond, rateLimitConfig.Burst)

// 	return func(c *gin.Context) {
// 		ip := c.ClientIP()

// 		if !limiter.getVisitor(ip).Allow() {
// 			c.JSON(http.StatusTooManyRequests, gin.H{
// 				"success": false,
// 				"error":   "rate limit exceeded",
// 				"code":    "RATE_LIMIT_EXCEEDED",
// 				"message": fmt.Sprintf("Maximum %d requests per second allowed", rateLimitConfig.RequestsPerSecond),
// 			})
// 			c.Abort()
// 			return
// 		}

// 		c.Next()
// 	}
// }

// // ============================================================================
// // SECURITY HEADERS MIDDLEWARE
// // ============================================================================

// func SecurityHeaders() gin.HandlerFunc {
// 	return func(c *gin.Context) {
// 		c.Header("X-Content-Type-Options", "nosniff")
// 		c.Header("X-Frame-Options", "DENY")
// 		c.Header("X-XSS-Protection", "1; mode=block")
// 		c.Header("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
// 		c.Header("Content-Security-Policy", "default-src 'self'")
// 		c.Next()
// 	}
// }

// // ============================================================================
// // ERROR HANDLER MIDDLEWARE
// // ============================================================================

// func ErrorHandler(log logger.Logger) gin.HandlerFunc {
// 	return func(c *gin.Context) {
// 		c.Next()

// 		if len(c.Errors) > 0 {
// 			err := c.Errors.Last()

// 			log.Error("Request error", map[string]interface{}{
// 				"error":     err.Error(),
// 				"path":      c.Request.URL.Path,
// 				"method":    c.Request.Method,
// 				"requestId": c.GetString("requestId"),
// 			})

// 			// Only write response if not already written
// 			if !c.Writer.Written() {
// 				c.JSON(http.StatusInternalServerError, gin.H{
// 					"success":   false,
// 					"error":     "internal server error",
// 					"requestId": c.GetString("requestId"),
// 				})
// 			}
// 		}
// 	}
// }

// // ============================================================================
// // TIMEOUT MIDDLEWARE
// // ============================================================================

// func Timeout(timeout time.Duration) gin.HandlerFunc {
// 	return func(c *gin.Context) {
// 		ctx, cancel := context.WithTimeout(c.Request.Context(), timeout)
// 		defer cancel()

// 		c.Request = c.Request.WithContext(ctx)
// 		c.Next()
// 	}
// }

// // ============================================================================
// // HELPER FUNCTIONS
// // ============================================================================

// func joinStrings(strs []string, sep string) string {
// 	if len(strs) == 0 {
// 		return ""
// 	}
// 	result := strs[0]
// 	for i := 1; i < len(strs); i++ {
// 		result += sep + strs[i]
// 	}
// 	return result
// }

// // ============================================================================
// // INPUT VALIDATION MIDDLEWARE (GAP #1 FIX)
// // ============================================================================

// func InputValidation() gin.HandlerFunc {
// 	return func(c *gin.Context) {
// 		// 1. Validate Content-Type for non-GET requests
// 		if c.Request.Method == "POST" || c.Request.Method == "PUT" || c.Request.Method == "PATCH" {
// 			contentType := c.GetHeader("Content-Type")
// 			if !strings.Contains(contentType, "application/json") &&
// 				!strings.Contains(contentType, "multipart/form-data") {
// 				c.JSON(http.StatusBadRequest, gin.H{
// 					"success": false,
// 					"error":   "Invalid Content-Type",
// 					"message": "Content-Type must be application/json",
// 				})
// 				c.Abort()
// 				return
// 			}
// 		}

// 		// 2. Validate request body size (max 10MB)
// 		const maxBodySize = 10 * 1024 * 1024 // 10MB
// 		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBodySize)

// 		// 3. Validate common query parameters
// 		if page := c.Query("page"); page != "" {
// 			if pageNum := parseInt(page); pageNum < 1 || pageNum > 10000 {
// 				c.JSON(http.StatusBadRequest, gin.H{
// 					"success": false,
// 					"error":   "Invalid page parameter",
// 					"message": "page must be between 1 and 10000",
// 				})
// 				c.Abort()
// 				return
// 			}
// 		}

// 		if limit := c.Query("limit"); limit != "" {
// 			if limitNum := parseInt(limit); limitNum < 1 || limitNum > 100 {
// 				c.JSON(http.StatusBadRequest, gin.H{
// 					"success": false,
// 					"error":   "Invalid limit parameter",
// 					"message": "limit must be between 1 and 100",
// 				})
// 				c.Abort()
// 				return
// 			}
// 		}

// 		c.Next()
// 	}
// }

// // ============================================================================
// // IDEMPOTENCY MIDDLEWARE (GAP #3 FIX - COMPLETE IMPLEMENTATION)
// // ============================================================================

// // IdempotencyMiddleware provides API-level idempotency using Redis
// func IdempotencyMiddleware(redisClient *redis.Client) gin.HandlerFunc {
// 	return func(c *gin.Context) {
// 		// Skip idempotency for safe methods (GET, HEAD, OPTIONS)
// 		method := c.Request.Method
// 		if method == http.MethodGet || method == http.MethodHead || method == http.MethodOptions {
// 			c.Next()
// 			return
// 		}

// 		// Get idempotency key from header
// 		idempotencyKey := c.GetHeader("X-Idempotency-Key")

// 		// If no key provided, generate one and continue
// 		if idempotencyKey == "" {
// 			idempotencyKey = uuid.New().String()
// 			c.Header("X-Idempotency-Key", idempotencyKey)
// 			c.Next()
// 			return
// 		}

// 		// Validate key format
// 		if !validateIdempotencyKey(idempotencyKey) {
// 			c.JSON(http.StatusBadRequest, gin.H{
// 				"success":   false,
// 				"error":     "invalid_idempotency_key",
// 				"message":   "Idempotency key must be a valid UUID or 16-255 characters without whitespace",
// 				"requestId": c.GetString("requestId"),
// 			})
// 			c.Abort()
// 			return
// 		}

// 		// Read request body for hashing
// 		var bodyBytes []byte
// 		if c.Request.Body != nil {
// 			bodyBytes, _ = io.ReadAll(c.Request.Body)
// 			// Restore request body for downstream handlers
// 			c.Request.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))
// 		}

// 		// Generate composite cache key
// 		cacheKey := generateIdempotencyCacheKey(c, idempotencyKey, bodyBytes)

// 		ctx := c.Request.Context()

// 		// Check for cached response
// 		cachedResponse, exists, err := checkCachedResponse(ctx, redisClient, cacheKey)
// 		if err != nil {
// 			// Log error but don't fail the request
// 			fmt.Printf("Idempotency cache check error: %v\n", err)
// 			// Continue processing without idempotency
// 			c.Next()
// 			return
// 		}

// 		if exists {
// 			// Return cached response
// 			c.Header("X-Idempotency-Status", "cached")
// 			c.JSON(http.StatusOK, cachedResponse)
// 			c.Abort()
// 			return
// 		}

// 		// Create response writer to capture response
// 		writer := &responseWriter{
// 			ResponseWriter: c.Writer,
// 			body:           bytes.NewBuffer(nil),
// 			status:         http.StatusOK,
// 		}
// 		c.Writer = writer

// 		// Process request
// 		c.Next()

// 		// Only cache successful responses (2xx)
// 		if writer.status >= 200 && writer.status < 300 {
// 			// Parse response body
// 			var responseBody map[string]interface{}
// 			if err := json.Unmarshal(writer.body.Bytes(), &responseBody); err == nil {
// 				// Add idempotency headers
// 				c.Header("X-Idempotency-Status", "processed")
// 				c.Header("X-Idempotency-Key", idempotencyKey)

// 				// Determine TTL based on endpoint
// 				ttl := getTTLForEndpoint(c.Request.URL.Path)

// 				// Store response in background
// 				go storeCachedResponse(context.Background(), redisClient, cacheKey, responseBody, ttl)
// 			}
// 		}
// 	}
// }

// // ============================================================================
// // HELPER FUNCTIONS FOR IDEMPOTENCY
// // ============================================================================

// // validateIdempotencyKey validates the format of an API idempotency key
// func validateIdempotencyKey(key string) bool {
// 	// Must be a valid UUID
// 	_, err := uuid.Parse(key)
// 	if err == nil {
// 		return true
// 	}

// 	// Or must be 16-255 characters without whitespace
// 	if len(key) < 16 || len(key) > 255 {
// 		return false
// 	}

// 	// Check for whitespace
// 	if strings.ContainsAny(key, " \t\n\r") {
// 		return false
// 	}

// 	return true
// }

// // generateIdempotencyCacheKey creates a composite cache key from request
// func generateIdempotencyCacheKey(c *gin.Context, idempotencyKey string, bodyBytes []byte) string {
// 	method := c.Request.Method
// 	path := c.Request.URL.Path

// 	// Generate body hash
// 	var bodyHash string
// 	if len(bodyBytes) > 0 {
// 		hash := sha256.Sum256(bodyBytes)
// 		bodyHash = hex.EncodeToString(hash[:])
// 	} else {
// 		bodyHash = "empty"
// 	}

// 	return fmt.Sprintf("idempotency:%s:%s:%s:%s", method, path, idempotencyKey, bodyHash)
// }

// // checkCachedResponse checks if response is cached in Redis
// func checkCachedResponse(ctx context.Context, redisClient *redis.Client, cacheKey string) (map[string]interface{}, bool, error) {
// 	val, err := redisClient.Get(ctx, cacheKey).Result()
// 	if err == redis.Nil {
// 		return nil, false, nil
// 	}
// 	if err != nil {
// 		return nil, false, fmt.Errorf("redis get failed: %w", err)
// 	}

// 	var response map[string]interface{}
// 	if err := json.Unmarshal([]byte(val), &response); err != nil {
// 		return nil, false, fmt.Errorf("failed to parse cached response: %w", err)
// 	}

// 	return response, true, nil
// }

// // storeCachedResponse stores response in Redis cache
// func storeCachedResponse(ctx context.Context, redisClient *redis.Client, cacheKey string, response map[string]interface{}, ttl time.Duration) {
// 	responseJSON, err := json.Marshal(response)
// 	if err != nil {
// 		fmt.Printf("Failed to marshal response for caching: %v\n", err)
// 		return
// 	}

// 	if err := redisClient.Set(ctx, cacheKey, responseJSON, ttl).Err(); err != nil {
// 		fmt.Printf("Failed to cache idempotency response: %v\n", err)
// 	}
// }

// // getTTLForEndpoint determines appropriate TTL based on endpoint
// func getTTLForEndpoint(path string) time.Duration {
// 	// Default TTL: 24 hours
// 	ttl := 24 * time.Hour

// 	// Special TTLs for different endpoints
// 	if strings.Contains(path, "/auth/signup") || strings.Contains(path, "/auth/signin") {
// 		ttl = 1 * time.Hour // Shorter TTL for auth operations
// 	} else if strings.Contains(path, "/applications/submit") {
// 		ttl = 24 * time.Hour // Long TTL for applications
// 	} else if strings.Contains(path, "/email/") {
// 		ttl = 24 * time.Hour // Long TTL for emails
// 	} else if strings.Contains(path, "/crm/") {
// 		ttl = 7 * 24 * time.Hour // 1 week for CRM operations
// 	}

// 	return ttl
// }

// // responseWriter captures the response for caching
// type responseWriter struct {
// 	gin.ResponseWriter
// 	body   *bytes.Buffer
// 	status int
// }

// func (w *responseWriter) Write(b []byte) (int, error) {
// 	w.body.Write(b)
// 	return w.ResponseWriter.Write(b)
// }

// func (w *responseWriter) WriteString(s string) (int, error) {
// 	w.body.WriteString(s)
// 	return w.ResponseWriter.WriteString(s)
// }

// func (w *responseWriter) WriteHeader(statusCode int) {
// 	w.status = statusCode
// 	w.ResponseWriter.WriteHeader(statusCode)
// }

// // GetIdempotencyKeyFromContext gets idempotency key from context
// func GetIdempotencyKeyFromContext(c *gin.Context) string {
// 	return c.GetHeader("X-Idempotency-Key")
// }

// // Helper function to parse integers
// func parseInt(s string) int {
// 	var i int
// 	fmt.Sscanf(s, "%d", &i)
// 	return i
// }

// // ============================================================================
// // ERROR RESPONSE FORMATTING (GAP #4)
// // ============================================================================

// // ErrorResponse represents a standardized API error response
// type ErrorResponse struct {
// 	Success   bool                   `json:"success"`
// 	Error     string                 `json:"error"`
// 	ErrorCode string                 `json:"errorCode,omitempty"`
// 	Message   string                 `json:"message"`
// 	Details   string                 `json:"details,omitempty"`
// 	RequestID string                 `json:"requestId"`
// 	Timestamp string                 `json:"timestamp"`
// 	Path      string                 `json:"path"`
// 	Method    string                 `json:"method"`
// 	Context   map[string]interface{} `json:"context,omitempty"`
// }

// // FormatErrorResponse creates a standardized error response
// func FormatErrorResponse(c *gin.Context, statusCode int, errCode, message, details string) {
// 	requestID := c.GetString("requestId")
// 	if requestID == "" {
// 		requestID = uuid.New().String()
// 	}

// 	response := ErrorResponse{
// 		Success:   false,
// 		Error:     http.StatusText(statusCode),
// 		ErrorCode: errCode,
// 		Message:   message,
// 		Details:   details,
// 		RequestID: requestID,
// 		Timestamp: time.Now().UTC().Format(time.RFC3339),
// 		Path:      c.Request.URL.Path,
// 		Method:    c.Request.Method,
// 	}

// 	c.JSON(statusCode, response)
// }

// // HandleStandardError converts StandardError to HTTP response
// func HandleStandardError(c *gin.Context, stdErr *errors.StandardError) {
// 	httpStatus := errors.GetHTTPStatus(stdErr.Code)
// 	userMessage := errors.GetUserFriendlyMessage(stdErr.Code)

// 	FormatErrorResponse(
// 		c,
// 		httpStatus,
// 		string(stdErr.Code),
// 		userMessage,
// 		stdErr.Details,
// 	)
// }
