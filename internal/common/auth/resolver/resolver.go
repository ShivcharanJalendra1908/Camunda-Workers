package resolver

import (
	"context"

	auth "camunda-workers/internal/common/auth/types"
)

// Resolver determines which internal user an external identity belongs to.
// It is the ONLY place where identity-to-user mapping logic lives.
type Resolver interface {
	Resolve(
		ctx context.Context,
		identity *auth.Identity,
	) (userID string, err error)
}
