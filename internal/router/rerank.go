package router

import (
	"context"

	"github.com/agentstation/starmap/pkg/catalogs"

	"github.com/agentstation/starport/internal/inference"
	"github.com/agentstation/starport/internal/providers/connectors"
	"github.com/agentstation/starport/internal/routing"
)

// Reranking is neither chat nor media, and it still plans, retries, selects a
// credential, and spends the same attempt budget as everything else. It is
// therefore written against the shared operation path rather than beside it,
// which is what keeps one retry policy over every operation the gateway serves.

// RerankRequest routes one rerank call.
type RerankRequest = OperationRequest[inference.RerankRequest]

// RerankResponse is one ranked document list with route evidence.
type RerankResponse = OperationResponse[inference.RerankResponse]

// RouteRerank scores one document list against a query at a provider the
// catalog serves the operation for.
func (r *modelRouter) RouteRerank(ctx context.Context, req *RerankRequest) (*RerankResponse, error) {
	if req == nil || req.Request.Model == "" {
		return nil, ErrNoModelsAvailable
	}
	call := providerCall[*connectors.RerankRequest, *connectors.RerankResponse, inference.RerankResponse]{
		transport: rerankTransport,
		build: func() *connectors.RerankRequest {
			return connectors.RerankRequestFromInference(req.Request)
		},
		convert: connectors.RerankResponseToInference,
	}
	return routeOperation(ctx, r, req.policy(req.Request.Model), routing.OperationRerank,
		inference.RerankResponse.Clone, call.attempt(routing.OperationRerank))
}

func rerankTransport(
	connector connectors.Connector,
	endpointType catalogs.EndpointType,
) (providerInvoke[*connectors.RerankRequest, *connectors.RerankResponse], bool) {
	reranker, implemented := connectors.RerankerFor(connector, endpointType)
	if !implemented {
		return nil, false
	}
	return reranker.Rerank, true
}
