package router

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	runtimecatalog "github.com/agentstation/starport/internal/catalog"
	"github.com/agentstation/starport/internal/credentials"
	"github.com/agentstation/starport/internal/providers/connectors"
)

func TestRouterRetainsRuntimeLeaseThroughRequestAndStream(t *testing.T) {
	t.Run("chat", func(t *testing.T) {
		connector := newBlockingLeaseConnector()
		registry := &leaseTestRegistry{connector: connector}
		modelRouter := New(registry)
		result := make(chan error, 1)
		go func() {
			_, err := modelRouter.RouteWithFallback(t.Context(), leaseTestRequest(false))
			result <- err
		}()
		<-connector.entered
		lease := registry.currentLease()
		require.NotNil(t, lease)
		require.False(t, lease.released.Load())
		close(connector.continueRequest)
		require.NoError(t, <-result)
		require.True(t, lease.released.Load())
	})

	t.Run("stream", func(t *testing.T) {
		registry := &leaseTestRegistry{
			connector: connectors.NewMockConnector(connectors.ProviderConfig{}),
		}
		modelRouter := New(registry)
		stream, err := modelRouter.RouteStream(t.Context(), leaseTestRequest(true))
		require.NoError(t, err)
		lease := registry.currentLease()
		require.NotNil(t, lease)
		require.False(t, lease.released.Load())
		require.NoError(t, stream.Close())
		require.True(t, lease.released.Load())
	})

	t.Run("borrowed request runtime", func(t *testing.T) {
		registry := &leaseTestRegistry{
			connector: connectors.NewMockConnector(connectors.ProviderConfig{}),
		}
		borrowed := &leaseTestRuntime{registry: registry}
		ctx := connectors.ContextWithRuntimeLease(t.Context(), borrowed)
		modelRouter := New(registry)

		_, err := modelRouter.RouteWithFallback(ctx, leaseTestRequest(false))
		require.NoError(t, err)
		require.Nil(t, registry.currentLease())
		require.False(t, borrowed.released.Load())
		borrowed.Release()
		require.True(t, borrowed.released.Load())
	})
}

func leaseTestRequest(stream bool) *Request {
	return &Request{
		ChatRequest: &connectors.ChatRequest{
			Model: "acme/model", Stream: stream,
			Messages: []connectors.Message{{Role: connectors.RoleUser, Content: "hello"}},
		},
		Models: []string{"acme/model"},
	}
}

type leaseTestRegistry struct {
	connector connectors.Connector

	mu    sync.Mutex
	lease *leaseTestRuntime
}

func (r *leaseTestRegistry) Get(provider string) connectors.Connector {
	if provider == "acme" {
		return r.connector
	}
	return nil
}

func (r *leaseTestRegistry) List() []string { return []string{"acme"} }

func (r *leaseTestRegistry) ResolveMaterial(
	context.Context,
	string,
) (credentials.Material, error) {
	return routerTestMaterial(), nil
}

func (r *leaseTestRegistry) AcquireRuntime() (connectors.RuntimeLease, error) {
	lease := &leaseTestRuntime{registry: r}
	r.mu.Lock()
	r.lease = lease
	r.mu.Unlock()
	return lease, nil
}

func (r *leaseTestRegistry) currentLease() *leaseTestRuntime {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.lease
}

type leaseTestRuntime struct {
	registry *leaseTestRegistry
	released atomic.Bool
}

func (l *leaseTestRuntime) Snapshot() *runtimecatalog.RoutableSnapshot { return nil }

func (l *leaseTestRuntime) Get(provider string) connectors.Connector {
	return l.registry.Get(provider)
}

func (*leaseTestRuntime) RequiresAuthentication(string) bool { return false }

func (l *leaseTestRuntime) ResolveMaterial(
	ctx context.Context,
	provider string,
) (credentials.Material, error) {
	return l.registry.ResolveMaterial(ctx, provider)
}

func (l *leaseTestRuntime) Release() { l.released.Store(true) }

type blockingLeaseConnector struct {
	connectors.Connector
	entered         chan struct{}
	continueRequest chan struct{}
}

func newBlockingLeaseConnector() *blockingLeaseConnector {
	return &blockingLeaseConnector{
		Connector:       connectors.NewMockConnector(connectors.ProviderConfig{}),
		entered:         make(chan struct{}),
		continueRequest: make(chan struct{}),
	}
}

func (c *blockingLeaseConnector) Chat(
	ctx context.Context,
	request *connectors.ChatRequest,
) (*connectors.ChatResponse, error) {
	close(c.entered)
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-c.continueRequest:
		return c.Connector.Chat(ctx, request)
	}
}
