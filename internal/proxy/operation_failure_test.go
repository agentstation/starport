package proxy

import (
	"errors"
	"fmt"
	"testing"

	runtimecatalog "github.com/agentstation/starport/internal/catalog"
	"github.com/agentstation/starport/internal/routing"
	"github.com/stretchr/testify/require"
)

// TestRouteFailureSeparatesCallerErrorsFromGatewayReports holds the three
// answers this seam produces. Two are caller errors that no retry changes, and
// wrapping them as routing failures would answer both with "the gateway could
// not reach a provider, try again later".
func TestRouteFailureSeparatesCallerErrorsFromGatewayReports(t *testing.T) {
	const model = "author/rerank-model"

	// A model that serves other operations reads as a fault in the model the
	// caller named, so the refusal names that field and carries the planner's
	// message through unchanged.
	refusal := fmt.Errorf(
		"%w: %w: provider/opaque/chat@001: offering does not serve the rerank operation",
		routing.ErrNoCandidate, routing.ErrOperationUnsupported,
	)
	var validation *ValidationError
	require.ErrorAs(t, routeFailure(model, refusal), &validation)
	require.Equal(t, fieldModel, validation.Field)
	require.Contains(t, validation.Message, "rerank")
	require.Contains(t, validation.Message, "provider/opaque/chat@001")

	// A name the catalog never held passes through, so the controller can
	// answer it as a missing resource rather than as a busy gateway.
	notCatalogued := fmt.Errorf("%w: %s", runtimecatalog.ErrModelNotCatalogued, model)
	require.ErrorIs(t, routeFailure(model, notCatalogued), runtimecatalog.ErrModelNotCatalogued)

	// Everything else is a report about the gateway, and it keeps the model so
	// an operator reading the answer knows which route failed.
	var routingErr *RoutingError
	require.ErrorAs(t, routeFailure(model, errors.New("all providers refused")), &routingErr)
	require.Equal(t, model, routingErr.Model)
}
