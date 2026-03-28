package resolver

import (
	"context"
	"database/sql"
	"errors"

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

	// 1. Identity lookup — already exists?
	err = tx.QueryRow(ctx, `
        SELECT user_id FROM public.identities
        WHERE provider = $1 AND provider_user_id = $2
    `, identity.Provider, identity.ProviderUserID).Scan(&userID)

	if err == nil {
		// Existing user — seedha return
		return userID.String(), tx.Commit()
	}
	if err != sql.ErrNoRows {
		return "", err
	}

	// 2. Email-based linking — same email se aaya hai?
	err = tx.QueryRow(ctx, `
        SELECT id FROM public.users
        WHERE email = $1 FOR UPDATE
    `, identity.Email).Scan(&userID)

	if err == nil {
		// User hai, sirf identity link karo
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

	// 3. Bilkul naya user — create karo
	err = tx.QueryRow(ctx, `
        INSERT INTO public.users (email, email_verified)
        VALUES ($1, $2)
        ON CONFLICT (email) DO UPDATE SET email = EXCLUDED.email
        RETURNING id
    `, identity.Email, identity.EmailVerified).Scan(&userID)
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

	// 5. Free subscription — naye user ko automatically free tier
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

// package resolver

// import (
// 	"context"
// 	"database/sql"
// 	"errors"

// 	auth "camunda-workers/internal/common/auth/types"
// 	"camunda-workers/internal/common/database"

// 	"github.com/google/uuid"
// )

// // Resolver interface
// type Resolver interface {
// 	Resolve(ctx context.Context, identity *auth.Identity) (string, error)
// }

// // DBResolver resolves identities using the database.
// type DBResolver struct {
// 	db *database.PostgresClient
// }

// func NewDBResolver(db *database.PostgresClient) *DBResolver {
// 	return &DBResolver{db: db}
// }

// func (r *DBResolver) Resolve(
// 	ctx context.Context,
// 	identity *auth.Identity,
// ) (string, error) {

// 	if identity == nil {
// 		return "", errors.New("identity is nil")
// 	}

// 	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{
// 		Isolation: sql.LevelReadCommitted,
// 	})
// 	if err != nil {
// 		return "", err
// 	}
// 	defer tx.Rollback()

// 	var userID uuid.UUID

// 	// 1. Identity lookup
// 	err = tx.QueryRow(ctx, `
//         SELECT user_id FROM public.identities
//         WHERE provider = $1 AND provider_user_id = $2
//     `, identity.Provider, identity.ProviderUserID).Scan(&userID)

// 	if err == nil {
// 		return userID.String(), tx.Commit()
// 	}
// 	if err != sql.ErrNoRows {
// 		return "", err
// 	}

// 	// 2. Email-based linking — FOR UPDATE lock
// 	err = tx.QueryRow(ctx, `
//         SELECT id FROM public.users
//         WHERE email = $1 FOR UPDATE
//     `, identity.Email).Scan(&userID)

// 	if err == nil {
// 		_, err = tx.Exec(ctx, `
//             INSERT INTO public.identities (user_id, provider, provider_user_id)
//             VALUES ($1, $2, $3)
//             ON CONFLICT (provider, provider_user_id) DO NOTHING
//         `, userID, identity.Provider, identity.ProviderUserID)
// 		if err != nil {
// 			return "", err
// 		}
// 		return userID.String(), tx.Commit()
// 	}
// 	if err != sql.ErrNoRows {
// 		return "", err
// 	}

// 	// 3. New user
// 	err = tx.QueryRow(ctx, `
//         INSERT INTO public.users (email, email_verified)
//         VALUES ($1, $2)
//         ON CONFLICT (email) DO UPDATE SET email = EXCLUDED.email
//         RETURNING id
//     `, identity.Email, identity.EmailVerified).Scan(&userID)
// 	if err != nil {
// 		return "", err
// 	}

// 	// 4. Identity mapping
// 	_, err = tx.Exec(ctx, `
//         INSERT INTO public.identities (user_id, provider, provider_user_id)
//         VALUES ($1, $2, $3)
//         ON CONFLICT (provider, provider_user_id) DO NOTHING
//     `, userID, identity.Provider, identity.ProviderUserID)
// 	if err != nil {
// 		return "", err
// 	}

// 	return userID.String(), tx.Commit()
// }
