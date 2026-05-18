package flagsmith

import (
	"context"
	"fmt"
	"sync"
	"time"

	"camunda-workers/internal/common/config"
	"camunda-workers/internal/common/logger"

	flagsmithapi "github.com/Flagsmith/flagsmith-go-client/v3"
)

var (
	clientInstance *flagsmithapi.Client
	enabled        bool
	mu             sync.RWMutex
	log            logger.Logger
)

// Init initializes the global Flagsmith client wrapper.
func Init(cfg *config.Config, l logger.Logger) {
	mu.Lock()
	defer mu.Unlock()

	log = l
	enabled = cfg.Flagsmith.Enabled

	if !enabled {
		log.Info("Flagsmith feature flags are disabled in config. Using fallback behavior.", nil)
		return
	}

	if cfg.Flagsmith.EnvironmentKey == "" {
		log.Warn("Flagsmith is enabled but environment key is empty. Disabling Flagsmith integration.", nil)
		enabled = false
		return
	}

	ctx := context.Background()
	var opts []flagsmithapi.Option

	if cfg.Flagsmith.EnableLocalEvaluation {
		opts = append(opts, flagsmithapi.WithLocalEvaluation(ctx))
		log.Info("Flagsmith client running with local evaluation enabled", nil)
	}

	if cfg.Flagsmith.EnvironmentRefreshTTL > 0 {
		opts = append(opts, flagsmithapi.WithEnvironmentRefreshInterval(time.Duration(cfg.Flagsmith.EnvironmentRefreshTTL)*time.Second))
	}

	client := flagsmithapi.NewClient(cfg.Flagsmith.EnvironmentKey, opts...)
	clientInstance = client

	log.Info("Flagsmith client initialized successfully", map[string]interface{}{
		"local_evaluation": cfg.Flagsmith.EnableLocalEvaluation,
		"refresh_interval": cfg.Flagsmith.EnvironmentRefreshTTL,
	})
}

// IsFeatureEnabled checks if a global/environment feature flag is active.
// If Flagsmith is disabled or uninitialized, it returns false (fail-closed fallback).
func IsFeatureEnabled(ctx context.Context, flagName string) bool {
	mu.RLock()
	defer mu.RUnlock()

	if !enabled || clientInstance == nil {
		return false
	}

	flags, err := clientInstance.GetEnvironmentFlags(ctx)
	if err != nil {
		if log != nil {
			log.Error("Flagsmith environment flags retrieval failed", map[string]interface{}{
				"flag":  flagName,
				"error": err.Error(),
			})
		}
		return false
	}

	res, err := flags.IsFeatureEnabled(flagName)
	if err != nil {
		return false
	}

	return res
}

// GetFeatureValue reads a global/environment remote config value as a string.
func GetFeatureValue(ctx context.Context, flagName string) string {
	mu.RLock()
	defer mu.RUnlock()

	if !enabled || clientInstance == nil {
		return ""
	}

	flags, err := clientInstance.GetEnvironmentFlags(ctx)
	if err != nil {
		if log != nil {
			log.Error("Flagsmith environment value retrieval failed", map[string]interface{}{
				"flag":  flagName,
				"error": err.Error(),
			})
		}
		return ""
	}

	res, err := flags.GetFeatureValue(flagName)
	if err != nil || res == nil {
		return ""
	}

	return fmt.Sprintf("%v", res)
}

// IsFeatureEnabledForUser checks if a feature flag is enabled for a specific user ID.
func IsFeatureEnabledForUser(ctx context.Context, userID string, flagName string, traits map[string]interface{}) bool {
	mu.RLock()
	defer mu.RUnlock()

	if !enabled || clientInstance == nil || userID == "" {
		return false
	}

	sdkTraits := make([]*flagsmithapi.Trait, 0, len(traits))
	for k, v := range traits {
		sdkTraits = append(sdkTraits, &flagsmithapi.Trait{
			TraitKey:   k,
			TraitValue: v,
		})
	}

	flags, err := clientInstance.GetIdentityFlags(ctx, userID, sdkTraits)
	if err != nil {
		if log != nil {
			log.Error("Flagsmith identity flags retrieval failed", map[string]interface{}{
				"user_id": userID,
				"flag":    flagName,
				"error":   err.Error(),
			})
		}
		return false
	}

	res, err := flags.IsFeatureEnabled(flagName)
	if err != nil {
		return false
	}

	return res
}

// GetFeatureValueForUser reads a remote config value for a specific user ID.
func GetFeatureValueForUser(ctx context.Context, userID string, flagName string, traits map[string]interface{}) string {
	mu.RLock()
	defer mu.RUnlock()

	if !enabled || clientInstance == nil || userID == "" {
		return ""
	}

	sdkTraits := make([]*flagsmithapi.Trait, 0, len(traits))
	for k, v := range traits {
		sdkTraits = append(sdkTraits, &flagsmithapi.Trait{
			TraitKey:   k,
			TraitValue: v,
		})
	}

	flags, err := clientInstance.GetIdentityFlags(ctx, userID, sdkTraits)
	if err != nil {
		if log != nil {
			log.Error("Flagsmith identity value retrieval failed", map[string]interface{}{
				"user_id": userID,
				"flag":    flagName,
				"error":   err.Error(),
			})
		}
		return ""
	}

	res, err := flags.GetFeatureValue(flagName)
	if err != nil || res == nil {
		return ""
	}

	return fmt.Sprintf("%v", res)
}
