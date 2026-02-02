// internal/models/user.go
package models

import (
	"regexp"
	"strings"
	"time"

	ozzo "github.com/go-ozzo/ozzo-validation/v4"
	"github.com/go-ozzo/ozzo-validation/v4/is"
)

// User represents a user in the system
type User struct {
	ID               string                 `json:"id" db:"id"`
	Email            string                 `json:"email" db:"email"`
	Name             string                 `json:"name" db:"name"`
	FirstName        string                 `json:"firstName,omitempty" db:"first_name"`
	LastName         string                 `json:"lastName,omitempty" db:"last_name"`
	ProfileImage     string                 `json:"profileImage,omitempty" db:"profile_image"`
	EmailVerified    bool                   `json:"emailVerified" db:"email_verified"`
	Phone            string                 `json:"phone,omitempty" db:"phone"`
	PhoneVerified    bool                   `json:"phoneVerified" db:"phone_verified"`
	Company          string                 `json:"company,omitempty" db:"company"`
	JobTitle         string                 `json:"jobTitle,omitempty" db:"job_title"`
	Timezone         string                 `json:"timezone,omitempty" db:"timezone"`
	Locale           string                 `json:"locale,omitempty" db:"locale"`
	Status           string                 `json:"status" db:"status"` // active, inactive, suspended, pending
	Role             string                 `json:"role" db:"role"`     // user, admin, super_admin
	Permissions      []string               `json:"permissions,omitempty" db:"permissions"`
	LastLogin        *time.Time             `json:"lastLogin,omitempty" db:"last_login"`
	LastIP           string                 `json:"lastIp,omitempty" db:"last_ip"`
	LoginCount       int                    `json:"loginCount" db:"login_count"`
	FailedLoginCount int                    `json:"failedLoginCount" db:"failed_login_count"`
	LockedUntil      *time.Time             `json:"lockedUntil,omitempty" db:"locked_until"`
	CreatedAt        time.Time              `json:"createdAt" db:"created_at"`
	UpdatedAt        time.Time              `json:"updatedAt" db:"updated_at"`
	Metadata         map[string]interface{} `json:"metadata,omitempty" db:"metadata"`
}

// Validate validates the User struct
func (u User) Validate() error {
	return ozzo.ValidateStruct(&u,
		// ID validation
		ozzo.Field(&u.ID,
			ozzo.When(u.ID != "", ozzo.Required, ozzo.Length(36, 36),
				ozzo.Match(regexp.MustCompile(`^[a-fA-F0-9]{8}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{12}$`))).
				Else(ozzo.Skip)),

		// Email validation
		ozzo.Field(&u.Email,
			ozzo.Required,
			is.Email,
			ozzo.Length(5, 255)),

		// Name validation
		ozzo.Field(&u.Name,
			ozzo.Required,
			ozzo.Length(2, 200),
			ozzo.Match(regexp.MustCompile(`^[a-zA-Z\s\-\.']+$`))),

		// First name validation
		ozzo.Field(&u.FirstName,
			ozzo.Length(0, 100),
			ozzo.When(u.FirstName != "", ozzo.Match(regexp.MustCompile(`^[a-zA-Z\s\-\.']+$`))).
				Else(ozzo.Skip)),

		// Last name validation
		ozzo.Field(&u.LastName,
			ozzo.Length(0, 100),
			ozzo.When(u.LastName != "", ozzo.Match(regexp.MustCompile(`^[a-zA-Z\s\-\.']+$`))).
				Else(ozzo.Skip)),

		// Phone validation
		ozzo.Field(&u.Phone,
			ozzo.Length(0, 20),
			ozzo.When(u.Phone != "", ozzo.Match(regexp.MustCompile(`^[\d\s\+\-\(\)]*$`))).
				Else(ozzo.Skip)),

		// Company validation
		ozzo.Field(&u.Company,
			ozzo.Length(0, 200)),

		// Job title validation
		ozzo.Field(&u.JobTitle,
			ozzo.Length(0, 100)),

		// Timezone validation
		ozzo.Field(&u.Timezone,
			ozzo.Length(0, 50)),

		// Locale validation
		ozzo.Field(&u.Locale,
			ozzo.Length(0, 10),
			ozzo.When(u.Locale != "", ozzo.Match(regexp.MustCompile(`^[a-z]{2}(-[A-Z]{2})?$`))).
				Else(ozzo.Skip)),

		// Status validation
		ozzo.Field(&u.Status,
			ozzo.Required,
			ozzo.In("active", "inactive", "suspended", "pending")),

		// Role validation
		ozzo.Field(&u.Role,
			ozzo.Required,
			ozzo.In("user", "admin", "super_admin")),

		// Last IP validation
		ozzo.Field(&u.LastIP,
			ozzo.Length(0, 45),
			ozzo.When(u.LastIP != "", is.IP).
				Else(ozzo.Skip)),

		// Login count validation
		ozzo.Field(&u.LoginCount,
			ozzo.Min(0)),

		// Failed login count validation
		ozzo.Field(&u.FailedLoginCount,
			ozzo.Min(0)),

		// Profile image URL validation
		ozzo.Field(&u.ProfileImage,
			ozzo.Length(0, 500),
			ozzo.When(u.ProfileImage != "", is.URL).
				Else(ozzo.Skip)),
	)
}

// ValidateForCreate validates the User struct for creation
func (u User) ValidateForCreate() error {
	// First, validate basic fields
	if err := u.Validate(); err != nil {
		return err
	}

	// Additional validation for creation
	return ozzo.ValidateStruct(&u,
		// Ensure email is provided for new users
		ozzo.Field(&u.Email,
			ozzo.Required),

		// Ensure name is provided for new users
		ozzo.Field(&u.Name,
			ozzo.Required),

		// Ensure status is valid for new users
		ozzo.Field(&u.Status,
			ozzo.In("pending", "active")),
	)
}

// ValidateForUpdate validates the User struct for updates
func (u User) ValidateForUpdate() error {
	// First, validate basic fields
	if err := u.Validate(); err != nil {
		return err
	}

	// Additional validation for updates
	return ozzo.ValidateStruct(&u,
		// Ensure ID is provided for updates
		ozzo.Field(&u.ID,
			ozzo.Required),
	)
}

// Sanitize sanitizes the User struct fields
func (u *User) Sanitize() {
	// Trim whitespace from string fields
	u.ID = strings.TrimSpace(u.ID)
	u.Email = strings.TrimSpace(u.Email)
	u.Name = strings.TrimSpace(u.Name)
	u.FirstName = strings.TrimSpace(u.FirstName)
	u.LastName = strings.TrimSpace(u.LastName)
	u.Phone = strings.TrimSpace(u.Phone)
	u.Company = strings.TrimSpace(u.Company)
	u.JobTitle = strings.TrimSpace(u.JobTitle)
	u.Timezone = strings.TrimSpace(u.Timezone)
	u.Locale = strings.TrimSpace(u.Locale)
	u.LastIP = strings.TrimSpace(u.LastIP)
	u.ProfileImage = strings.TrimSpace(u.ProfileImage)

	// Convert email to lowercase
	u.Email = strings.ToLower(u.Email)

	// Convert locale to lowercase
	u.Locale = strings.ToLower(u.Locale)

	// Remove any HTML tags
	u.Name = removeHTMLTags(u.Name)
	u.FirstName = removeHTMLTags(u.FirstName)
	u.LastName = removeHTMLTags(u.LastName)
	u.Company = removeHTMLTags(u.Company)
	u.JobTitle = removeHTMLTags(u.JobTitle)
}

// Helper function to remove HTML tags
func removeHTMLTags(s string) string {
	// Simple regex to remove HTML tags
	re := regexp.MustCompile(`<[^>]*>`)
	return re.ReplaceAllString(s, "")
}

// UserSubscription represents user subscription information
type UserSubscription struct {
	ID                   string    `json:"id" db:"id"`
	UserID               string    `json:"userId" db:"user_id"`
	SubscriptionTier     string    `json:"subscriptionTier" db:"subscription_tier"`
	TierLevel            string    `json:"tierLevel" db:"tier_level"`
	IsValid              bool      `json:"isValid" db:"is_valid"`
	CurrentPeriodEnd     time.Time `json:"currentPeriodEnd" db:"current_period_end"`
	CancelAtPeriodEnd    bool      `json:"cancelAtPeriodEnd" db:"cancel_at_period_end"`
	StripeCustomerID     string    `json:"stripeCustomerId,omitempty" db:"stripe_customer_id"`
	StripeSubscriptionID string    `json:"stripeSubscriptionId,omitempty" db:"stripe_subscription_id"`
	CreatedAt            time.Time `json:"createdAt" db:"created_at"`
	UpdatedAt            time.Time `json:"updatedAt" db:"updated_at"`
}

// Validate validates the UserSubscription struct
func (us UserSubscription) Validate() error {
	return ozzo.ValidateStruct(&us,
		// ID validation
		ozzo.Field(&us.ID,
			ozzo.When(us.ID != "", ozzo.Required, ozzo.Length(36, 36),
				ozzo.Match(regexp.MustCompile(`^[a-fA-F0-9]{8}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{12}$`))).
				Else(ozzo.Skip)),

		// User ID validation
		ozzo.Field(&us.UserID,
			ozzo.Required,
			ozzo.Length(36, 36),
			ozzo.Match(regexp.MustCompile(`^[a-fA-F0-9]{8}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{12}$`))),

		// Subscription tier validation
		ozzo.Field(&us.SubscriptionTier,
			ozzo.Required,
			ozzo.Length(1, 50)),

		// Tier level validation
		ozzo.Field(&us.TierLevel,
			ozzo.Required,
			ozzo.In("free", "basic", "premium", "enterprise")),

		// Stripe customer ID validation
		ozzo.Field(&us.StripeCustomerID,
			ozzo.Length(0, 100)),

		// Stripe subscription ID validation
		ozzo.Field(&us.StripeSubscriptionID,
			ozzo.Length(0, 100)),

		// Date validation
		ozzo.Field(&us.CurrentPeriodEnd,
			ozzo.Required),
	)
}

// UserPreferences represents user preferences
type UserPreferences struct {
	UserID               string          `json:"userId" db:"user_id"`
	EmailNotifications   bool            `json:"emailNotifications" db:"email_notifications"`
	PushNotifications    bool            `json:"pushNotifications" db:"push_notifications"`
	SMSNotifications     bool            `json:"smsNotifications" db:"sms_notifications"`
	Theme                string          `json:"theme" db:"theme"` // light, dark, auto
	Language             string          `json:"language" db:"language"`
	Timezone             string          `json:"timezone" db:"timezone"`
	NotificationSettings map[string]bool `json:"notificationSettings" db:"notification_settings"`
	UpdatedAt            time.Time       `json:"updatedAt" db:"updated_at"`
}

// Validate validates the UserPreferences struct
func (up UserPreferences) Validate() error {
	return ozzo.ValidateStruct(&up,
		// User ID validation
		ozzo.Field(&up.UserID,
			ozzo.Required,
			ozzo.Length(36, 36),
			ozzo.Match(regexp.MustCompile(`^[a-fA-F0-9]{8}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{12}$`))),

		// Theme validation
		ozzo.Field(&up.Theme,
			ozzo.In("light", "dark", "auto")),

		// Language validation
		ozzo.Field(&up.Language,
			ozzo.Length(0, 10),
			ozzo.When(up.Language != "", ozzo.Match(regexp.MustCompile(`^[a-z]{2}(-[A-Z]{2})?$`))).
				Else(ozzo.Skip)),

		// Timezone validation
		ozzo.Field(&up.Timezone,
			ozzo.Length(0, 50)),

		// Updated at validation
		ozzo.Field(&up.UpdatedAt,
			ozzo.Required),
	)
}

// UserRepository defines user data access interface
type UserRepository interface {
	Create(user *User) error
	FindByID(id string) (*User, error)
	FindByEmail(email string) (*User, error)
	FindByPhone(phone string) (*User, error)
	Update(user *User) error
	Delete(id string) error
	List(limit, offset int) ([]*User, error)
	Count() (int, error)
	UpdateLastLogin(userID, ipAddress string) error
	IncrementLoginCount(userID string) error
	UpdateStatus(userID, status string) error
	FindByStatus(status string) ([]*User, error)
}

// UserService defines business logic for user operations
type UserService interface {
	Register(user *User) error
	Login(email, password string) (*User, *Session, error)
	Logout(sessionID string) error
	GetProfile(userID string) (*User, error)
	UpdateProfile(userID string, updates map[string]interface{}) error
	ChangePassword(userID, oldPassword, newPassword string) error
	ResetPassword(email string) error
	VerifyEmail(token string) error
	UpdateSubscription(userID string, subscription *UserSubscription) error
	GetUserSubscription(userID string) (*UserSubscription, error)
}

// package models

// import "time"

// // User represents a user in the system
// type User struct {
// 	ID               string                 `json:"id" db:"id"`
// 	Email            string                 `json:"email" db:"email"`
// 	Name             string                 `json:"name" db:"name"`
// 	FirstName        string                 `json:"firstName,omitempty" db:"first_name"`
// 	LastName         string                 `json:"lastName,omitempty" db:"last_name"`
// 	ProfileImage     string                 `json:"profileImage,omitempty" db:"profile_image"`
// 	EmailVerified    bool                   `json:"emailVerified" db:"email_verified"`
// 	Phone            string                 `json:"phone,omitempty" db:"phone"`
// 	PhoneVerified    bool                   `json:"phoneVerified" db:"phone_verified"`
// 	Company          string                 `json:"company,omitempty" db:"company"`
// 	JobTitle         string                 `json:"jobTitle,omitempty" db:"job_title"`
// 	Timezone         string                 `json:"timezone,omitempty" db:"timezone"`
// 	Locale           string                 `json:"locale,omitempty" db:"locale"`
// 	Status           string                 `json:"status" db:"status"` // active, inactive, suspended, pending
// 	Role             string                 `json:"role" db:"role"`     // user, admin, super_admin
// 	Permissions      []string               `json:"permissions,omitempty" db:"permissions"`
// 	LastLogin        *time.Time             `json:"lastLogin,omitempty" db:"last_login"`
// 	LastIP           string                 `json:"lastIp,omitempty" db:"last_ip"`
// 	LoginCount       int                    `json:"loginCount" db:"login_count"`
// 	FailedLoginCount int                    `json:"failedLoginCount" db:"failed_login_count"`
// 	LockedUntil      *time.Time             `json:"lockedUntil,omitempty" db:"locked_until"`
// 	CreatedAt        time.Time              `json:"createdAt" db:"created_at"`
// 	UpdatedAt        time.Time              `json:"updatedAt" db:"updated_at"`
// 	Metadata         map[string]interface{} `json:"metadata,omitempty" db:"metadata"`
// }

// // UserSubscription represents user subscription information
// type UserSubscription struct {
// 	ID                   string    `json:"id" db:"id"`
// 	UserID               string    `json:"userId" db:"user_id"`
// 	SubscriptionTier     string    `json:"subscriptionTier" db:"subscription_tier"`
// 	TierLevel            string    `json:"tierLevel" db:"tier_level"`
// 	IsValid              bool      `json:"isValid" db:"is_valid"`
// 	CurrentPeriodEnd     time.Time `json:"currentPeriodEnd" db:"current_period_end"`
// 	CancelAtPeriodEnd    bool      `json:"cancelAtPeriodEnd" db:"cancel_at_period_end"`
// 	StripeCustomerID     string    `json:"stripeCustomerId,omitempty" db:"stripe_customer_id"`
// 	StripeSubscriptionID string    `json:"stripeSubscriptionId,omitempty" db:"stripe_subscription_id"`
// 	CreatedAt            time.Time `json:"createdAt" db:"created_at"`
// 	UpdatedAt            time.Time `json:"updatedAt" db:"updated_at"`
// }

// // UserPreferences represents user preferences
// type UserPreferences struct {
// 	UserID               string          `json:"userId" db:"user_id"`
// 	EmailNotifications   bool            `json:"emailNotifications" db:"email_notifications"`
// 	PushNotifications    bool            `json:"pushNotifications" db:"push_notifications"`
// 	SMSNotifications     bool            `json:"smsNotifications" db:"sms_notifications"`
// 	Theme                string          `json:"theme" db:"theme"` // light, dark, auto
// 	Language             string          `json:"language" db:"language"`
// 	Timezone             string          `json:"timezone" db:"timezone"`
// 	NotificationSettings map[string]bool `json:"notificationSettings" db:"notification_settings"`
// 	UpdatedAt            time.Time       `json:"updatedAt" db:"updated_at"`
// }

// // UserRepository defines user data access interface
// type UserRepository interface {
// 	Create(user *User) error
// 	FindByID(id string) (*User, error)
// 	FindByEmail(email string) (*User, error)
// 	FindByPhone(phone string) (*User, error)
// 	Update(user *User) error
// 	Delete(id string) error
// 	List(limit, offset int) ([]*User, error)
// 	Count() (int, error)
// 	UpdateLastLogin(userID, ipAddress string) error
// 	IncrementLoginCount(userID string) error
// 	UpdateStatus(userID, status string) error
// 	FindByStatus(status string) ([]*User, error)
// }

// // UserService defines business logic for user operations
// type UserService interface {
// 	Register(user *User) error
// 	Login(email, password string) (*User, *Session, error)
// 	Logout(sessionID string) error
// 	GetProfile(userID string) (*User, error)
// 	UpdateProfile(userID string, updates map[string]interface{}) error
// 	ChangePassword(userID, oldPassword, newPassword string) error
// 	ResetPassword(email string) error
// 	VerifyEmail(token string) error
// 	UpdateSubscription(userID string, subscription *UserSubscription) error
// 	GetUserSubscription(userID string) (*UserSubscription, error)
// }
