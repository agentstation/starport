package router

import (
	"context"
	"fmt"
	"testing"

	"github.com/agentstation/starmap/pkg/catalogs"

	"github.com/agentstation/starport/internal/credentials"
	"github.com/agentstation/starport/internal/execution"
	"github.com/agentstation/starport/internal/failure"
	"github.com/agentstation/starport/internal/inference"
	"github.com/agentstation/starport/internal/providers/connectors"
	"github.com/agentstation/starport/internal/routing"
	"github.com/stretchr/testify/require"
)

// TestRoutePlanFailureKeepsTheOperationRefusal holds the classification a
// rerank caller depends on. Collapsing the refusal onto ErrNoModelsAvailable
// answers a chat model asked to rerank with a 503, which tells the caller to
// retry a request that fails the same way every time.
func TestRoutePlanFailureKeepsTheOperationRefusal(t *testing.T) {
	refusal := fmt.Errorf(
		"%w: %w: provider/opaque/chat@001: offering does not serve the rerank operation",
		routing.ErrNoCandidate, routing.ErrOperationUnsupported,
	)

	mapped := routePlanFailure(refusal)
	require.ErrorIs(t, mapped, routing.ErrOperationUnsupported)
	require.Contains(t, mapped.Error(), "rerank")
	require.Contains(t, mapped.Error(), "provider/opaque/chat@001")

	mapped = routePlanFailure(fmt.Errorf("%w: 2 route(s) rejected", routing.ErrNoCandidate))
	require.ErrorIs(t, mapped, ErrNoModelsAvailable)
	require.NotErrorIs(t, mapped, routing.ErrOperationUnsupported)
}

// boundTestRequest is the smallest provider request the shared attempt accepts.
// It records whether the attempt got far enough to bind a credential, which is
// the step after the bound check.
type boundTestRequest struct{ bound bool }

func (r *boundTestRequest) Bind(string, connectors.InferenceEndpoint, credentials.Material) {
	r.bound = true
}

// TestTheDocumentBoundRefusesBeforeTheProviderCall proves the bound is read off
// the route the planner chose, and that it refuses before anything is spent. A
// check placed after the provider call would report the same message and still
// have paid for the request.
func TestTheDocumentBoundRefusesBeforeTheProviderCall(t *testing.T) {
	request := inference.RerankRequest{
		Model:     "author/rerank-model",
		Query:     "q",
		Documents: []string{"a", "b", "c"},
	}
	invoked := 0
	built := &boundTestRequest{}
	call := providerCall[*boundTestRequest, string, string]{
		transport: func(connectors.Connector, catalogs.EndpointType) (providerInvoke[*boundTestRequest, string], bool) {
			return func(context.Context, *boundTestRequest) (string, error) {
				invoked++
				return "", nil
			}, true
		},
		build:   func() *boundTestRequest { return built },
		convert: func(answer string) (string, error) { return answer, nil },
		bounded: func(route routing.Route) error {
			return request.CheckDocumentBound(route.MaxDocuments)
		},
	}
	attempt := call.attempt(routing.OperationRerank)
	route := func(provider string, maxDocuments int) routing.Route {
		return routing.Route{
			ProviderID:   provider,
			MaxDocuments: maxDocuments,
			Endpoint:     routing.Endpoint{Protocol: "cohere", URL: "https://" + provider + ".test/rerank"},
		}
	}

	// The offering whose bound is below the list refuses it. Nothing was
	// invoked and nothing was bound, which is the whole reason the check sits
	// where it does.
	answer, refusal, action := attempt(context.Background(), nil, route("cohere", 2), credentialSelection{})
	require.Nil(t, answer)
	require.NotNil(t, refusal)
	require.Zero(t, invoked)
	require.False(t, built.bound)

	// The fault is the caller's, so it answers as a validation error rather
	// than as an unavailable gateway. It stays retryable, because a second
	// offering of the same model may state a larger bound.
	require.Equal(t, failure.Validation, refusal.Kind())
	require.True(t, refusal.Retryable())
	require.Equal(t, execution.AttemptActionDefault, action)
	require.Contains(t, refusal.SafeMessage(), "3")
	require.Contains(t, refusal.SafeMessage(), "2")

	// That second offering accepts the same list, so the refusal belongs to the
	// offering rather than to the request.
	answer, refusal, _ = attempt(context.Background(), nil, route("voyage", 1000), credentialSelection{})
	require.Nil(t, refusal)
	require.NotNil(t, answer)
	require.Equal(t, 1, invoked)

	// An offering whose catalog entry states no bound accepts it too. Reading
	// zero as "no documents allowed" would refuse every request to a model the
	// catalog has not described yet.
	_, refusal, _ = attempt(context.Background(), nil, route("jina", 0), credentialSelection{})
	require.Nil(t, refusal)
	require.Equal(t, 2, invoked)
}
