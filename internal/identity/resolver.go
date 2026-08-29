package identity

import (
	"context"
	"errors"
)

// AccountResolver resolves the accounts an identity session reaches. A
// session carries the provider-qualified subject an acquisition path
// resolved; this turns that subject into account IDs through the user's own
// grants and the grants of every team the user is on. It is the object the
// composition root hands the session gate, satisfying the gate's contract
// structurally the way the Authenticator satisfies its identity slot.
type AccountResolver struct {
	users  UserRepository
	grants AccountGrantRepository
}

// NewAccountResolver builds the resolver over the two repositories it reads.
func NewAccountResolver(users UserRepository, grants AccountGrantRepository) (*AccountResolver, error) {
	if users == nil || grants == nil {
		return nil, ErrRepositoryRequired
	}
	return &AccountResolver{users: users, grants: grants}, nil
}

// ReachableAccounts reports every account the subject's grants reach. A
// subject with no user — one whose user was removed while a session still
// lived — reaches nothing, which is a normal answer here, not a failure:
// the session stays valid and simply has no accounts behind it.
func (r *AccountResolver) ReachableAccounts(ctx context.Context, subject string) ([]string, error) {
	record, err := r.users.GetBySubject(ctx, subject)
	if errors.Is(err, ErrUserNotFound) {
		return []string{}, nil
	}
	if err != nil {
		return nil, err
	}
	return r.grants.ReachableAccounts(ctx, record.User.ID)
}
