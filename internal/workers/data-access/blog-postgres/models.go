package blogpostgres

import (
	"errors"
)

type BaseInput struct {
	OperationType string `json:"operation_type"`
}

type BaseOutput struct {
	ID          string `json:"id,omitempty"`
	Success     bool   `json:"success"`
	Message     string `json:"message"`
	RecipientID string `json:"recipient_id,omitempty"`
}

type CreateBlogInput struct {
	OperationType string `json:"operation_type"`

	// listings table fields
	Name             string  `json:"name"`
	Slug             string  `json:"slug"`
	ShortDescription string  `json:"short_description,omitempty"`
	Description      string  `json:"description,omitempty"`
	CreatedBy        string  `json:"created_by"`
	Status           string  `json:"status"` // e.g., 'PENDING_REVIEW', 'DRAFT'

	// blogs table fields
	ReadingTimeMins    int      `json:"reading_time_mins"`
	Content            string   `json:"content,omitempty"`
	SEOTitle           string   `json:"seo_title,omitempty"`
	SEODescription     string   `json:"seo_description,omitempty"`
	FeaturedImageURL   string   `json:"featured_image_url"`
	AuthorDisplayName  string   `json:"author_display_name,omitempty"`
	Tags               []string `json:"tags,omitempty"`
	AdditionalMediaURLs []string `json:"additional_media_urls,omitempty"`
	
	// author profile fields
	AuthorFullName     string   `json:"author_full_name,omitempty"`
	AuthorBio          string   `json:"author_bio,omitempty"`
	AuthorProfilePic   string   `json:"author_profile_pic,omitempty"`
	AuthorCategories   []string `json:"author_categories,omitempty"`
	
	// listing_categories
	CategoryIDs        []string `json:"category_ids"`
}

func (i *CreateBlogInput) Validate() error {
	if i.Name == "" {
		return errors.New("name is required")
	}
	if i.Slug == "" {
		return errors.New("slug is required")
	}
	if i.CreatedBy == "" {
		return errors.New("created_by is required")
	}
	if i.FeaturedImageURL == "" {
		return errors.New("featured_image_url is required")
	}
	if i.ReadingTimeMins < 0 {
		return errors.New("reading_time_mins must be >= 0")
	}
	if len(i.CategoryIDs) == 0 {
		return errors.New("at least one category is required")
	}
	return nil
}

type UpdateBlogInput struct {
	OperationType string `json:"operation_type"`
	BlogID        string `json:"blog_id"`
	UpdatedBy     string `json:"updated_by"` // user ID or admin ID
	
	Name             *string   `json:"name,omitempty"`
	ShortDescription *string   `json:"short_description,omitempty"`
	Description      *string   `json:"description,omitempty"`
	Status           *string   `json:"status,omitempty"`

	ReadingTimeMins    *int      `json:"reading_time_mins,omitempty"`
	Content            *string   `json:"content,omitempty"`
	SEOTitle           *string   `json:"seo_title,omitempty"`
	SEODescription     *string   `json:"seo_description,omitempty"`
	FeaturedImageURL   *string   `json:"featured_image_url,omitempty"`
	AuthorDisplayName  *string   `json:"author_display_name,omitempty"`
	Tags               *[]string `json:"tags,omitempty"`
	
	CategoryIDs        *[]string `json:"category_ids,omitempty"`
}

type CreateSubscriberInput struct {
	OperationType string `json:"operation_type"`
	Email         string `json:"email"`
	Source        string `json:"source,omitempty"`
}

type UpdateSubscriberInput struct {
	OperationType string `json:"operation_type"`
	Email         string `json:"email"`
	Status        string `json:"status"` // 'ACTIVE' or 'UNSUBSCRIBED'
}

type CreateFollowerInput struct {
	OperationType string `json:"operation_type"`
	FollowerID    string `json:"follower_id"`
	AuthorID      string `json:"author_id"`
}

type DeleteFollowerInput struct {
	OperationType string `json:"operation_type"`
	FollowerID    string `json:"follower_id"`
	AuthorID      string `json:"author_id"`
}
