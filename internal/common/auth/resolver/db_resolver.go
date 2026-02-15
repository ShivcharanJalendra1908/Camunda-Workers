package resolver

import (
	"context"
	"database/sql"
	"errors"

	auth "camunda-workers/internal/common/auth/types"
	"camunda-workers/internal/common/database"

	"github.com/google/uuid"
)

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

	// 1. Try identity lookup (provider + provider_user_id)
	var userID uuid.UUID
	err := r.db.QueryRowContext(ctx, `
		SELECT user_id
		FROM public.identities
		WHERE provider = $1
		  AND provider_user_id = $2
	`,
		identity.Provider,
		identity.ProviderUserID,
	).Scan(&userID)

	if err == nil {
		return userID.String(), nil
	}

	if err != sql.ErrNoRows {
		return "", err
	}

	// 2. Try email-based linking (existing user, new provider)
	err = r.db.QueryRowContext(ctx, `
		SELECT id
		FROM public.users
		WHERE email = $1
	`,
		identity.Email,
	).Scan(&userID)

	if err == nil {
		// Link new identity to existing user
		_, err = r.db.ExecContext(ctx, `
			INSERT INTO public.identities (user_id, provider, provider_user_id)
			VALUES ($1, $2, $3)
		`,
			userID,
			identity.Provider,
			identity.ProviderUserID,
		)
		if err != nil {
			return "", err
		}

		return userID.String(), nil
	}

	if err != sql.ErrNoRows {
		return "", err
	}

	// 3. Create new user
	err = r.db.QueryRowContext(ctx, `
		INSERT INTO public.users (email, email_verified)
		VALUES ($1, $2)
		RETURNING id
	`,
		identity.Email,
		identity.EmailVerified,
	).Scan(&userID)

	if err != nil {
		return "", err
	}

	// 4. Create identity mapping
	_, err = r.db.ExecContext(ctx, `
		INSERT INTO public.identities (user_id, provider, provider_user_id)
		VALUES ($1, $2, $3)
	`,
		userID,
		identity.Provider,
		identity.ProviderUserID,
	)

	if err != nil {
		return "", err
	}

	return userID.String(), nil
}
