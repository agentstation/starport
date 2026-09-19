package server

import (
	"context"
	"errors"
	"net/http"

	"github.com/agentstation/starport/internal/account"
	"github.com/agentstation/starport/internal/apikey"
	"github.com/agentstation/starport/internal/authorization"
	"github.com/agentstation/starport/internal/server/requestctx"
)

// UseAuthorization selects the bounded cache and its verified clock before serving requests.
func (m *AuthMiddleware) UseAuthorization(cache *authorization.Cache, clock authorization.Clock) {
	m.authorization, m.permissionClock = cache, clock
}

func (m *AuthMiddleware) cachedBearer(ctx context.Context, secret, hash string) (context.Context, error) {
	bundle, err := m.authorization.Resolve(ctx, authorization.Identity{Subject: hash})
	if err != nil {
		return nil, err
	}
	if m.permissionClock == nil {
		return nil, authorization.ErrUnavailable
	}
	now, healthy := m.permissionClock()
	if err := bundle.Permit().Check(now, healthy); err != nil {
		return nil, err
	}
	key, owner := bundle.Key().APIKey, bundle.Account().Account
	ctx = requestctx.WithAPIKey(ctx, secret)
	ctx = requestctx.WithAPIKeyID(ctx, key.ID)
	ctx = requestctx.WithAPIKeyModel(ctx, &key)
	ctx = requestctx.WithAccountID(ctx, owner.ID)
	ctx = requestctx.WithAccountRecord(ctx, &owner)
	policy := func() error {
		if m.policy.Disabled() {
			return authorization.ErrWithdrawn
		}
		return nil
	}
	if err := policy(); err != nil {
		return nil, err
	}
	ctx = requestctx.WithAuthorization(ctx, bundle, m.permissionClock, policy)
	return requestctx.WithAuthorizationRefresh(ctx, func(next context.Context) (context.Context, error) {
		return m.cachedBearer(next, "", hash)
	}), nil
}

func writeAuthorizationRefusal(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, apikey.ErrNotFound):
		writeProtocolError(w, r, http.StatusUnauthorized, "authentication_error", "Invalid API key")
	case errors.Is(err, authorization.ErrDenied), errors.Is(err, account.ErrNotFound):
		writeProtocolError(w, r, http.StatusForbidden, "permission_error", "Account or key access denied")
	default:
		w.Header().Set("Retry-After", "1")
		writeProtocolError(w, r, http.StatusServiceUnavailable, "server_error", "Authorization is temporarily unavailable")
	}
}

func (m *AuthMiddleware) cachedLocal(ctx context.Context, subject string, policy func() error) (context.Context, error) {
	bundle, err := m.authorization.Resolve(ctx, authorization.Identity{Subject: subject})
	if err != nil {
		return nil, err
	}
	if m.permissionClock == nil {
		return nil, authorization.ErrUnavailable
	}
	now, healthy := m.permissionClock()
	if err := bundle.Permit().Check(now, healthy); err != nil {
		return nil, err
	}
	key, owner := bundle.Key().APIKey, bundle.Account().Account
	ctx = requestctx.WithAPIKeyID(ctx, key.ID)
	ctx = requestctx.WithAPIKeyModel(ctx, &key)
	ctx = requestctx.WithAccountID(ctx, owner.ID)
	ctx = requestctx.WithAccountRecord(ctx, &owner)
	if policy != nil {
		if err := policy(); err != nil {
			return nil, err
		}
	}
	ctx = requestctx.WithAuthorization(ctx, bundle, m.permissionClock, policy)
	grant, actor, console := requestctx.GetConsoleSession(ctx)
	return requestctx.WithAuthorizationRefresh(ctx, func(next context.Context) (context.Context, error) {
		if console {
			next = requestctx.WithConsoleSession(next, grant, actor)
		}
		return m.cachedLocal(next, subject, policy)
	}), nil
}
