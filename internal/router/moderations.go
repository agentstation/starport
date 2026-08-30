package router

import (
	"context"

	"github.com/agentstation/starmap/pkg/catalogs"

	"github.com/agentstation/starport/internal/inference"
	"github.com/agentstation/starport/internal/providers/connectors"
	"github.com/agentstation/starport/internal/routing"
)

// Moderation is neither chat nor media, and it still plans, retries, selects
// a credential, and spends the same attempt budget as everything else. Like
// rerank, it is written against the shared operation path, which is what
// keeps one retry policy over every operation the gateway serves.

// ModerationRequest routes one moderation call.
type ModerationRequest = OperationRequest[inference.ModerationRequest]

// ModerationResponse is one classified input list with route evidence.
type ModerationResponse = OperationResponse[inference.ModerationResponse]

// RouteModerations classifies one input list at a provider the catalog
// serves the operation for.
func (r *modelRouter) RouteModerations(
	ctx context.Context,
	req *ModerationRequest,
) (*ModerationResponse, error) {
	if req == nil || req.Request.Model == "" {
		return nil, ErrNoModelsAvailable
	}
	call := providerCall[*connectors.ModerationRequest, *connectors.ModerationResponse, inference.ModerationResponse]{
		transport: moderationTransport,
		build: func() *connectors.ModerationRequest {
			return connectors.ModerationRequestFromInference(req.Request)
		},
		convert: connectors.ModerationResponseToInference,
	}
	return routeOperation(ctx, r, req.policy(req.Request.Model), routing.OperationModerations,
		inference.ModerationResponse.Clone, call.attempt(routing.OperationModerations))
}

func moderationTransport(
	connector connectors.Connector,
	endpointType catalogs.EndpointType,
) (providerInvoke[*connectors.ModerationRequest, *connectors.ModerationResponse], bool) {
	moderator, implemented := connectors.ModeratorFor(connector, endpointType)
	if !implemented {
		return nil, false
	}
	return moderator.Moderate, true
}
