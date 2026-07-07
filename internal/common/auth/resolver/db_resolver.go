package resolver

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	auth "camunda-workers/internal/common/auth/types"
	"camunda-workers/internal/common/database"

	"github.com/google/uuid"
)

// Resolver interface
type Resolver interface {
	Resolve(ctx context.Context, identity *auth.Identity) (string, error)
}

// DBResolver resolves identities using the database.
type DBResolver struct {
	db *database.PostgresClient
}

func NewDBResolver(db *database.PostgresClient) *DBResolver {
	return &DBResolver{db: db}
}

func (r *DBResolver) Resolve(
	ctx context.Context,
	identity *auth.Identity,
) (string, error) {

	if identity == nil {
		return "", errors.New("identity is nil")
	}

	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{
		Isolation: sql.LevelReadCommitted,
	})
	if err != nil {
		return "", err
	}
	defer tx.Rollback()

	var userID uuid.UUID

	// 1. Identity lookup â€” already exists?
	err = tx.QueryRow(ctx, `
        SELECT user_id FROM public.identities
        WHERE provider = $1 AND provider_user_id = $2
    `, identity.Provider, identity.ProviderUserID).Scan(&userID)

	if err == nil {
		// Existing user â€” check if name is missing and update if needed
		fullName := strings.TrimSpace(identity.FirstName + " " + identity.LastName)
		if fullName != "" {
			_, err = tx.Exec(ctx, `
				UPDATE public.users 
				SET name = $1 
				WHERE id = $2 AND (name IS NULL OR name = '')
			`, fullName, userID)
			if err != nil {
				return "", err
			}
		}
		return userID.String(), tx.Commit()
	}
	if err != sql.ErrNoRows {
		return "", err
	}

	// 2. Email-based linking â€” same email se aaya hai?
	err = tx.QueryRow(ctx, `
        SELECT id FROM public.users
        WHERE email = $1 FOR UPDATE
    `, identity.Email).Scan(&userID)

	if err == nil {
		// User hai, link identity and update name if missing
		fullName := strings.TrimSpace(identity.FirstName + " " + identity.LastName)
		_, err = tx.Exec(ctx, `
            UPDATE public.users 
            SET name = $1 
            WHERE id = $2 AND (name IS NULL OR name = '')
        `, fullName, userID)
		if err != nil {
			return "", err
		}

		_, err = tx.Exec(ctx, `
            INSERT INTO public.identities (user_id, provider, provider_user_id)
            VALUES ($1, $2, $3)
            ON CONFLICT (provider, provider_user_id) DO NOTHING
        `, userID, identity.Provider, identity.ProviderUserID)
		if err != nil {
			return "", err
		}
		return userID.String(), tx.Commit()
	}
	if err != sql.ErrNoRows {
		return "", err
	}

	// 3. Bilkul naya user â€” create karo
	fullName := strings.TrimSpace(identity.FirstName + " " + identity.LastName)
	err = tx.QueryRow(ctx, `
        INSERT INTO public.users (email, email_verified, name)
        VALUES ($1, $2, $3)
        ON CONFLICT (email) DO UPDATE SET 
            email = EXCLUDED.email,
            name = CASE 
                WHEN users.name IS NULL OR users.name = '' THEN EXCLUDED.name 
                ELSE users.name 
            END
        RETURNING id
    `, identity.Email, identity.EmailVerified, fullName).Scan(&userID)
	if err != nil {
		return "", err
	}

	// 4. Identity mapping
	_, err = tx.Exec(ctx, `
        INSERT INTO public.identities (user_id, provider, provider_user_id)
        VALUES ($1, $2, $3)
        ON CONFLICT (provider, provider_user_id) DO NOTHING
    `, userID, identity.Provider, identity.ProviderUserID)
	if err != nil {
		return "", err
	}

	// 5. Free subscription â€” naye user ko automatically free tier
	_, err = tx.Exec(ctx, `
        INSERT INTO public.user_subscriptions (user_id, tier, is_valid)
        VALUES ($1, 'free', true)
        ON CONFLICT (user_id, tier) DO NOTHING
    `, userID)
	if err != nil {
		return "", err
	}

	return userID.String(), tx.Commit()
}
