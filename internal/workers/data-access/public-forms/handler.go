package publicforms

import (
	"context"
	"encoding/json"
	"regexp"

	"camunda-workers/internal/common/database"
	"camunda-workers/internal/common/logger"

	"github.com/camunda/zeebe/clients/go/v8/pkg/entities"
	"github.com/camunda/zeebe/clients/go/v8/pkg/worker"
	ozzo "github.com/go-ozzo/ozzo-validation/v4"
	"github.com/go-ozzo/ozzo-validation/v4/is"
)

type PublicFormWorker struct {
	logger logger.Logger
	db     *database.PostgresClient
	config *Config
}

func NewPublicFormWorker(logger logger.Logger, db *database.PostgresClient, config *Config) *PublicFormWorker {
	if config == nil {
		config = DefaultConfig()
	}
	return &PublicFormWorker{
		logger: logger,
		db:     db,
		config: config,
	}
}

func (w *PublicFormWorker) HandleValidateForm(client worker.JobClient, job entities.Job) {
	w.logger.Info("Validating public form submission", map[string]interface{}{
		"jobKey": job.GetKey(),
	})

	var variables ValidationVariables
	if err := job.GetVariablesAs(&variables); err != nil {
		w.logger.Error("Failed to parse variables", map[string]interface{}{"error": err.Error()})
		client.NewFailJobCommand().JobKey(job.GetKey()).Retries(0).ErrorMessage("Invalid variables").Send(context.Background())
		return
	}

	isValid := true
	validationErrors := make(map[string]string)

	var email, phone, message string
	if e, ok := variables.FormData["email"].(string); ok {
		email = e
	} else if ea, ok := variables.FormData["emailAddress"].(string); ok {
		email = ea
	}

	if p, ok := variables.FormData["phone"].(string); ok {
		phone = p
	} else if pn, ok := variables.FormData["phoneNumber"].(string); ok {
		phone = pn
	}

	if m, ok := variables.FormData["message"].(string); ok {
		message = m
	}

	// Validate email
	if err := ozzo.Validate(email, ozzo.Required, is.Email); err != nil {
		isValid = false
		validationErrors["email"] = "Invalid email format"
	}

	// Validate phone
	if phone != "" {
		if err := ozzo.Validate(phone, ozzo.Match(regexp.MustCompile(`^[\d\s\+\-\(\)]*$`))); err != nil {
			isValid = false
			validationErrors["phone"] = "Invalid phone number format"
		}
	}

	// Contact Us specific validation
	if variables.FormType == "contact_us" {
		if err := ozzo.Validate(message, ozzo.Required, ozzo.Length(10, 5000)); err != nil {
			isValid = false
			validationErrors["message"] = "Message is required and must be between 10 and 5000 characters"
		}
	}

	response := ValidationResponse{
		IsValid:          isValid,
		ValidationErrors: validationErrors,
	}

	_, err := client.NewCompleteJobCommand().JobKey(job.GetKey()).VariablesFromObject(response)
	if err != nil {
		w.logger.Error("Failed to complete validation job", map[string]interface{}{"error": err.Error()})
		return
	}

	w.logger.Info("Validation complete", map[string]interface{}{"isValid": isValid})
}

func (w *PublicFormWorker) HandleSaveForm(client worker.JobClient, job entities.Job) {
	w.logger.Info("Saving public form submission", map[string]interface{}{
		"jobKey": job.GetKey(),
	})

	var variables ValidationVariables
	if err := job.GetVariablesAs(&variables); err != nil {
		w.logger.Error("Failed to parse variables", map[string]interface{}{"error": err.Error()})
		client.NewFailJobCommand().JobKey(job.GetKey()).Retries(0).ErrorMessage("Invalid variables").Send(context.Background())
		return
	}

	var email, phone string
	if e, ok := variables.FormData["email"].(string); ok {
		email = e
	} else if ea, ok := variables.FormData["emailAddress"].(string); ok {
		email = ea
	}

	if p, ok := variables.FormData["phone"].(string); ok {
		phone = p
	} else if pn, ok := variables.FormData["phoneNumber"].(string); ok {
		phone = pn
	}

	formDataJSON, err := json.Marshal(variables.FormData)
	if err != nil {
		w.logger.Error("Failed to marshal form data", map[string]interface{}{"error": err.Error()})
		client.NewFailJobCommand().JobKey(job.GetKey()).Retries(job.GetRetries()-1).ErrorMessage("Internal error").Send(context.Background())
		return
	}

	var submissionID string
	query := `
		INSERT INTO public_form_submissions (form_type, email, phone, form_data)
		VALUES ($1, $2, $3, $4)
		RETURNING id
	`

	err = w.db.QueryRow(context.Background(), query, variables.FormType, email, phone, formDataJSON).Scan(&submissionID)
	if err != nil {
		w.logger.Error("Database insertion failed", map[string]interface{}{"error": err.Error()})
		client.NewFailJobCommand().JobKey(job.GetKey()).Retries(job.GetRetries()-1).ErrorMessage("Database error").Send(context.Background())
		return
	}

	response := SaveResponse{
		SubmissionID: submissionID,
	}

	_, err = client.NewCompleteJobCommand().JobKey(job.GetKey()).VariablesFromObject(response)
	if err != nil {
		w.logger.Error("Failed to complete save job", map[string]interface{}{"error": err.Error()})
	} else {
		w.logger.Info("Form saved successfully", map[string]interface{}{"submissionId": submissionID})
	}
}
