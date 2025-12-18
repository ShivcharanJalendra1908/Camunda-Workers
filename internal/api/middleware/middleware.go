// internal/api/middleware/middleware.go
package middleware

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"camunda-workers/internal/common/config"
	"camunda-workers/internal/common/logger"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
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

		log.Info("API Request", map[string]interface{}{
			"method":     method,
			"path":       path,
			"status":     statusCode,
			"latency":    latency.Milliseconds(),
			"clientIP":   clientIP,
			"requestId":  requestID,
			"userAgent":  c.Request.UserAgent(),
		})
	}
}

// ============================================================================
// CORS MIDDLEWARE
// ============================================================================

func CORS(corsConfig config.CORSConfig) gin.HandlerFunc {
	return func(c *gin.Context) {
		origin := c.Request.Header.Get("Origin")
		
		// Set CORS headers
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

		// Handle preflight requests
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

	// Cleanup old visitors every 5 minutes
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
				"error": "rate limit exceeded",
				"code":  "RATE_LIMIT_EXCEEDED",
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
// ERROR HANDLER MIDDLEWARE
// ============================================================================

func ErrorHandler(log logger.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Next()

		if len(c.Errors) > 0 {
			err := c.Errors.Last()
			
			log.Error("Request error", map[string]interface{}{
				"error":     err.Error(),
				"path":      c.Request.URL.Path,
				"method":    c.Request.Method,
				"requestId": c.GetString("requestId"),
			})

			c.JSON(-1, gin.H{
				"error":     "internal server error",
				"requestId": c.GetString("requestId"),
			})
		}
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

		finished := make(chan struct{})
		go func() {
			c.Next()
			finished <- struct{}{}
		}()

		select {
		case <-finished:
			return
		case <-ctx.Done():
			c.JSON(http.StatusRequestTimeout, gin.H{
				"error": "request timeout",
				"code":  "REQUEST_TIMEOUT",
			})
			c.Abort()
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




// // internal/api/middleware/middleware.go
// package middleware

// import (
// 	"context" 
// 	"fmt"
// 	"net/http"
// 	"sync"
// 	"time"

// 	"camunda-workers/internal/common/config"
// 	"camunda-workers/internal/common/logger"
	
// 	"github.com/gin-gonic/gin"
// 	"github.com/google/uuid"
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

// 		log.Info("API Request", map[string]interface{}{
// 			"method":     method,
// 			"path":       path,
// 			"status":     statusCode,
// 			"latency":    latency.Milliseconds(),
// 			"clientIP":   clientIP,
// 			"requestId":  requestID,
// 			"userAgent":  c.Request.UserAgent(),
// 		})
// 	}
// }

// // ============================================================================
// // CORS MIDDLEWARE
// // ============================================================================

// // ✅ FIXED - Now accepts config.CORSConfig directly
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

// type RateLimiter struct {
// 	visitors map[string]*visitor
// 	mu       sync.RWMutex
// 	rps      int
// 	burst    int
// }

// func NewRateLimiter(rps, burst int) *RateLimiter {
// 	rl := &RateLimiter{
// 		visitors: make(map[string]*visitor),
// 		rps:      rps,
// 		burst:    burst,
// 	}

// 	// Cleanup old visitors every 5 minutes
// 	go rl.cleanupVisitors()

// 	return rl
// }

// func (rl *RateLimiter) getVisitor(ip string) *rate.Limiter {
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

// func (rl *RateLimiter) cleanupVisitors() {
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

// // ✅ FIXED - Now accepts config.RateLimitConfig directly
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
// 				"error": "rate limit exceeded",
// 				"code":  "RATE_LIMIT_EXCEEDED",
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

// 			c.JSON(-1, gin.H{
// 				"error":     "internal server error",
// 				"requestId": c.GetString("requestId"),
// 			})
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

// 		finished := make(chan struct{})
// 		go func() {
// 			c.Next()
// 			finished <- struct{}{}
// 		}()

// 		select {
// 		case <-finished:
// 			return
// 		case <-ctx.Done():
// 			c.JSON(http.StatusRequestTimeout, gin.H{
// 				"error": "request timeout",
// 				"code":  "REQUEST_TIMEOUT",
// 			})
// 			c.Abort()
// 		}
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