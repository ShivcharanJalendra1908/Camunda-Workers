package verifywebsite

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"camunda-workers/internal/common/logger"
	"github.com/camunda/zeebe/clients/go/v8/pkg/entities"
	"github.com/camunda/zeebe/clients/go/v8/pkg/worker"
)

const TaskType = "verify-website"

type Config struct {
	Timeout int `mapstructure:"timeout"`
}

type Handler struct {
	config *Config
	logger logger.Logger
	client *http.Client
}

func NewHandler(cfg *Config, log logger.Logger) *Handler {
	return &Handler{
		config: cfg,
		logger: log,
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

func (h *Handler) Handle(client worker.JobClient, job entities.Job) {
	jobKey := job.GetKey()
	
	variables, err := job.GetVariablesAsMap()
	if err != nil {
		h.logger.Error("Failed to get variables", map[string]interface{}{"jobKey": jobKey, "error": err.Error()})
		client.NewFailJobCommand().JobKey(jobKey).Retries(0).ErrorMessage(err.Error()).Send(context.Background())
		return
	}

	websiteURL, ok := variables["websiteUrl"].(string)
	verificationToken, tokOk := variables["verificationToken"].(string)

	if !ok || !tokOk || websiteURL == "" || verificationToken == "" {
		h.logger.Error("Missing websiteUrl or verificationToken", map[string]interface{}{"jobKey": jobKey})
		client.NewFailJobCommand().JobKey(jobKey).Retries(0).ErrorMessage("Missing websiteUrl or verificationToken").Send(context.Background())
		return
	}

	h.logger.Info("Verifying website", map[string]interface{}{"jobKey": jobKey, "websiteUrl": websiteURL})

	isVerified := false
	var verificationMethod string

	// 1. DNS TXT Check
	parsedURL, err := url.Parse(websiteURL)
	if err == nil && parsedURL.Host != "" {
		domain := parsedURL.Hostname()
		txtRecords, _ := net.LookupTXT(domain)
		for _, txt := range txtRecords {
			if strings.Contains(txt, fmt.Sprintf("lemici-site-verification=%s", verificationToken)) {
				isVerified = true
				verificationMethod = "DNS_TXT"
				break
			}
		}
	}

	// 2. HTTP Meta Tag Check (if DNS failed)
	if !isVerified {
		req, _ := http.NewRequest("GET", websiteURL, nil)
		req.Header.Set("User-Agent", "LeMiCi-Site-Verifier/1.0")
		resp, err := h.client.Do(req)
		if err == nil {
			defer resp.Body.Close()
			if resp.StatusCode == 200 {
				bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20)) // Read up to 1MB
				bodyStr := string(bodyBytes)
				// Look for meta tag with regex supporting different attribute orders and single/double quotes
				metaRegex1 := regexp.MustCompile(`(?i)<meta\s+[^>]*name=["']lemici-site-verification["']\s+[^>]*content=["']` + regexp.QuoteMeta(verificationToken) + `["']`)
				metaRegex2 := regexp.MustCompile(`(?i)<meta\s+[^>]*content=["']` + regexp.QuoteMeta(verificationToken) + `["']\s+[^>]*name=["']lemici-site-verification["']`)
				if metaRegex1.MatchString(bodyStr) || metaRegex2.MatchString(bodyStr) {
					isVerified = true
					verificationMethod = "META_TAG"
				}
			}
		}
	}

	h.logger.Info("Website verification completed", map[string]interface{}{
		"jobKey":             jobKey,
		"websiteUrl":         websiteURL,
		"isVerified":         isVerified,
		"verificationMethod": verificationMethod,
	})

	variablesMap := map[string]interface{}{
		"isVerified":         isVerified,
		"verificationMethod": verificationMethod,
	}

	cmd, err := client.NewCompleteJobCommand().JobKey(jobKey).VariablesFromMap(variablesMap)
	if err == nil {
		cmd.Send(context.Background())
	} else {
		h.logger.Error("Failed to complete job", map[string]interface{}{"jobKey": jobKey, "error": err.Error()})
	}
}
