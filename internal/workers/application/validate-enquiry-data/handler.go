package validateenquirydata

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/camunda/zeebe/clients/go/v8/pkg/entities"
	"github.com/camunda/zeebe/clients/go/v8/pkg/worker"

	appErrs "camunda-workers/internal/common/errors"
	"camunda-workers/internal/common/logger"
)

var (
	emailRegex = regexp.MustCompile(`^[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}$`)
	phoneRegex = regexp.MustCompile(`^\+?[0-9\s\-]{8,15}$`)
)

type Handler struct {
	config       *Config
	logger       logger.Logger
	errorHandler *appErrs.ErrorHandler
}

func NewHandler(config *Config, log logger.Logger) *Handler {
	if config == nil {
		config = DefaultConfig()
	}
	return &Handler{
		config:       config,
		logger:       log.WithFields(map[string]interface{}{"taskType": TaskType}),
		errorHandler: appErrs.NewErrorHandler(log),
	}
}

func (h *Handler) Handle(client worker.JobClient, job entities.Job) {
	ctx, cancel := context.WithTimeout(context.Background(), h.config.Timeout)
	defer cancel()

	h.logger.Info("processing validate-enquiry-data job", map[string]interface{}{
		"jobKey":      job.GetKey(),
		"workflowKey": job.GetProcessInstanceKey(),
	})

	// ===== STEP 1: PARSE INPUT =====
	var input Input
	if err := json.Unmarshal([]byte(job.Variables), &input); err != nil {
		h.errorHandler.HandleJobError(ctx, client, job,
			appErrs.NewValidationError("input", fmt.Sprintf("parse failed: %v", err)))
		return
	}

	// ===== STEP 2: VALIDATE REQUIRED IDs =====
	if input.FranchiseID == "" {
		h.errorHandler.HandleJobError(ctx, client, job,
			appErrs.NewRequiredFieldError("franchiseId"))
		return
	}
	if input.UserID == "" {
		h.errorHandler.HandleJobError(ctx, client, job,
			appErrs.NewRequiredFieldError("userId"))
		return
	}

	// ===== STEP 3: MERGE profile + form data =====
	merged := h.mergeData(input.UserProfile, input.EnquiryFormData)

	// ===== STEP 4: VALIDATE MERGED DATA =====
	errors := h.validateMergedData(merged)

	if len(errors) > 0 {
		h.logger.Info("enquiry validation failed", map[string]interface{}{
			"errors": errors,
		})

		output := Output{
			IsValid:          false,
			MergedData:       merged,
			ValidationErrors: errors,
		}
		h.completeJob(ctx, client, job, output)
		return
	}

	h.logger.Info("enquiry validation passed", map[string]interface{}{
		"franchiseId": input.FranchiseID,
		"userId":      input.UserID,
	})

	output := Output{
		IsValid:          true,
		MergedData:       merged,
		ValidationErrors: []string{},
	}
	h.completeJob(ctx, client, job, output)
}

// mergeData: form data overrides profile data
// Profile se prefill, form se override
func (h *Handler) mergeData(profile, form map[string]interface{}) map[string]interface{} {
	merged := map[string]interface{}{}

	// Step 1: Profile se prefill karo
	// users table mein: id, email, name, phone
	if v, ok := profile["name"].(string); ok && v != "" {
		merged["fullName"] = v
	}
	if v, ok := profile["email"].(string); ok && v != "" {
		merged["email"] = v
	}
	if v, ok := profile["phone"].(string); ok && v != "" {
		merged["phone"] = v
	}

	// Step 2: Form data se override karo (non-empty values only)
	formFields := []string{
		"fullName", "email", "phone",
		"city", "state", "pinCode",
		"preferredState", "preferredCity",
		"approximateInvestmentBudget",
		"involvementLevel",
		"background",
		"applicantType",
	}

	for _, field := range formFields {
		if v, ok := form[field]; ok {
			// String type check - empty string skip karo
			if str, isStr := v.(string); isStr {
				if strings.TrimSpace(str) != "" {
					merged[field] = strings.TrimSpace(str)
				}
			} else if v != nil {
				merged[field] = v
			}
		}
	}

	return merged
}

// validateMergedData: required fields check
func (h *Handler) validateMergedData(data map[string]interface{}) []string {
	var errors []string

	// Required fields
	requiredFields := map[string]string{
		"fullName":                    "Full name is required",
		"email":                       "Email address is required",
		"phone":                       "Phone number is required",
		"preferredState":              "Preferred state is required",
		"approximateInvestmentBudget": "Investment budget is required",
	}

	for field, msg := range requiredFields {
		v, ok := data[field]
		if !ok {
			errors = append(errors, msg)
			continue
		}
		if str, isStr := v.(string); isStr && strings.TrimSpace(str) == "" {
			errors = append(errors, msg)
		}
	}

	// Email format check
	if email, ok := data["email"].(string); ok && email != "" {
		if !emailRegex.MatchString(email) {
			errors = append(errors, "Invalid email format")
		}
	}

	// Phone format check
	if phone, ok := data["phone"].(string); ok && phone != "" {
		// Digits only extract karke check karo
		digitsOnly := regexp.MustCompile(`\d`).FindAllString(phone, -1)
		if len(digitsOnly) < 8 || len(digitsOnly) > 15 {
			errors = append(errors, "Phone number must be 8-15 digits")
		}
	}

	// Involvement level valid values check
	if involvement, ok := data["involvementLevel"].(string); ok && involvement != "" {
		validValues := map[string]bool{
			"full-time-owner":   true,
			"part-time-manager": true,
			"investor-only":     true,
			"full_time_owner":   true,
			"part_time_manager": true,
			"investor_only":     true,
		}
		if !validValues[involvement] {
			errors = append(errors, "Invalid involvement level")
		}
	}

	// Applicant type check
	if appType, ok := data["applicantType"].(string); ok && appType != "" {
		if appType != "individual" && appType != "company" {
			errors = append(errors, "Applicant type must be 'individual' or 'company'")
		}
	}

	return errors
}

func (h *Handler) completeJob(ctx context.Context, client worker.JobClient, job entities.Job, output Output) {
	cmd, err := client.NewCompleteJobCommand().
		JobKey(job.GetKey()).
		VariablesFromObject(output)
	if err != nil {
		h.logger.Error("failed to create complete command", map[string]interface{}{"error": err})
		return
	}
	if _, err := cmd.Send(ctx); err != nil {
		h.logger.Error("failed to complete job", map[string]interface{}{"error": err})
	}
}

// Execute - direct API call ke liye (testing/internal use)
func (h *Handler) Execute(ctx context.Context, input *Input) (*Output, error) {
	_ = ctx
	merged := h.mergeData(input.UserProfile, input.EnquiryFormData)
	errors := h.validateMergedData(merged)

	return &Output{
		IsValid:          len(errors) == 0,
		MergedData:       merged,
		ValidationErrors: errors,
	}, nil
}

// GetFranchiseContactEmail - franchisee ka contact email fetch karne ka helper
// BPMN ke query-postgresql step mein ye query use hogi
// Query: SELECT contact_email, name FROM franchises WHERE id = $1
func GetFranchiseContactEmailQuery() string {
	return "SELECT contact_email, name FROM franchises WHERE id = $1"
}

// RenderEnquiryEmailBody - franchisee ko bhejne wali email ka body
func RenderApplicantConfirmationEmail(data map[string]interface{}) (string, string) {
	franchiseName := getStr(data, "franchiseName", "the franchise")
	applicantName := getStr(data, "fullName", "Applicant")
	applicationID := getStr(data, "applicationId", "")

	subject := fmt.Sprintf("Your enquiry for %s has been received — Ref #%s", franchiseName, applicationID)
	body := fmt.Sprintf(`
<html><body>
<h2>Hello %s,</h2>
<p>Thank you for your interest in <strong>%s</strong>.</p>
<p>We have received your enquiry. Our team will connect you with the franchise representative shortly.</p>
<p><strong>Application Reference:</strong> %s</p>
<p><strong>Next Steps:</strong></p>
<ul>
  <li>The franchise team will review your enquiry within 2-3 business days</li>
  <li>You may be contacted at the phone number you provided</li>
  <li>Keep your Application Reference handy for follow-ups</li>
</ul>
<p>Best regards,<br/>Team Lemici</p>
</body></html>`, applicantName, franchiseName, applicationID)

	return subject, body
}

// RenderFranchiseeLeadEmail - franchisee owner ko bhejne wali email
func RenderFranchiseeLeadEmail(data map[string]interface{}) (string, string) {
	applicantName := getStr(data, "fullName", "A user")
	applicantCity := getStr(data, "city", "N/A")
	applicantState := getStr(data, "state", "N/A")
	applicantPhone := getStr(data, "phone", "N/A")
	applicantEmail := getStr(data, "email", "N/A")
	budget := getStr(data, "approximateInvestmentBudget", "N/A")
	involvement := getStr(data, "involvementLevel", "N/A")
	applicantType := getStr(data, "applicantType", "N/A")
	background := getStr(data, "background", "Not provided")
	applicationID := getStr(data, "applicationId", "")
	franchiseName := getStr(data, "franchiseName", "Your Franchise")

	subject := fmt.Sprintf("New Enquiry — %s from %s, %s | Budget: %s", applicantName, applicantCity, applicantState, budget)

	body := fmt.Sprintf(`
<html><body>
<h2>New Franchise Enquiry for %s</h2>
<p>A new enquiry has been submitted via Lemici. Details below:</p>

<table border="1" cellpadding="8" cellspacing="0" style="border-collapse:collapse;">
  <tr><td><strong>Applicant Name</strong></td><td>%s</td></tr>
  <tr><td><strong>Email</strong></td><td>%s</td></tr>
  <tr><td><strong>Phone</strong></td><td>%s</td></tr>
  <tr><td><strong>City / State</strong></td><td>%s, %s</td></tr>
  <tr><td><strong>Applicant Type</strong></td><td>%s</td></tr>
  <tr><td><strong>Investment Budget</strong></td><td>%s</td></tr>
  <tr><td><strong>Involvement Level</strong></td><td>%s</td></tr>
  <tr><td><strong>Background</strong></td><td>%s</td></tr>
  <tr><td><strong>Application ID</strong></td><td>%s</td></tr>
</table>

<p>Please respond to this lead within 48 hours.</p>
<p>Login to your Lemici dashboard to view and manage this lead.</p>
<p>Best regards,<br/>Team Lemici</p>
</body></html>`,
		franchiseName,
		applicantName, applicantEmail, applicantPhone,
		applicantCity, applicantState,
		applicantType, budget, involvement, background,
		applicationID)

	return subject, body
}

// RenderInternalAlertEmail - internal team ko bhejne wali email
func RenderInternalAlertEmail(data map[string]interface{}) (string, string) {
	applicationID := getStr(data, "applicationId", "N/A")
	franchiseName := getStr(data, "franchiseName", "N/A")
	applicantName := getStr(data, "fullName", "N/A")
	budget := getStr(data, "approximateInvestmentBudget", "N/A")
	priority := getStr(data, "priority", "standard")

	subject := fmt.Sprintf("[New Enquiry] #%s | %s | Priority: %s", applicationID, franchiseName, priority)

	body := fmt.Sprintf(`
<html><body>
<h2>New Franchise Enquiry Alert</h2>
<p><strong>Application ID:</strong> %s</p>
<p><strong>Franchise:</strong> %s</p>
<p><strong>Applicant:</strong> %s</p>
<p><strong>Budget:</strong> %s</p>
<p><strong>Priority:</strong> %s</p>
<p><strong>Submitted At:</strong> %s</p>
<p>Full details available in the admin dashboard.</p>
</body></html>`,
		applicationID, franchiseName, applicantName, budget, priority,
		time.Now().Format("2006-01-02 15:04:05 UTC"))

	return subject, body
}

func getStr(data map[string]interface{}, key, defaultVal string) string {
	if v, ok := data[key].(string); ok && v != "" {
		return v
	}
	return defaultVal
}
