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

// StoredCredentialResolver resolves one exact stored record against the
// provider contract retained by the request's runtime generation. One
// resolver serves both stored planes: the scope decides whether the record is
// the operator's gateway credential or the account's own.
type StoredCredentialResolver interface {
	ResolveStoredMaterial(context.Context, string, catalogs.Provider) (credentials.Material, error)
}

type credentialPolicy struct {
	runtime    connectors.RuntimeLease
	storedKeys StoredCredentialResolver
	gate       OperatorCredentialGate
	accountID  string
	sources    []keyring.CredentialSource
	states     map[string]credentialRouteState
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
	accountID string,
	runtime connectors.RuntimeLease,
	storedKeys StoredCredentialResolver,
	gate OperatorCredentialGate,
) (*credentialPolicy, error) {
	parsedStrategy, err := keyring.ParseStrategy(string(strategy))
	if err != nil {
		return nil, failure.New(failure.Validation, "The provider credential strategy is invalid.", false, failure.ProviderDetails{}, err)
	}
	return &credentialPolicy{
		runtime: runtime, storedKeys: storedKeys, gate: gate, accountID: accountID,
		sources: reachableSources(parsedStrategy, accountID, storedKeys),
		states:  make(map[string]credentialRouteState),
	}, nil
}

// reachableSources drops the planes this deployment cannot read at all, so the
// policy never spends an attempt on a source that has no store behind it.
// A strategy is never widened here: an account on BYOKOnly whose BYOK plane is
// unreachable resolves to no source and gets the not-configured failure, and
// never falls through to an operator credential.
func reachableSources(
	strategy keyring.Strategy,
	accountID string,
	storedKeys StoredCredentialResolver,
) []keyring.CredentialSource {
	reachable := make([]keyring.CredentialSource, 0, 3)
	for _, source := range strategy.Sources() {
		switch source {
		case keyring.SourceGateway:
			if storedKeys == nil {
				continue
			}
		case keyring.SourceBYOK:
			if storedKeys == nil || accountID == "" {
				continue
			}
		}
		reachable = append(reachable, source)
	}
	return reachable
}

func credentialRequestPolicy(request *Request) (keyring.Strategy, string) {
	if request == nil {
		return keyring.OperatorFirst, ""
	}
	return request.APIKeyConfig.credentialStrategy(), request.AccountID
}

// credentialStrategy reports the effective strategy the HTTP seam already
// resolved against the account's. A request that arrived without a key config
// runs under the default.
func (c *APIKeyConfig) credentialStrategy() keyring.Strategy {
	if c == nil || c.CredentialStrategy == "" {
		return keyring.OperatorFirst
	}
	return c.CredentialStrategy
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
	case keyring.SourceEnvironment:
		material, err = p.runtime.ResolveMaterial(ctx, route.ProviderID)
	case keyring.SourceGateway:
		material, err = p.resolveStored(ctx, keyring.GatewayScope, route.ProviderID)
	case keyring.SourceBYOK:
		material, err = p.resolveStored(ctx, keyring.AccountScope(p.accountID), route.ProviderID)
	default:
		err = errors.New("unsupported credential source")
	}
	if err == nil {
		if source == keyring.SourceEnvironment && p.gate != nil &&
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
				// The attempt is credited to nobody, which is a different
				// fact from an attempt whose credential went unrecorded. The
				// owner stays empty so availability still counts the result:
				// a keyless provider's health is the deployment's to see.
				execution.RecordCredential(ctx, execution.CredentialEvidence{
					Source: string(keyring.SourceAnonymous),
				})
				return credentialSelection{material: material, source: keyring.SourceAnonymous}, nil, execution.AttemptActionDefault
			}
		}
		return credentialSelection{}, providerFailure, execution.AttemptActionFallbackRoute
	}
	return credentialSelection{}, providerFailure, execution.AttemptActionStop
}

// resolveStored reads one stored plane. The gateway plane and the BYOK plane
// differ only in their scope, so they share this path and cannot drift apart
// in how they look a provider up or how they fail.
func (p *credentialPolicy) resolveStored(
	ctx context.Context,
	scope string,
	providerID string,
) (credentials.Material, error) {
	if p.storedKeys == nil {
		return credentials.Material{}, keyring.ErrKeyNotFound
	}
	snapshot := p.runtime.Snapshot()
	if snapshot == nil || snapshot.Catalog() == nil {
		return credentials.Material{}, errors.New("runtime catalog is unavailable")
	}
	provider, err := snapshot.Catalog().Provider(catalogs.ProviderID(providerID))
	if err != nil {
		return credentials.Material{}, err
	}
	return p.storedKeys.ResolveStoredMaterial(ctx, scope, provider)
}

// credentialEvidence reports who paid for the attempt, and out of which of
// their planes. An environment credential and a gateway credential are both
// the operator's, so they record the same owner; the source is what tells
// them apart, and an operator needs it to see whether the deployment is
// running on a shell variable or on a credential someone applied.
func credentialEvidence(
	source keyring.CredentialSource,
	material credentials.Material,
) execution.CredentialEvidence {
	owner := execution.CredentialOwner("")
	switch source {
	case keyring.SourceEnvironment, keyring.SourceGateway:
		owner = execution.CredentialOwnerOperator
	case keyring.SourceBYOK:
		owner = execution.CredentialOwnerAccount
	}
	return execution.CredentialEvidence{
		Owner: owner, Source: string(source), MaterialVersion: material.Version(),
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
