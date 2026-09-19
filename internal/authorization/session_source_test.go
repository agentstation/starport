package authorization

import (
	"context"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/identity"
	"github.com/stretchr/testify/require"
)

type userFunc func(context.Context, string) (identity.UserRecord, error)

func (f userFunc) GetBySubject(ctx context.Context, subject string) (identity.UserRecord, error) {
	return f(ctx, subject)
}

type grantFunc func(context.Context, string, string) (string, error)

func (f grantFunc) ResolveAccount(ctx context.Context, user, selected string) (string, error) {
	return f(ctx, user, selected)
}

func TestSessionProjectionWarmReadsAvoidPolicyStorage(t *testing.T) {
	source, reads := sourceFixture(t)
	userReads, grantReads := 0, 0
	source.users = userFunc(func(context.Context, string) (identity.UserRecord, error) {
		userReads++
		return identity.UserRecord{Revision: 7, User: identity.User{ID: "person", Subject: "issuer:person"}}, nil
	})
	source.grants = grantFunc(func(_ context.Context, user, selected string) (string, error) {
		grantReads++
		require.Equal(t, "person", user)
		require.Equal(t, "account", selected)
		return "account", nil
	})
	cache, err := NewCache(source, source.authorities, cacheTestLimits(), source.clock)
	require.NoError(t, err)
	t.Cleanup(cache.Close)
	caller := Identity{Subject: SessionSubjectPrefix + "issuer:person", Tenant: "account"}
	first, err := cache.Resolve(t.Context(), caller)
	require.NoError(t, err)
	require.Equal(t, uint64(7), first.Key().Revision)
	require.False(t, first.Key().APIKey.HasScope("admin"))
	before := len(*reads)
	for range 10 {
		next, err := cache.Resolve(t.Context(), caller)
		require.NoError(t, err)
		require.Same(t, first, next)
	}
	require.Equal(t, before, len(*reads))
	require.Equal(t, 1, userReads)
	require.Equal(t, 1, grantReads)
}

func TestSessionProjectionRefusesUnknownGrantState(t *testing.T) {
	source, _ := sourceFixture(t)
	source.users = userFunc(func(context.Context, string) (identity.UserRecord, error) {
		return identity.UserRecord{Revision: 1, User: identity.User{ID: "person", Subject: "issuer:person"}}, nil
	})
	source.grants = grantFunc(func(context.Context, string, string) (string, error) { return "", ErrUnavailable })
	_, err := source.Load(t.Context(), Identity{Subject: SessionSubjectPrefix + "issuer:person", Tenant: "account"})
	require.ErrorIs(t, err, ErrUnavailable)
}

func TestSessionDeadlineIncludesClockUncertainty(t *testing.T) {
	now := time.Unix(1000, 0)
	healthy := true
	cache := &Cache{clock: func() (time.Time, bool) { return now, healthy }, limits: cacheTestLimits()}
	require.NoError(t, cache.CheckDeadline(now.Add(31*time.Second)))
	require.ErrorIs(t, cache.CheckDeadline(now.Add(30*time.Second)), ErrExpired)
	require.ErrorIs(t, cache.CheckDeadline(time.Time{}), ErrExpired)
	healthy = false
	require.ErrorIs(t, cache.CheckDeadline(now.Add(time.Hour)), ErrUnavailable)
}
