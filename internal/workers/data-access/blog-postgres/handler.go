package blogpostgres

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	appErrs "camunda-workers/internal/common/errors"
	"camunda-workers/internal/common/logger"

	"github.com/camunda/zeebe/clients/go/v8/pkg/entities"
	"github.com/camunda/zeebe/clients/go/v8/pkg/worker"
	"github.com/google/uuid"
	"github.com/lib/pq"
	"go.opentelemetry.io/otel"
)

const (
	TaskType = "blog-postgres"
)

type Handler struct {
	db           *sql.DB
	logger       logger.Logger
	config       *Config
	errorHandler *appErrs.ErrorHandler
}

func NewHandler(db *sql.DB, logger logger.Logger, config *Config) *Handler {
	return &Handler{
		db:           db,
		logger:       logger.WithFields(map[string]interface{}{"taskType": TaskType}),
		config:       config,
		errorHandler: appErrs.NewErrorHandler(logger),
	}
}

func (h *Handler) HandleJob(client worker.JobClient, job entities.Job) {
	ctx := context.Background()
	tracer := otel.Tracer(TaskType)
	ctx, span := tracer.Start(ctx, "HandleJob")
	defer span.End()

	log := h.logger.WithFields(map[string]interface{}{
		"jobKey":       job.GetKey(),
		"workflowInst": job.GetProcessInstanceKey(),
	})
	log.Info("Processing blog-postgres job", nil)

	variables, err := job.GetVariablesAsMap()
	if err != nil {
		log.Error("Failed to parse variables", map[string]interface{}{"error": err.Error()})
		h.failJob(ctx, client, job, "INVALID_VARIABLES", err.Error())
		return
	}

	payloadJSON, ok := variables["payload"].(string)
	if !ok {
		h.failJob(ctx, client, job, "MISSING_PAYLOAD", "Payload string not found in variables")
		return
	}

	var baseInput BaseInput
	if err := json.Unmarshal([]byte(payloadJSON), &baseInput); err != nil {
		h.failJob(ctx, client, job, "INVALID_PAYLOAD", "Could not parse payload json")
		return
	}

	var output BaseOutput

	switch baseInput.OperationType {
	case "CREATE_BLOG":
		var input CreateBlogInput
		if err := json.Unmarshal([]byte(payloadJSON), &input); err != nil {
			h.failJob(ctx, client, job, "INVALID_INPUT", err.Error())
			return
		}
		if err := input.Validate(); err != nil {
			h.failJob(ctx, client, job, "VALIDATION_ERROR", err.Error())
			return
		}
		output, err = h.handleCreateBlog(ctx, input)
		if err != nil {
			h.failJob(ctx, client, job, "DB_ERROR", err.Error())
			return
		}
	case "UPDATE_BLOG":
		var input UpdateBlogInput
		if err := json.Unmarshal([]byte(payloadJSON), &input); err != nil {
			h.failJob(ctx, client, job, "INVALID_INPUT", err.Error())
			return
		}
		output, err = h.handleUpdateBlog(ctx, input)
		if err != nil {
			h.failJob(ctx, client, job, "DB_ERROR", err.Error())
			return
		}
	case "CREATE_SUBSCRIBER":
		var input CreateSubscriberInput
		if err := json.Unmarshal([]byte(payloadJSON), &input); err != nil {
			h.failJob(ctx, client, job, "INVALID_INPUT", err.Error())
			return
		}
		output, err = h.handleCreateSubscriber(ctx, input)
		if err != nil {
			h.failJob(ctx, client, job, "DB_ERROR", err.Error())
			return
		}
	case "UPDATE_SUBSCRIBER":
		var input UpdateSubscriberInput
		if err := json.Unmarshal([]byte(payloadJSON), &input); err != nil {
			h.failJob(ctx, client, job, "INVALID_INPUT", err.Error())
			return
		}
		output, err = h.handleUpdateSubscriber(ctx, input)
		if err != nil {
			h.failJob(ctx, client, job, "DB_ERROR", err.Error())
			return
		}
	case "CREATE_FOLLOWER":
		var input CreateFollowerInput
		if err := json.Unmarshal([]byte(payloadJSON), &input); err != nil {
			h.failJob(ctx, client, job, "INVALID_INPUT", err.Error())
			return
		}
		output, err = h.handleCreateFollower(ctx, input)
		if err != nil {
			h.failJob(ctx, client, job, "DB_ERROR", err.Error())
			return
		}
	case "DELETE_FOLLOWER":
		var input DeleteFollowerInput
		if err := json.Unmarshal([]byte(payloadJSON), &input); err != nil {
			h.failJob(ctx, client, job, "INVALID_INPUT", err.Error())
			return
		}
		output, err = h.handleDeleteFollower(ctx, input)
		if err != nil {
			h.failJob(ctx, client, job, "DB_ERROR", err.Error())
			return
		}
	default:
		h.failJob(ctx, client, job, "UNSUPPORTED_OPERATION", "Operation not supported")
		return
	}

	outBytes, _ := json.Marshal(output)
	variables["db_result"] = string(outBytes)
	variables["success"] = output.Success
	
	if !output.Success {
		variables["error_message"] = output.Message
	} else if baseInput.OperationType == "CREATE_BLOG" {
		variables["recipientId"] = output.RecipientID
		variables["notificationType"] = "blog_submitted"
		variables["recipientType"] = "seeker"
		variables["applicationId"] = output.ID

		// Parse CreateBlogInput to build ops email + MD attachment
		var blogInput CreateBlogInput
		_ = json.Unmarshal([]byte(payloadJSON), &blogInput)

		// Pass template variables for the user-facing send-notification email
		variables["blogTitle"] = blogInput.Name
		variables["authorName"] = blogInput.AuthorDisplayName

		// --- 1. Clean HTML metadata table for email body ---
		tags := strings.Join(blogInput.Tags, ", ")
		if tags == "" {
			tags = "—"
		}
		htmlBody := fmt.Sprintf(`<html><body style="font-family:Arial,sans-serif;max-width:700px;margin:auto">
<h2 style="color:#1a1a1a">📝 New Blog Submitted for Review</h2>
<p style="color:#555">A blog post has been submitted and is awaiting your approval before going live.</p>
<table border="1" cellpadding="10" cellspacing="0" style="border-collapse:collapse;width:100%%">
  <tr style="background:#f5f5f5"><td width="160"><b>Blog ID</b></td><td>%s</td></tr>
  <tr><td><b>Title</b></td><td>%s</td></tr>
  <tr style="background:#f5f5f5"><td><b>Author</b></td><td>%s</td></tr>
  <tr><td><b>SEO Title</b></td><td>%s</td></tr>
  <tr style="background:#f5f5f5"><td><b>Short Description</b></td><td>%s</td></tr>
  <tr><td><b>Tags</b></td><td>%s</td></tr>
  <tr style="background:#f5f5f5"><td><b>Reading Time</b></td><td>%d mins</td></tr>
  <tr><td><b>Status</b></td><td><b style="color:#e67e22">PENDING_REVIEW</b></td></tr>
</table>
<p style="margin-top:16px;color:#555">📎 The full blog content is attached as <b>blog-%s.md</b>. Please review it and update the status on the admin dashboard.</p>
</body></html>`,
			output.ID, blogInput.Name, blogInput.AuthorDisplayName,
			blogInput.SEOTitle, blogInput.ShortDescription, tags,
			blogInput.ReadingTimeMins, output.ID)
		variables["opsEmailHtml"] = htmlBody

		// --- 2. Build full blog content as .md attachment ---
		var mdBuilder strings.Builder
		fmt.Fprintf(&mdBuilder, "# %s\n\n", blogInput.Name)
		fmt.Fprintf(&mdBuilder, "**Author:** %s\n", blogInput.AuthorDisplayName)
		fmt.Fprintf(&mdBuilder, "**Author Bio:** %s\n", blogInput.AuthorBio)
		fmt.Fprintf(&mdBuilder, "**SEO Title:** %s\n", blogInput.SEOTitle)
		fmt.Fprintf(&mdBuilder, "**SEO Description:** %s\n", blogInput.SEODescription)
		fmt.Fprintf(&mdBuilder, "**Tags:** %s\n", tags)
		fmt.Fprintf(&mdBuilder, "**Reading Time:** %d mins\n", blogInput.ReadingTimeMins)
		fmt.Fprintf(&mdBuilder, "**Status:** PENDING_REVIEW\n")
		fmt.Fprintf(&mdBuilder, "**Blog ID:** %s\n", output.ID)
		fmt.Fprintf(&mdBuilder, "**Featured Image:** %s\n\n", blogInput.FeaturedImageURL)
		fmt.Fprintf(&mdBuilder, "---\n\n")
		fmt.Fprintf(&mdBuilder, "## Content\n\n%s\n", blogInput.Description)

		mdBase64 := base64.StdEncoding.EncodeToString([]byte(mdBuilder.String()))
		attachmentFilename := fmt.Sprintf("blog-%s.md", output.ID)

		// attachments is a JSON array compatible with email-send worker's expected format
		attachments := []map[string]string{
			{
				"filename":    attachmentFilename,
				"contentType": "text/markdown",
				"content":     mdBase64,
			},
		}
		attachmentsBytes, _ := json.Marshal(attachments)
		variables["opsAttachments"] = string(attachmentsBytes)
	}

	request, err := client.NewCompleteJobCommand().JobKey(job.GetKey()).VariablesFromMap(variables)
	if err != nil {
		log.Error("Failed to create complete command", map[string]interface{}{"error": err.Error()})
		return
	}

	ctx, cancel := context.WithTimeout(ctx, time.Duration(h.config.Timeout)*time.Second)
	defer cancel()
	
	if _, err := request.Send(ctx); err != nil {
		log.Error("Failed to complete job", map[string]interface{}{"error": err.Error()})
	} else {
		log.Info("Successfully completed job", map[string]interface{}{"operation": baseInput.OperationType})
	}
}

func (h *Handler) handleCreateBlog(ctx context.Context, input CreateBlogInput) (BaseOutput, error) {
	tx, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		return BaseOutput{}, err
	}
	defer tx.Rollback()

	newID := uuid.New().String()

	// 0. Upsert Author Profile if details are provided
	if input.AuthorFullName != "" || input.AuthorBio != "" || input.AuthorProfilePic != "" {
		_, err = tx.ExecContext(ctx, `
			INSERT INTO author_profiles (user_id, full_name, author_name, bio, profile_picture_url, categories)
			VALUES ($1, $2, $3, $4, $5, $6)
			ON CONFLICT (user_id) DO UPDATE SET
				full_name = EXCLUDED.full_name,
				author_name = EXCLUDED.author_name,
				bio = EXCLUDED.bio,
				profile_picture_url = EXCLUDED.profile_picture_url,
				categories = EXCLUDED.categories,
				updated_at = CURRENT_TIMESTAMP
		`, input.CreatedBy, input.AuthorFullName, input.AuthorDisplayName, input.AuthorBio, input.AuthorProfilePic, pq.Array(input.AuthorCategories))
		if err != nil {
			return BaseOutput{}, err
		}
	}

	// 1. Enforce PENDING_REVIEW status for new blogs
	input.Status = "PENDING_REVIEW"

	// 2. Insert into listings
	_, err = tx.ExecContext(ctx, `
		INSERT INTO listings (
			id, name, slug, short_description, description, 
			entity_type, status, created_by
		) VALUES ($1, $2, $3, $4, $5, 'blog', $6, $7)
	`, newID, input.Name, input.Slug, input.ShortDescription, input.Description, input.Status, input.CreatedBy)
	
	if err != nil {
		return BaseOutput{}, err
	}

	// 2. Insert into blogs
	_, err = tx.ExecContext(ctx, `
		INSERT INTO blogs (
			id, reading_time_mins, seo_title, seo_description, 
			featured_image_url, author_display_name, tags, additional_media_urls
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`, newID, input.ReadingTimeMins, input.SEOTitle, input.SEODescription, 
	input.FeaturedImageURL, input.AuthorDisplayName, pq.Array(input.Tags), pq.Array(input.AdditionalMediaURLs))
	
	if err != nil {
		return BaseOutput{}, err
	}

	// 3. Insert listing_categories
	for _, catID := range input.CategoryIDs {
		_, err = tx.ExecContext(ctx, `
			INSERT INTO listing_categories (listing_id, category_id)
			VALUES ($1, $2)
		`, newID, catID)
		if err != nil {
			return BaseOutput{}, err
		}
	}

	// 4. Initialize listing_stats
	_, err = tx.ExecContext(ctx, `
		INSERT INTO listing_stats (listing_id, view_count, follow_count)
		VALUES ($1, 0, 0)
	`, newID)
	if err != nil {
		return BaseOutput{}, err
	}

	if err := tx.Commit(); err != nil {
		return BaseOutput{}, err
	}

	return BaseOutput{
		ID:          newID,
		Success:     true,
		Message:     "Blog created successfully",
		RecipientID: input.CreatedBy,
	}, nil
}

func (h *Handler) handleUpdateBlog(ctx context.Context, input UpdateBlogInput) (BaseOutput, error) {
	tx, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		return BaseOutput{}, err
	}
	defer tx.Rollback()

	if input.Status != nil {
		_, err = tx.ExecContext(ctx, "UPDATE listings SET status = $1, updated_at = NOW() WHERE id = $2", *input.Status, input.BlogID)
		if err != nil {
			return BaseOutput{}, err
		}
	}

	if err := tx.Commit(); err != nil {
		return BaseOutput{}, err
	}

	return BaseOutput{Success: true, Message: "Blog updated successfully"}, nil
}

func (h *Handler) handleCreateSubscriber(ctx context.Context, input CreateSubscriberInput) (BaseOutput, error) {
	_, err := h.db.ExecContext(ctx, `
		INSERT INTO blog_subscribers (email, source)
		VALUES ($1, $2)
		ON CONFLICT (email) DO UPDATE SET status = 'ACTIVE', source = EXCLUDED.source, unsubscribed_at = NULL
	`, input.Email, input.Source)
	if err != nil {
		return BaseOutput{}, err
	}
	return BaseOutput{Success: true, Message: "Subscribed successfully"}, nil
}

func (h *Handler) handleUpdateSubscriber(ctx context.Context, input UpdateSubscriberInput) (BaseOutput, error) {
	var unsubscribedAt interface{}
	if input.Status == "UNSUBSCRIBED" {
		unsubscribedAt = time.Now()
	}
	_, err := h.db.ExecContext(ctx, `
		UPDATE blog_subscribers SET status = $1, unsubscribed_at = COALESCE($2, unsubscribed_at)
		WHERE email = $3
	`, input.Status, unsubscribedAt, input.Email)
	if err != nil {
		return BaseOutput{}, err
	}
	return BaseOutput{Success: true, Message: "Subscriber updated successfully"}, nil
}

func (h *Handler) handleCreateFollower(ctx context.Context, input CreateFollowerInput) (BaseOutput, error) {
	_, err := h.db.ExecContext(ctx, `
		INSERT INTO blog_author_followers (follower_id, author_id)
		VALUES ($1, $2)
		ON CONFLICT DO NOTHING
	`, input.FollowerID, input.AuthorID)
	if err != nil {
		return BaseOutput{}, err
	}
	return BaseOutput{Success: true, Message: "Followed successfully"}, nil
}

func (h *Handler) handleDeleteFollower(ctx context.Context, input DeleteFollowerInput) (BaseOutput, error) {
	_, err := h.db.ExecContext(ctx, `
		DELETE FROM blog_author_followers
		WHERE follower_id = $1 AND author_id = $2
	`, input.FollowerID, input.AuthorID)
	if err != nil {
		return BaseOutput{}, err
	}
	return BaseOutput{Success: true, Message: "Unfollowed successfully"}, nil
}

func (h *Handler) failJob(ctx context.Context, client worker.JobClient, job entities.Job, errorCode, errorMessage string) {
	h.logger.Error(errorMessage, map[string]interface{}{"code": errorCode, "job": job.GetKey()})
	
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	
	_, err := client.NewFailJobCommand().
		JobKey(job.GetKey()).
		Retries(job.GetRetries() - 1).
		ErrorMessage(errorMessage).
		Send(ctx)
		
	if err != nil {
		h.logger.Error("Failed to send fail command", map[string]interface{}{"error": err.Error()})
	}
}
