package middleware

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"camunda-workers/internal/common/config"
	"camunda-workers/internal/common/database"
	"camunda-workers/internal/models"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

const (
	blockKeyPrefix = "block:guest:"
	anomIPTokenPrefix = "anom:ip:"
	anomTokenIPsPrefix = "anom:token:"
	anomBurstPrefix = "anom:token:"
)

// GuestAnomalyMiddleware runs anomaly detection checks S1â€“S5 before quota enforcement.
//
// S5: Blocklist check (cheapest â€” single EXISTS)
// S3: Burst detection (INCR, 3+ in 60s = block)
// S1: Unique tokens per IP/24 (HyperLogLog, 20+ = block)
// S2: Unique IPs per token (HyperLogLog, 3+ = block)
// S4: Token absence â€” log warning, use fallback (don't block)
//
// On Redis failure: fail open (don't punish users for infra issues).
func GuestAnomalyMiddleware(redisClient *redis.Client, cfg *config.Config, auditRepo *database.GuestAuditRepo) gin.HandlerFunc {
	anomalyCfg := cfg.Guest.Anomaly

	return func(c *gin.Context) {
		// Skip anomaly detection if disabled
		if !anomalyCfg.Enabled {
			c.Next()
			return
		}

		identity := getGuestIdentity(c)
		if identity == nil {
			c.Next()
			return
		}

		ctx := c.Request.Context()
		key := identity.CompositeKey

		// S4: Token absence warning (logged by guest_signal, not blocking)
		// This is informational only â€” already handled by signal middleware

		// â”€â”€ S5: Blocklist check â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€
		if isBlocked(ctx, redisClient, key, anomalyCfg.BlockTTLSeconds) {
			blocked := &models.GuestAuditEvent{
				SessionID:    "",
				CompositeKey: key,
				Action:       "blocked",
				Blocked:      true,
				BlockReason:  "S5_blocklist",
				CreatedAt:    time.Now(),
			}
			logAuditEvent(auditRepo, blocked)

			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"success": false,
				"error":   "ACCESS_BLOCKED",
				"message": "Access temporarily restricted. Please try again later.",
			})
			return
		}

		// â”€â”€ S3: Burst detection â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€
		burstCount, err := checkBurst(ctx, redisClient, key,
			anomalyCfg.S3BurstThreshold,
			time.Duration(anomalyCfg.S3BurstWindowSeconds)*time.Second)
		if err == nil && burstCount >= int64(anomalyCfg.S3BurstThreshold) {
			blockGuest(ctx, redisClient, key, anomalyCfg.BlockTTLSeconds)
			blocked := &models.GuestAuditEvent{
				CompositeKey: key,
				Action:       "blocked",
				Blocked:      true,
				BlockReason:  fmt.Sprintf("S3_burst_%d_in_%ds", burstCount, anomalyCfg.S3BurstWindowSeconds),
				CreatedAt:    time.Now(),
			}
			logAuditEvent(auditRepo, blocked)

			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
				"success": false,
				"error":   "RATE_LIMITED",
				"message": "Too many requests. Please slow down.",
			})
			return
		}

		// â”€â”€ S1: Unique tokens per IP/24 â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€
		if identity.FallbackIP != "" {
			// S1 checks: how many unique tokens are using this IP/24?
			tokenCount, err := countTokensPerIP(ctx, redisClient, identity.FallbackIP, key,
				time.Hour) // 1hr window
			if err == nil && tokenCount >= int64(anomalyCfg.S1TokensPerIPThreshold) {
				blockGuest(ctx, redisClient, key, anomalyCfg.BlockTTLSeconds)
				blocked := &models.GuestAuditEvent{
					CompositeKey: key,
					Action:       "blocked",
					Blocked:      true,
					BlockReason:  fmt.Sprintf("S1_%d_tokens_from_ip", tokenCount),
					CreatedAt:    time.Now(),
				}
			logAuditEvent(auditRepo, blocked)

			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
				"success": false,
				"error":   "TOO_MANY_DEVICES",
				"message": "Too many devices from this network. Please sign in.",
			})
				return
			}
		}

		// â”€â”€ S2: Unique IPs per token â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€
		if identity.HasToken {
			currentIP := ipSlash24(c.ClientIP())
			ipCount, err := countIPsPerToken(ctx, redisClient, identity.CompositeKey, currentIP,
				time.Hour) // 1hr window
			if err == nil && ipCount >= int64(anomalyCfg.S2IPsPerTokenThreshold) {
				blockGuest(ctx, redisClient, key, anomalyCfg.BlockTTLSeconds)
				blocked := &models.GuestAuditEvent{
					CompositeKey: key,
					Action:       "blocked",
					Blocked:      true,
					BlockReason:  fmt.Sprintf("S2_%d_ips_per_token", ipCount),
					CreatedAt:    time.Now(),
				}
			logAuditEvent(auditRepo, blocked)

			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
				"success": false,
				"error":   "SHARED_DEVICE",
				"message": "Too many locations for this device. Please sign in.",
			})
				return
			}
		}

		c.Next()
	}
}

// â”€â”€ Anomaly helpers â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€

func getGuestIdentity(c *gin.Context) *models.GuestIdentity {
	val, exists := c.Get(models.CompositeKeyContext)
	if !exists {
		return nil
	}
	identity, ok := val.(*models.GuestIdentity)
	if !ok {
		return nil
	}
	return identity
}

// isBlocked checks S5 blocklist.
func isBlocked(ctx context.Context, client *redis.Client, compositeKey string, blockTTL int) bool {
	key := blockKeyPrefix + compositeKey
	exists, err := client.Exists(ctx, key).Result()
	if err != nil {
		return false // fail open
	}
	return exists > 0
}

// blockGuest adds a guest to the blocklist.
func blockGuest(ctx context.Context, client *redis.Client, compositeKey string, blockTTL int) {
	key := blockKeyPrefix + compositeKey
	ttl := time.Duration(blockTTL) * time.Second
	client.Set(ctx, key, "blocked", ttl)
}

// checkBurst increments burst counter and returns current count.
func checkBurst(ctx context.Context, client *redis.Client, compositeKey string, threshold int, window time.Duration) (int64, error) {
	key := anomBurstPrefix + compositeKey + ":burst"
	count, err := client.Incr(ctx, key).Result()
	if err != nil {
		return 0, err
	}
	if count == 1 {
		client.Expire(ctx, key, window)
	}
	return count, nil
}

// countTokensPerIP registers the composite key in the IP's HyperLogLog and returns unique token count.
func countTokensPerIP(ctx context.Context, client *redis.Client, ipSlash24, compositeKey string, ttl time.Duration) (int64, error) {
	key := anomIPTokenPrefix + ipSlash24 + ":tokens"
	// Register this token under the IP
	client.PFAdd(ctx, key, compositeKey)
	// Set TTL on first registration (HyperLogLog doesn't track this, so always set â€” cheap no-op if exists)
	client.Expire(ctx, key, ttl)
	return client.PFCount(ctx, key).Result()
}

// countIPsPerToken registers the IP in the token's HyperLogLog and returns unique IP count.
func countIPsPerToken(ctx context.Context, client *redis.Client, compositeKey, ipSlash24 string, ttl time.Duration) (int64, error) {
	key := anomTokenIPsPrefix + compositeKey + ":ips"
	// Register this IP under the token
	client.PFAdd(ctx, key, ipSlash24)
	// Set TTL on first registration
	client.Expire(ctx, key, ttl)
	return client.PFCount(ctx, key).Result()
}

// logAuditEvent writes to PG audit log if repo is available, otherwise logs to stdout.
func logAuditEvent(auditRepo *database.GuestAuditRepo, event *models.GuestAuditEvent) {
	if auditRepo != nil {
		auditRepo.LogEvent(event)
		return
	}
	fmt.Printf("[GUEST_AUDIT] action=%s composite_key=%s blocked=%v reason=%s\n",
		event.Action, event.CompositeKey, event.Blocked, event.BlockReason)
}
