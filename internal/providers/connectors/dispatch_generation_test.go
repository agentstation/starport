package connectors_test

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/agentstation/starmap"
	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/stretchr/testify/require"

	runtimecatalog "github.com/agentstation/starport/internal/catalog"
	"github.com/agentstation/starport/internal/providers/connectors"
	"github.com/agentstation/starport/internal/registry"
)

const (
	generationCapacity = 2
	generationWaiters  = 3
	generationHold     = "X-Generation-Hold"
)

// generationUpstream records the connection that served each request and the
// server state of each connection. It holds a request that carries the hold
// header until the test releases it.
type generationUpstream struct {
	*httptest.Server
	release chan struct{}
	held    chan struct{}

	mu      sync.Mutex
	served  map[string]string
	states  map[string]http.ConnState
	dialed  int
	arrived int
}

func newGenerationUpstream(t *testing.T) *generationUpstream {
	t.Helper()
	upstream := &generationUpstream{
		release: make(chan struct{}),
		held:    make(chan struct{}, generationCapacity),
		served:  make(map[string]string),
		states:  make(map[string]http.ConnState),
	}
	upstream.Server = httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstream.mu.Lock()
		upstream.served[r.Header.Get("X-Request-Name")] = r.RemoteAddr
		upstream.arrived++
		upstream.mu.Unlock()
		if r.Header.Get(generationHold) != "" {
			upstream.held <- struct{}{}
			<-upstream.release
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	upstream.Config.ConnState = func(conn net.Conn, state http.ConnState) {
		upstream.mu.Lock()
		defer upstream.mu.Unlock()
		if state == http.StateNew {
			upstream.dialed++
		}
		upstream.states[conn.RemoteAddr().String()] = state
	}
	upstream.Start()
	t.Cleanup(upstream.Close)
	return upstream
}

func (u *generationUpstream) servedBy(name string) string {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.served[name]
}

func (u *generationUpstream) counts() (dialed, arrived int) {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.dialed, u.arrived
}

func (u *generationUpstream) closed(addresses ...string) bool {
	u.mu.Lock()
	defer u.mu.Unlock()
	for _, address := range addresses {
		if u.states[address] != http.StateClosed {
			return false
		}
	}
	return true
}

func generationConnector(t *testing.T, baseURL string) *connectors.OpenAIConnector {
	t.Helper()
	connector, err := connectors.NewOpenAIConnector(connectors.ProviderConfig{BaseURL: baseURL, MaxConnections: generationCapacity})
	require.NoError(t, err)
	return connector
}

func generationRegistration(connector connectors.Connector) registry.Registration {
	return registry.Registration{
		Provider: string(catalogs.ProviderIDOpenAI), Connector: connector,
		Operations:    []catalogs.ProviderOperation{catalogs.ProviderOperationChatCompletions},
		EndpointTypes: []catalogs.EndpointType{catalogs.EndpointTypeOpenAI},
	}
}

// generationRequest sends one request through the provider client of the
// connector that the lease retains, as a production attempt does.
func generationRequest(ctx context.Context, lease connectors.RuntimeLease, baseURL, name string, hold bool, trace *httptrace.ClientTrace) error {
	connector := lease.Get(string(catalogs.ProviderIDOpenAI)).(*connectors.OpenAIConnector)
	ctx = connectors.ContextWithRuntimeLease(httptrace.WithClientTrace(ctx, trace), lease)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/v1/models", nil)
	if err != nil {
		return err
	}
	request.Header.Set("X-Request-Name", name)
	if hold {
		request.Header.Set(generationHold, "true")
	}
	response, err := connectors.ProviderHTTPClient(connector).Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if _, err := io.Copy(io.Discard, response.Body); err != nil {
		return err
	}
	if response.StatusCode != http.StatusNoContent {
		return fmt.Errorf("unexpected status %d", response.StatusCode)
	}
	return nil
}

// TestDispatchTransportGenerationChangeSaturation saturates the dispatch
// capacity of one runtime generation with in-flight requests and waiters, and
// then publishes a replacement generation through the registry. A lease
// retains its complete generation, so the admitted requests and the waiters
// finish on the old connections. The last old lease closes the old connector,
// which closes the old idle connections and returns the old accounting to
// zero. New requests use the new generation during and after the drain.
func TestDispatchTransportGenerationChangeSaturation(t *testing.T) {
	upstream := newGenerationUpstream(t)
	client, err := starmap.New()
	require.NoError(t, err)
	plane, err := runtimecatalog.Open(client)
	require.NoError(t, err)
	oldConnector := generationConnector(t, upstream.URL)
	providers, err := registry.Open(plane, []registry.Registration{generationRegistration(oldConnector)})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, providers.Close()) })
	oldClient := connectors.ProviderHTTPClient(oldConnector)

	// The holder lease keeps the old generation open after its requests end.
	holder, err := providers.AcquireRuntime()
	require.NoError(t, err)
	type outcome struct {
		name string
		err  error
	}
	results := make(chan outcome, generationCapacity+generationWaiters)
	send := func(name string, hold bool, queued chan<- struct{}) {
		lease, err := providers.AcquireRuntime()
		require.NoError(t, err)
		trace := &httptrace.ClientTrace{GetConn: func(string) { queued <- struct{}{} }}
		go func() {
			defer lease.Release()
			results <- outcome{name: name, err: generationRequest(t.Context(), lease, upstream.URL, name, hold, trace)}
		}()
	}
	queued := make(chan struct{}, generationCapacity+generationWaiters)
	inFlight := []string{"in-flight-0", "in-flight-1"}
	for _, name := range inFlight {
		send(name, true, queued)
	}
	for range generationCapacity {
		<-upstream.held
	}
	waiters := []string{"waiter-0", "waiter-1", "waiter-2"}
	for _, name := range waiters {
		send(name, false, queued)
	}
	for range generationCapacity + generationWaiters {
		<-queued
	}
	tracked, active, dialing := connectors.DispatchAccounting(oldClient)
	require.Equal(t, []int{generationCapacity, generationCapacity, 0}, []int{tracked, active, dialing}, "the old generation must be saturated")
	dialed, arrived := upstream.counts()
	require.Equal(t, []int{generationCapacity, generationCapacity}, []int{dialed, arrived}, "a waiter must not dial above capacity")
	oldConnections := []string{upstream.servedBy(inFlight[0]), upstream.servedBy(inFlight[1])}

	newConnector := generationConnector(t, upstream.URL)
	candidate, err := providers.Prepare([]registry.Registration{generationRegistration(newConnector)})
	require.NoError(t, err)
	state := client.CurrentCatalogState()
	state.GenerationID += "-replacement"
	state.Sequence++
	require.NoError(t, plane.ValidateRuntime(state, candidate.Availability()))
	snapshot, err := plane.ReplaceRuntime(state, candidate.Availability())
	require.NoError(t, err)
	require.NoError(t, providers.Publish(candidate, snapshot))

	newRequest := func(name string) string {
		lease, err := providers.AcquireRuntime()
		require.NoError(t, err)
		defer lease.Release()
		require.Same(t, newConnector, lease.Get(string(catalogs.ProviderIDOpenAI)))
		require.NoError(t, generationRequest(t.Context(), lease, upstream.URL, name, false, &httptrace.ClientTrace{}))
		return upstream.servedBy(name)
	}
	firstNew := newRequest("new-during-drain")
	require.NotContains(t, oldConnections, firstNew, "a new-generation request must not use an old connection")

	close(upstream.release)
	completed := make([]string, 0, generationCapacity+generationWaiters)
	for range generationCapacity + generationWaiters {
		select {
		case result := <-results:
			require.NoError(t, result.err, result.name)
			completed = append(completed, result.name)
		case <-time.After(10 * time.Second):
			t.Fatalf("an old-generation request did not finish; finished: %v", completed)
		}
	}
	require.ElementsMatch(t, append(slices.Clone(inFlight), waiters...), completed)
	for _, name := range waiters {
		require.Contains(t, oldConnections, upstream.servedBy(name), "%s must reuse an old pooled connection", name)
	}
	tracked, active, dialing = connectors.DispatchAccounting(oldClient)
	require.Equal(t, []int{generationCapacity, 0, 0}, []int{tracked, active, dialing}, "the drained generation keeps its idle connections until its last lease ends")
	require.False(t, upstream.closed(oldConnections[0]) || upstream.closed(oldConnections[1]))

	holder.Release()
	require.Eventually(t, func() bool {
		tracked, active, dialing := connectors.DispatchAccounting(oldClient)
		return tracked == 0 && active == 0 && dialing == 0
	}, 5*time.Second, time.Millisecond, "the old generation accounting must return to zero")
	require.Eventually(t, func() bool { return upstream.closed(oldConnections...) }, 5*time.Second, time.Millisecond, "the old idle connections must close")

	require.Equal(t, firstNew, newRequest("new-after-drain"), "the new generation must reuse its pooled connection")
	tracked, active, dialing = connectors.DispatchAccounting(connectors.ProviderHTTPClient(newConnector))
	require.Equal(t, []int{1, 0, 0}, []int{tracked, active, dialing})
	dialed, _ = upstream.counts()
	require.Equal(t, generationCapacity+1, dialed, "only the new generation dials after saturation")
}
