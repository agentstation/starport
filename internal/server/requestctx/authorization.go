package requestctx

import (
	"context"

	"github.com/agentstation/starport/internal/authorization"
	"github.com/agentstation/starport/internal/inference"
)

type authorizationKey struct{}

type authorizationState struct {
	bundle *authorization.Bundle
	clock  authorization.Clock
	policy func() error
}

// WithAuthorization retains the original permission receipt for this request.
func WithAuthorization(ctx context.Context, bundle *authorization.Bundle, clock authorization.Clock, policy func() error) context.Context {
	state := &authorizationState{bundle: bundle, clock: clock, policy: policy}
	ctx = context.WithValue(ctx, authorizationKey{}, state)
	return inference.WithPermission(ctx, state)
}

// Authorization returns the original bundle only while every permission remains valid.
func Authorization(ctx context.Context) (*authorization.Bundle, error) {
	state, ok := ctx.Value(authorizationKey{}).(*authorizationState)
	if !ok || state.bundle == nil || state.clock == nil {
		return nil, authorization.ErrUnavailable
	}
	if err := state.Check(); err != nil {
		return nil, err
	}
	return state.bundle, nil
}

func (state *authorizationState) Check() error {
	if state == nil || state.bundle == nil || state.clock == nil {
		return authorization.ErrUnavailable
	}
	if state.policy != nil {
		if err := state.policy(); err != nil {
			return err
		}
	}
	now, healthy := state.clock()
	return state.bundle.Permit().Check(now, healthy)
}
