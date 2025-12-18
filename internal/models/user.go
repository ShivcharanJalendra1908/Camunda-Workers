package models

import "time"

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
