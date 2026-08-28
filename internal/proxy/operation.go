package proxy

import (
	"context"
	"errors"

	runtimecatalog "github.com/agentstation/starport/internal/catalog"
	"github.com/agentstation/starport/internal/router"
)

// processOperation hands one validated request to the router and carries the
// route evidence back. Images, speech, transcription, and reranking differ only
// in the router method they call, so the identity transfer and the failure
// wrapping are written once.
func processOperation[Request, Response any](
	ctx context.Context,
	req *OperationRequest[Request],
	model string,
	route func(context.Context, *router.OperationRequest[Request]) (*router.OperationResponse[Response], error),
) (*OperationResponse[Response], error) {
	result, err := route(ctx, &router.OperationRequest[Request]{
		Request:      req.Request,
		APIKeyConfig: transformAPIKeyConfig(req.APIKeyConfig),
		TenantID:     req.TenantID,
	})
	if err != nil {
		return nil, routeFailure(model, err)
	}
	response := &OperationResponse[Response]{
		Response:         result.Response,
		ModelUsed:        result.ModelUsed,
		ProviderUsed:     result.ProviderUsed,
		CredentialSource: result.CredentialSource,
		Attempts:         result.Attempts,
		CatalogSnapshot:  result.CatalogSnapshot,
	}
	if result.Metadata != nil {
		response.RoutingDuration = result.Metadata.RoutingDuration
	}
	return response, nil
}

// routeFailure names one failed route. A model the catalog does not hold is
// passed through rather than wrapped, because it is a caller error about a
// name and every other route failure is a report that the gateway could not
// reach a provider. Wrapping the two alike would answer a misspelled model with
// "try again later".
func routeFailure(model string, err error) error {
	if errors.Is(err, runtimecatalog.ErrModelNotCatalogued) {
		return err
	}
	return &RoutingError{Model: model, Reason: "failed to route request", Err: err}
}
