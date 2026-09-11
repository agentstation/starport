package router

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/agentstation/starmap"
	"github.com/agentstation/starmap/pkg/catalogs"
	runtimecatalog "github.com/agentstation/starport/internal/catalog"
	"github.com/agentstation/starport/internal/credentials"
	"github.com/agentstation/starport/internal/execution"
	"github.com/agentstation/starport/internal/failure"
	"github.com/agentstation/starport/internal/inference"
	"github.com/agentstation/starport/internal/providers/connectors"
	"github.com/agentstation/starport/internal/providers/keyring"
	"github.com/agentstation/starport/internal/routing"
	"github.com/stretchr/testify/require"
)

type routerPermissionSource struct {
	state   starmap.CatalogState
	allowed atomic.Bool
}

func (s *routerPermissionSource) CurrentCatalogState() starmap.CatalogState { return s.state }
func (s *routerPermissionSource) AllowsCatalogAttempt(catalogs.CatalogAuthorityHead) bool {
	return s.allowed.Load()
}

type routerPermissionLease struct {
	connectors.RuntimeLease
	snapshot  *runtimecatalog.RoutableSnapshot
	onResolve func()
}

func (l routerPermissionLease) Snapshot() *runtimecatalog.RoutableSnapshot { return l.snapshot }

func (l routerPermissionLease) ResolveMaterial(ctx context.Context, provider string) (credentials.Material, error) {
	material, err := l.RuntimeLease.ResolveMaterial(ctx, provider)
	if l.onResolve != nil {
		l.onResolve()
	}
	return material, err
}

func TestRouterRechecksCatalogPermissionBeforeAttempts(t *testing.T) {
	for _, mode := range []string{"selection", "chat first attempt", "stream first attempt", "chat retry", "stream retry"} {
		t.Run(mode, func(t *testing.T) {
			fixture := newEndpointBindingFixture(t)
			r := fixture.router.(*modelRouter)
			original := r.catalog.Current()
			source := &routerPermissionSource{state: starmap.CatalogState{
				Catalog: original.Catalog(), GenerationID: original.GenerationID(), Sequence: original.CatalogSequence(),
				PayloadChecksum: original.PayloadChecksum(), GeneratedAt: original.GeneratedAt(),
			}}
			source.allowed.Store(mode != "selection")
			plane, err := runtimecatalog.Open(source)
			require.NoError(t, err)
			offerings, err := original.Catalog().ProviderOfferings("acme")
			require.NoError(t, err)
			require.NoError(t, plane.SetAdapter(routerAdapterAvailability("acme", offerings[0])))
			lease, err := r.registry.(connectors.LeasingRegistry).AcquireRuntime()
			require.NoError(t, err)
			defer lease.Release()
			bound := routerPermissionLease{RuntimeLease: lease, snapshot: plane.Current()}
			if mode == "chat first attempt" || mode == "stream first attempt" {
				bound.onResolve = func() { source.allowed.Store(false) }
			}
			ctx := connectors.ContextWithRuntimeLease(t.Context(), bound)
			config := execution.DefaultConfig()
			config.MaxRetriesPerRoute = 1
			config.RetryBackoff = 0
			r.executor, err = execution.New(config, nil, nil, nil)
			require.NoError(t, err)
			connector := r.registry.Get("acme").(*mockConnector)
			var calls int
			fail := func() error {
				calls++
				source.allowed.Store(false)
				return &connectors.APIError{StatusCode: 429, Message: "retry fixture"}
			}
			connector.chatFunc = func(context.Context, *connectors.ChatRequest) (*connectors.ChatResponse, error) { return nil, fail() }
			connector.chatStreamFunc = func(context.Context, *connectors.ChatRequest) (connectors.ChatStream, error) { return nil, fail() }
			switch mode {
			case "selection":
				_, _, err = fixture.router.SelectModel(ctx, fixture.request(keyring.OperatorFirst))
			case "chat retry", "chat first attempt":
				_, err = fixture.router.RouteWithFallback(ctx, fixture.request(keyring.OperatorFirst))
			case "stream retry", "stream first attempt":
				stream, streamErr := fixture.router.RouteStream(ctx, fixture.request(keyring.OperatorFirst))
				if stream != nil {
					require.NoError(t, stream.Close())
				}
				err = streamErr
			}
			require.Error(t, err)
			var refusal *failure.Failure
			require.ErrorAs(t, err, &refusal)
			require.Equal(t, failure.Kind("gateway_unavailable"), refusal.Kind())
			require.True(t, refusal.Retryable())
			require.Equal(t, failure.ScopeNone, refusal.ProviderDetails().StateScope)
			if mode == "selection" || bound.onResolve != nil {
				require.Zero(t, calls)
			} else {
				require.Equal(t, 1, calls)
			}
		})
	}
}

func TestEmbeddingAndGenericAttemptsRecheckPermission(t *testing.T) {
	for _, generic := range []bool{false, true} {
		name := "embedding"
		if generic {
			name = "generic operation"
		}
		t.Run(name, func(t *testing.T) {
			original := embeddingTestCatalogPlane(t).Current()
			source := &routerPermissionSource{state: starmap.CatalogState{Catalog: original.Catalog(), GenerationID: original.GenerationID(), Sequence: original.CatalogSequence(), GeneratedAt: original.GeneratedAt()}}
			source.allowed.Store(true)
			plane, err := runtimecatalog.Open(source)
			require.NoError(t, err)
			offerings, err := original.Catalog().ProviderOfferings("acme")
			require.NoError(t, err)
			require.NoError(t, plane.SetAdapter(routerAdapterAvailability("acme", offerings[0])))
			calls := 0
			connector := &mockConnector{name: "acme", embeddingsFunc: func(context.Context, *connectors.EmbeddingsRequest) (*connectors.EmbeddingsResponse, error) {
				calls++
				source.allowed.Store(false)
				return nil, &connectors.APIError{StatusCode: 429, Message: "retry fixture"}
			}}
			runtime := &embeddingTestRuntime{snapshot: plane.Current(), connector: connector, operator: embeddingTestMaterial("operator")}
			r := New(&embeddingTestRegistry{runtime: runtime}, WithCatalog(plane), func(r *modelRouter) {
				r.config.Execution.MaxRetriesPerRoute = 1
				r.config.Execution.RetryBackoff = 0
			}).(*modelRouter)
			if generic {
				request := &OperationRequest[inference.EmbeddingRequest]{Request: inference.EmbeddingRequest{Model: "author/embed"}}
				_, err = routeOperation(t.Context(), r, request.policy("author/embed"), routing.OperationEmbeddings, inference.EmbeddingResponse.Clone,
					func(context.Context, connectors.Connector, routing.Route, credentialSelection) (*inference.EmbeddingResponse, *failure.Failure, execution.AttemptAction) {
						calls++
						source.allowed.Store(false)
						return nil, failure.New(failure.RateLimit, "retry fixture", true, failure.ProviderDetails{}, nil), execution.AttemptActionDefault
					})
			} else {
				_, err = r.RouteEmbeddings(t.Context(), &EmbeddingRequest{EmbeddingsRequest: &connectors.EmbeddingsRequest{Model: "author/embed", Input: "hello"}})
			}
			require.Error(t, err)
			var refusal *failure.Failure
			require.ErrorAs(t, err, &refusal)
			require.Equal(t, failure.GatewayUnavailable, refusal.Kind())
			require.Equal(t, 1, calls)
		})
	}
}
