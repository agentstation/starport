package router

import (
	"context"
	"errors"

	"github.com/agentstation/starmap/pkg/catalogs"

	"github.com/agentstation/starport/internal/credentials"
	"github.com/agentstation/starport/internal/execution"
	"github.com/agentstation/starport/internal/failure"
	"github.com/agentstation/starport/internal/providers/byok"
	"github.com/agentstation/starport/internal/providers/connectors"
	"github.com/agentstation/starport/internal/routing"
)

// UserCredentialResolver resolves one exact tenant record against the
// provider contract retained by the request's runtime generation.
type UserCredentialResolver interface {
	ResolveUserMaterial(context.Context, string, catalogs.Provider) (credentials.Material, error)
}

type credentialPolicy struct {
	runtime  connectors.RuntimeLease
	userKeys UserCredentialResolver
	tenantID string
	sources  []byok.CredentialSource
	states   map[string]credentialRouteState
}

type credentialRouteState struct {
	index    int
	previous *failure.Failure
}

func newCredentialPolicy(
	strategy byok.Strategy,
	tenantID string,
	runtime connectors.RuntimeLease,
	userKeys UserCredentialResolver,
) (*credentialPolicy, error) {
	parsedStrategy, err := byok.ParseStrategy(string(strategy))
	if err != nil {
		return nil, failure.New(failure.Validation, "The provider credential strategy is invalid.", false, failure.ProviderDetails{}, err)
	}
	sources := parsedStrategy.Sources()
	if userKeys == nil && parsedStrategy == byok.OperatorFirst {
		sources = []byok.CredentialSource{byok.CredentialSourceOperator}
	}
	return &credentialPolicy{
		runtime: runtime, userKeys: userKeys, tenantID: tenantID,
		sources: sources, states: make(map[string]credentialRouteState),
	}, nil
}

func credentialRequestPolicy(request *Request) (byok.Strategy, string) {
	if request == nil {
		return byok.OperatorFirst, ""
	}
	strategy := byok.OperatorFirst
	if request.APIKeyConfig != nil {
		strategy = request.APIKeyConfig.CredentialStrategy
	}
	return strategy, request.TenantID
}

func (p *credentialPolicy) resolve(
	ctx context.Context,
	route routing.Route,
) (credentials.Material, *failure.Failure, execution.AttemptAction) {
	state := p.states[route.ID()]
	index := state.index
	if index >= len(p.sources) {
		return credentials.Material{}, credentialUnavailable(route.ProviderID, nil), execution.AttemptActionFallbackRoute
	}
	source := p.sources[index]
	var material credentials.Material
	var err error
	switch source {
	case byok.CredentialSourceOperator:
		material, err = p.runtime.ResolveMaterial(ctx, route.ProviderID)
	case byok.CredentialSourceUser:
		if p.userKeys == nil || p.tenantID == "" {
			err = byok.ErrKeyNotFound
			break
		}
		snapshot := p.runtime.Snapshot()
		if snapshot == nil || snapshot.Catalog() == nil {
			err = errors.New("runtime catalog is unavailable")
			break
		}
		provider, lookupErr := snapshot.Catalog().Provider(catalogs.ProviderID(route.ProviderID))
		if lookupErr != nil {
			err = lookupErr
			break
		}
		material, err = p.userKeys.ResolveUserMaterial(ctx, byok.UserScope(p.tenantID), provider)
	default:
		err = errors.New("unsupported credential source")
	}
	if err == nil {
		return material, nil, execution.AttemptActionDefault
	}
	providerFailure, notConfigured := credentialResolutionFailure(route.ProviderID, err)
	if notConfigured && p.advance(route, nil) {
		return credentials.Material{}, providerFailure, execution.AttemptActionContinueRoute
	}
	if notConfigured && state.previous != nil {
		return credentials.Material{}, state.previous, execution.AttemptActionFallbackRoute
	}
	if notConfigured {
		return credentials.Material{}, providerFailure, execution.AttemptActionFallbackRoute
	}
	return credentials.Material{}, providerFailure, execution.AttemptActionStop
}

func (p *credentialPolicy) afterFailure(route routing.Route, providerFailure *failure.Failure) execution.AttemptAction {
	if !byok.CanAdvance(providerFailure) {
		return execution.AttemptActionDefault
	}
	if p.advance(route, providerFailure) {
		return execution.AttemptActionContinueRoute
	}
	return execution.AttemptActionFallbackRoute
}

func (p *credentialPolicy) advance(route routing.Route, previous *failure.Failure) bool {
	state := p.states[route.ID()]
	if state.index+1 >= len(p.sources) {
		return false
	}
	state.index++
	if previous != nil {
		state.previous = previous
	}
	p.states[route.ID()] = state
	return true
}

func credentialResolutionFailure(providerID string, err error) (*failure.Failure, bool) {
	details := failure.ProviderDetails{Provider: providerID}
	switch {
	case errors.Is(err, context.Canceled):
		return failure.New(failure.Canceled, "The request was canceled.", false, details, err), false
	case errors.Is(err, context.DeadlineExceeded):
		return failure.New(failure.Timeout, "Provider credential resolution timed out.", false, details, err), false
	case errors.Is(err, byok.ErrKeyNotFound), errors.Is(err, credentials.ErrProviderNotConfigured),
		credentials.IsSourceError(err, credentials.SourceErrorNotConfigured):
		return credentialUnavailable(providerID, err), true
	case credentials.IsSourceError(err, credentials.SourceErrorDenied):
		return failure.New(failure.Permission, "Provider credential access was denied.", false, details, err), false
	case credentials.IsSourceError(err, credentials.SourceErrorInvalid):
		return failure.New(failure.Validation, "Provider credential material is invalid.", false, details, err), false
	default:
		return failure.New(failure.Internal, "Provider credential resolution failed.", false, details, err), false
	}
}

func credentialUnavailable(providerID string, err error) *failure.Failure {
	return byok.UnavailableFailure(providerID, err)
}
