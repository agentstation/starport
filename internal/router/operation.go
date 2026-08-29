package router

import (
	"context"
	"fmt"
	"strings"

	"github.com/agentstation/starmap/pkg/catalogs"

	runtimecatalog "github.com/agentstation/starport/internal/catalog"
	"github.com/agentstation/starport/internal/credentials"
	"github.com/agentstation/starport/internal/execution"
	"github.com/agentstation/starport/internal/failure"
	"github.com/agentstation/starport/internal/providers/connectors"
	"github.com/agentstation/starport/internal/routing"
)

// Every operation past chat reaches its provider the same way: plan against
// one retained runtime generation, resolve a credential, bind the endpoint the
// planner chose, and spend one total-attempt budget. Only the provider call
// itself differs. This file holds the shared path so that adding an operation
// adds a call rather than a second retry policy.

// OperationRequest carries one canonical request plus the account routing and
// credential policy every operation reads.
type OperationRequest[Request any] struct {
	Request      Request
	APIKeyConfig *APIKeyConfig
	AccountID    string
}

// policy names the model the plan asks for and the key restrictions that apply
// to it. Each canonical request carries its own Model field and no interface
// unites them, so the caller states the model and the shared path stays free
// of a constraint that would exist only to read one string.
func (r *OperationRequest[Request]) policy(model string) operationPolicy {
	return operationPolicy{Model: model, APIKeyConfig: r.APIKeyConfig, AccountID: r.AccountID}
}

// operationPolicy is everything the shared path reads that is not the
// provider call itself.
type operationPolicy struct {
	Model        string
	APIKeyConfig *APIKeyConfig
	AccountID    string
	// Provider pins the plan to one provider. It is empty for every operation
	// that finishes inside its request, and set only when a request carries an
	// identifier a single provider issued.
	Provider string
}

// allows reports whether the key may reach one named provider. An empty
// restriction reaches every provider, which is what an unrestricted key means
// everywhere else.
func (p operationPolicy) allows(provider string) bool {
	if p.APIKeyConfig == nil {
		return true
	}
	allowed := normalizeProviders(p.APIKeyConfig.AllowedProviders)
	if len(allowed) == 0 {
		return true
	}
	for _, candidate := range allowed {
		if strings.EqualFold(candidate, provider) {
			return true
		}
	}
	return false
}

// OperationResponse is one operation result with the same route evidence a
// chat or an embedding answer carries.
type OperationResponse[Response any] struct {
	Response         Response
	ModelUsed        string
	ProviderUsed     string
	CredentialSource string
	Attempts         int
	Metadata         *Metadata
	CatalogSnapshot  *runtimecatalog.RoutableSnapshot
}

// operationAttempt is the provider call of one operation. Everything around
// it, the plan, the credential, the endpoint binding, and the budget, is the
// same for every one of them, so only this part is written per operation.
type operationAttempt[Response any] func(
	ctx context.Context,
	connector connectors.Connector,
	route routing.Route,
	selected credentialSelection,
) (*Response, *failure.Failure, execution.AttemptAction)

// routeOperation runs one operation through the shared route plan and budget.
// It is written once over a type parameter rather than once per operation,
// because a second copy of a retry and credential policy drifts into a second
// policy.
func routeOperation[Response any](
	ctx context.Context,
	r *modelRouter,
	policy operationPolicy,
	operation routing.Operation,
	clone func(Response) Response,
	attempt operationAttempt[Response],
) (*OperationResponse[Response], error) {
	runtime, owned, err := r.acquireRuntime(ctx)
	if err != nil {
		return nil, ErrNoModelsAvailable
	}
	if owned {
		defer runtime.Release()
	}

	if err := checkCatalogued(runtime, policy.Model); err != nil {
		return nil, err
	}

	planningRequest := operationPlanningRequest(policy, r.config.EnableCostOptimization)
	plan, err := r.planOperation(ctx, planningRequest, operation, runtime, nil)
	if err != nil {
		if mapped := routePlanFailure(err); mapped != nil {
			return nil, mapped
		}
		return nil, fmt.Errorf("plan %s route: %w", operation, err)
	}
	credentialPolicy, err := newCredentialPolicy(
		policy.APIKeyConfig.credentialStrategy(), policy.AccountID,
		runtime, r.storedKeys, r.credentialGate,
	)
	if err != nil {
		return nil, err
	}

	result, err := execution.Execute(ctx, r.executor, plan, func(
		attemptCtx context.Context,
		planned routing.Attempt,
	) (*Response, *failure.Failure, execution.AttemptAction) {
		connector := runtime.Get(planned.Route.ProviderID)
		if connector == nil {
			return nil, failure.New(
				failure.ProviderUnavailable,
				"No provider adapter is available.",
				true,
				failure.ProviderDetails{Provider: planned.Route.ProviderID},
				nil,
			), execution.AttemptActionDefault
		}
		selected, materialFailure, action := credentialPolicy.resolve(attemptCtx, planned.Route)
		if materialFailure != nil {
			return nil, materialFailure, action
		}
		boundRoute, bindFailure := bindSelectedEndpoint(runtime, planned.Route, selected)
		if bindFailure != nil {
			return nil, bindFailure, execution.AttemptActionStop
		}
		response, attemptFailure, action := attempt(attemptCtx, connector, boundRoute, selected)
		if attemptFailure != nil {
			if action == execution.AttemptActionDefault {
				action = credentialPolicy.afterFailure(planned.Route, attemptFailure)
			}
			return nil, attemptFailure, action
		}
		execution.RecordCredentialAccepted(attemptCtx)
		return response, nil, action
	}, clone)
	if err != nil {
		evidence := executionEvidence(err)
		return &OperationResponse[Response]{Metadata: responseMetadata(evidence, "all models failed")},
			fmt.Errorf("%w: %w", ErrAllModelsFailed, err)
	}

	return &OperationResponse[Response]{
		Response:         result.Response,
		ModelUsed:        result.Route.ID(),
		ProviderUsed:     result.Route.ProviderID,
		CredentialSource: credentialSourceUsed(result.Attempts),
		Attempts:         len(result.Attempts),
		Metadata:         responseMetadata(result.Attempts, selectionReason(result.Attempts)),
		CatalogSnapshot:  runtime.Snapshot(),
	}, nil
}

// checkCatalogued reports a model name the retained generation does not hold.
// The planner cannot separate that from a catalogued model whose providers are
// all unavailable, and the two need different answers: one is a name to
// correct, the other is a wait. Asking the snapshot first is also what keeps a
// misspelled model from reaching a provider at all.
func checkCatalogued(runtime connectors.RuntimeLease, model string) error {
	// An unnamed model asks for any offering that serves the operation, and the
	// automatic model asks the planner to choose. Neither names a catalog entry
	// to look up.
	if model == "" || model == AutoModelID {
		return nil
	}
	if runtime == nil || runtime.Snapshot() == nil {
		return nil
	}
	if !runtime.Snapshot().Names(model) {
		return fmt.Errorf("%w: %s", runtimecatalog.ErrModelNotCatalogued, model)
	}
	return nil
}

// operationPlanningRequest builds the same planning request the embedding path
// builds. Account model and provider restrictions apply to every operation
// exactly as they apply to a chat call, because they are properties of the key.
func operationPlanningRequest(policy operationPolicy, preferLowestCost bool) routing.Request {
	request := routing.Request{
		Optimization: routing.OptimizationPolicy{
			PreferLowestCost:    preferLowestCost,
			PreferLowestLatency: true,
		},
	}
	// An unnamed model asks the planner for any offering that serves the
	// operation. Only the gateway's own reads arrive that way: a caller always
	// names a model, and every route a caller reaches refuses an empty one
	// before planning. Naming no model is how a gateway-ordered read stays a
	// catalog question rather than a table in this package.
	if policy.Model != "" {
		request.Models = []string{policy.Model}
	}
	if policy.APIKeyConfig != nil {
		request.Account = routing.AccountPolicy{
			AllowedModels:    wildcardAsUnrestricted(policy.APIKeyConfig.AllowedModels),
			AllowedProviders: normalizeProviders(policy.APIKeyConfig.AllowedProviders),
			ModelOverrides:   cloneModelOverrides(policy.APIKeyConfig.ModelOverrides),
		}
	}
	// A pinned provider narrows what the key already allows and never widens
	// it. The caller checked membership before setting it, so writing the one
	// name here cannot let a key reach a provider it may not use.
	if policy.Provider != "" {
		request.Account.AllowedProviders = []string{policy.Provider}
	}
	return request
}

// requestBinder is a provider request that can take the route the planner
// chose and the credential the policy selected. Every one of them embeds
// connectors.MediaTarget, so the binding is one call rather than three field
// writes repeated per operation.
type requestBinder interface {
	Bind(model string, endpoint connectors.InferenceEndpoint, credential credentials.Material)
}

// providerCall names the three steps that differ between operations: finding
// the narrow transport interface, building the provider request, and
// converting the answer back. Everything around them is one shape, written
// once in attempt below, so every operation reports a missing transport, a
// provider error, and an unreadable answer the same way.
type providerCall[Request requestBinder, ProviderResponse, Response any] struct {
	transport func(connectors.Connector, catalogs.EndpointType) (providerInvoke[Request, ProviderResponse], bool)
	build     func() Request
	convert   func(ProviderResponse) (Response, error)

	// bounded refuses one route whose offering states a limit the request
	// exceeds. Most operations leave it nil: a limit that every offering
	// shares belongs in validation, and only a limit that differs between two
	// offerings of one model has to wait until the planner has chosen.
	bounded func(routing.Route) error
}

// providerInvoke is one provider call, taken from the transport interface
// that serves the operation.
type providerInvoke[Request, ProviderResponse any] func(context.Context, Request) (ProviderResponse, error)

// attempt turns one provider call into the attempt the shared route path runs.
func (call providerCall[Request, ProviderResponse, Response]) attempt(
	operation routing.Operation,
) operationAttempt[Response] {
	return func(
		attemptCtx context.Context,
		connector connectors.Connector,
		route routing.Route,
		selected credentialSelection,
	) (*Response, *failure.Failure, execution.AttemptAction) {
		invoke, implemented := call.transport(connector, endpointTypeOf(route))
		if !implemented {
			return nil, transportInterfaceMissing(route, string(operation)), execution.AttemptActionDefault
		}
		if call.bounded != nil {
			if err := call.bounded(route); err != nil {
				return nil, offeringBoundExceeded(route, err), execution.AttemptActionDefault
			}
		}
		request := call.build()
		request.Bind(
			route.ProviderModelID,
			connectors.InferenceEndpoint{Type: endpointTypeOf(route), URL: route.Endpoint.URL},
			selected.material,
		)
		response, requestErr := invoke(attemptCtx, request)
		if requestErr != nil {
			return nil, connectors.NormalizeFailure(route.ProviderID, requestErr), execution.AttemptActionDefault
		}
		canonical, convertErr := call.convert(response)
		return operationAnswer(route, canonical, convertErr)
	}
}

// operationAnswer names the routed model on a converted response, and reports an
// unconvertible provider answer as an internal failure rather than as a result.
func operationAnswer[Response any](
	route routing.Route,
	canonical Response,
	err error,
) (*Response, *failure.Failure, execution.AttemptAction) {
	if err != nil {
		return nil, failure.New(
			failure.Internal,
			"The provider response was invalid.",
			false,
			failure.ProviderDetails{Provider: route.ProviderID},
			err,
		), execution.AttemptActionDefault
	}
	return &canonical, nil, execution.AttemptActionDefault
}

// offeringBoundExceeded reports a request the chosen offering will not accept.
// It is retryable, because the bound belongs to the offering rather than to the
// model: a second offering of the same model may state a larger one, and the
// gateway has no reason to refuse before it has asked. The kind is a validation
// fault, so a request that exhausts every offering answers the caller rather
// than reporting an unavailable gateway.
func offeringBoundExceeded(route routing.Route, err error) *failure.Failure {
	return failure.New(
		failure.Validation,
		err.Error(),
		true,
		failure.ProviderDetails{Provider: route.ProviderID},
		err,
	)
}

func endpointTypeOf(route routing.Route) catalogs.EndpointType {
	return catalogs.EndpointType(route.Endpoint.Protocol)
}

// transportInterfaceMissing reports a route whose transport does not implement
// the narrow interface the operation needs. Activation refuses a descriptor in that
// state, so reaching this means the catalog named an operation the compiled
// transport does not serve. The next route may serve it, so the attempt is
// retryable rather than fatal.
func transportInterfaceMissing(route routing.Route, operation string) *failure.Failure {
	return failure.New(
		failure.ProviderUnavailable,
		fmt.Sprintf("The provider transport does not serve %s.", operation),
		true,
		failure.ProviderDetails{Provider: route.ProviderID},
		nil,
	)
}
