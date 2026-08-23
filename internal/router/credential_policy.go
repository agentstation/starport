package router

import (
	"context"
	"errors"

	"github.com/agentstation/starmap/pkg/catalogs"

	"github.com/agentstation/starport/internal/credentials"
	"github.com/agentstation/starport/internal/execution"
	"github.com/agentstation/starport/internal/failure"
	"github.com/agentstation/starport/internal/providers/connectors"
	"github.com/agentstation/starport/internal/providers/keyring"
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
	gate     OperatorCredentialGate
	tenantID string
	sources  []keyring.CredentialSource
	states   map[string]credentialRouteState
}

type credentialRouteState struct {
	index    int
	previous *failure.Failure
}

type credentialSelection struct {
	material credentials.Material
	source   keyring.CredentialSource
}

func newCredentialPolicy(
	strategy keyring.Strategy,
	tenantID string,
	runtime connectors.RuntimeLease,
	userKeys UserCredentialResolver,
	gate OperatorCredentialGate,
) (*credentialPolicy, error) {
	parsedStrategy, err := keyring.ParseStrategy(string(strategy))
	if err != nil {
		return nil, failure.New(failure.Validation, "The provider credential strategy is invalid.", false, failure.ProviderDetails{}, err)
	}
	sources := parsedStrategy.Sources()
	if userKeys == nil && parsedStrategy == keyring.OperatorFirst {
		sources = []keyring.CredentialSource{keyring.CredentialSourceOperator}
	}
	return &credentialPolicy{
		runtime: runtime, userKeys: userKeys, gate: gate, tenantID: tenantID,
		sources: sources, states: make(map[string]credentialRouteState),
	}, nil
}

func credentialRequestPolicy(request *Request) (keyring.Strategy, string) {
	if request == nil {
		return keyring.OperatorFirst, ""
	}
	strategy := keyring.OperatorFirst
	if request.APIKeyConfig != nil {
		strategy = request.APIKeyConfig.CredentialStrategy
	}
	return strategy, request.TenantID
}

func (p *credentialPolicy) resolve(
	ctx context.Context,
	route routing.Route,
) (credentialSelection, *failure.Failure, execution.AttemptAction) {
	state := p.states[route.ID()]
	index := state.index
	if index >= len(p.sources) {
		return credentialSelection{}, credentialUnavailable(route.ProviderID, nil), execution.AttemptActionFallbackRoute
	}
	source := p.sources[index]
	var material credentials.Material
	var err error
	switch source {
	case keyring.CredentialSourceOperator:
		material, err = p.runtime.ResolveMaterial(ctx, route.ProviderID)
	case keyring.CredentialSourceUser:
		if p.userKeys == nil || p.tenantID == "" {
			err = keyring.ErrKeyNotFound
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
		material, err = p.userKeys.ResolveUserMaterial(ctx, keyring.UserScope(p.tenantID), provider)
	default:
		err = errors.New("unsupported credential source")
	}
	if err == nil {
		if source == keyring.CredentialSourceOperator && p.gate != nil &&
			!p.gate.OperatorMaterialReady(route.ProviderID, material.Version()) {
			providerFailure := failure.New(
				failure.Authentication,
				"Provider credentials are unavailable.",
				false,
				failure.ProviderDetails{Provider: route.ProviderID},
				nil,
			)
			if p.advance(route, providerFailure) {
				return credentialSelection{}, providerFailure, execution.AttemptActionContinueRoute
			}
			return credentialSelection{}, providerFailure, execution.AttemptActionFallbackRoute
		}
		execution.RecordCredential(ctx, credentialEvidence(source, material))
		return credentialSelection{material: material, source: source}, nil, execution.AttemptActionDefault
	}
	execution.RecordCredential(ctx, credentialEvidence(source, material))
	providerFailure, notConfigured := credentialResolutionFailure(route.ProviderID, err)
	if notConfigured && p.advance(route, nil) {
		return credentialSelection{}, providerFailure, execution.AttemptActionContinueRoute
	}
	if notConfigured && state.previous != nil {
		return credentialSelection{}, state.previous, execution.AttemptActionFallbackRoute
	}
	if notConfigured {
		if source, ok := p.runtime.(connectors.AnonymousMaterialSource); ok {
			if material, exists := source.AnonymousMaterial(route.ProviderID); exists {
				return credentialSelection{material: material}, nil, execution.AttemptActionDefault
			}
		}
		return credentialSelection{}, providerFailure, execution.AttemptActionFallbackRoute
	}
	return credentialSelection{}, providerFailure, execution.AttemptActionStop
}

func credentialEvidence(
	source keyring.CredentialSource,
	material credentials.Material,
) execution.CredentialEvidence {
	owner := execution.CredentialOwner("")
	switch source {
	case keyring.CredentialSourceOperator:
		owner = execution.CredentialOwnerOperator
	case keyring.CredentialSourceUser:
		owner = execution.CredentialOwnerTenant
	}
	return execution.CredentialEvidence{
		Owner: owner, MaterialVersion: material.Version(),
	}
}

func (p *credentialPolicy) afterFailure(route routing.Route, providerFailure *failure.Failure) execution.AttemptAction {
	if providerFailure == nil || providerFailure.StateScope() != failure.ScopeCredential ||
		!keyring.CanAdvance(providerFailure) {
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
	case errors.Is(err, keyring.ErrKeyNotFound), errors.Is(err, credentials.ErrProviderNotConfigured),
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
	return keyring.UnavailableFailure(providerID, err)
}
