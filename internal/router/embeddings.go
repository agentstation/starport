package router

import (
	"context"
	"errors"
	"fmt"

	"github.com/agentstation/starmap/pkg/catalogs"

	"github.com/agentstation/starport/internal/execution"
	"github.com/agentstation/starport/internal/failure"
	"github.com/agentstation/starport/internal/inference"
	"github.com/agentstation/starport/internal/providers/connectors"
	"github.com/agentstation/starport/internal/routing"
)

// RouteEmbeddings executes one embedding request through the same immutable
// runtime generation, credential policy, and total-attempt budget as chat.
func (r *modelRouter) RouteEmbeddings(ctx context.Context, req *EmbeddingRequest) (*EmbeddingResponse, error) {
	if req == nil || req.EmbeddingsRequest == nil || req.Model == "" {
		return nil, ErrNoModelsAvailable
	}
	runtime, owned, err := r.acquireRuntime(ctx)
	if err != nil {
		return nil, ErrNoModelsAvailable
	}
	if owned {
		defer runtime.Release()
	}

	planningRequest := embeddingPlanningRequest(req, r.config.EnableCostOptimization)
	plan, err := r.planOperation(ctx, planningRequest, routing.OperationEmbeddings, runtime, nil)
	if err != nil {
		if errors.Is(err, routing.ErrNoCandidate) {
			return nil, ErrNoModelsAvailable
		}
		return nil, fmt.Errorf("plan embedding route: %w", err)
	}
	credentialPolicy, err := newCredentialPolicy(
		req.APIKeyConfig.credentialStrategy(), req.AccountID,
		req.APIKeyConfig.byokProviderGate(),
		runtime, r.storedKeys, r.credentialGate,
	)
	if err != nil {
		return nil, err
	}

	result, err := r.executor.ExecuteEmbedding(ctx, plan, func(
		attemptCtx context.Context,
		planned routing.Attempt,
	) (*inference.EmbeddingResponse, *failure.Failure, execution.AttemptAction) {
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
		request := *req.EmbeddingsRequest
		request.Model = boundRoute.ProviderModelID
		request.Endpoint = connectors.InferenceEndpoint{
			Type: catalogs.EndpointType(boundRoute.Endpoint.Protocol),
			URL:  boundRoute.Endpoint.URL,
		}
		request.Credential = selected.material
		response, requestErr := connector.Embeddings(attemptCtx, &request)
		if requestErr != nil {
			providerFailure := connectors.NormalizeFailure(planned.Route.ProviderID, requestErr)
			return nil, providerFailure, credentialPolicy.afterFailure(planned.Route, providerFailure)
		}
		execution.RecordCredentialAccepted(attemptCtx)
		canonical, conversionErr := connectors.EmbeddingResponseToInference(response)
		if conversionErr != nil {
			return nil, failure.New(
				failure.Internal,
				"The provider response was invalid.",
				false,
				failure.ProviderDetails{Provider: planned.Route.ProviderID},
				conversionErr,
			), execution.AttemptActionDefault
		}
		canonical.Model = planned.Route.ID()
		return &canonical, nil, execution.AttemptActionDefault
	})
	if err != nil {
		evidence := executionEvidence(err)
		return &EmbeddingResponse{Metadata: responseMetadata(evidence, "all models failed")}, fmt.Errorf("%w: %w", ErrAllModelsFailed, err)
	}

	return &EmbeddingResponse{
		Response:         result.Response,
		ModelUsed:        result.Route.ID(),
		ProviderUsed:     result.Route.ProviderID,
		CredentialSource: credentialSourceUsed(result.Attempts),
		Attempts:         len(result.Attempts),
		Metadata:         responseMetadata(result.Attempts, selectionReason(result.Attempts)),
		CatalogSnapshot:  runtime.Snapshot(),
	}, nil
}

func embeddingPlanningRequest(req *EmbeddingRequest, preferLowestCost bool) routing.Request {
	request := routing.Request{
		Models: []string{req.Model},
		Optimization: routing.OptimizationPolicy{
			PreferLowestCost:    preferLowestCost,
			PreferLowestLatency: true,
		},
	}
	if req.APIKeyConfig != nil {
		request.Account = routing.AccountPolicy{
			AllowedModels:    wildcardAsUnrestricted(req.APIKeyConfig.AllowedModels),
			AllowedProviders: normalizeProviders(req.APIKeyConfig.AllowedProviders),
			ModelOverrides:   cloneModelOverrides(req.APIKeyConfig.ModelOverrides),
			Access:           cloneProviderAccess(req.APIKeyConfig.Access),
		}
	}
	return request
}
