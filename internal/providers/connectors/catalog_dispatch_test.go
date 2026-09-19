package connectors

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/agentstation/starmap"
	"github.com/agentstation/starmap/pkg/catalogs"
	runtimecatalog "github.com/agentstation/starport/internal/catalog"
	"github.com/agentstation/starport/internal/failure"
	"github.com/stretchr/testify/require"
)

type dispatchPermissionSource struct {
	state   starmap.CatalogState
	allowed atomic.Bool
}

func (s *dispatchPermissionSource) CurrentCatalogState() starmap.CatalogState { return s.state }
func (s *dispatchPermissionSource) AllowsCatalogAttempt(catalogs.CatalogAuthorityHead) bool {
	return s.allowed.Load()
}

type dispatchPermissionLease struct {
	RuntimeLease
	snapshot *runtimecatalog.RoutableSnapshot
}

func (l dispatchPermissionLease) Snapshot() *runtimecatalog.RoutableSnapshot { return l.snapshot }

func TestDispatchRejectsCatalogWithdrawalWhileWaitingForConnection(t *testing.T) {
	for _, protocol := range []string{"http1", "http2"} {
		t.Run(protocol, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			catalog, err := catalogs.NewEmpty().Build()
			require.NoError(t, err)
			source := &dispatchPermissionSource{state: starmap.CatalogState{Catalog: catalog, GenerationID: "admitted"}}
			source.allowed.Store(true)
			plane, err := runtimecatalog.Open(source)
			require.NoError(t, err)
			ctx = ContextWithRuntimeLease(ctx, dispatchPermissionLease{snapshot: plane.Current()})
			release := make(chan struct{})
			var releaseOnce sync.Once
			var received atomic.Int64
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if protocol == "http2" && r.ProtoMajor != 2 {
					t.Errorf("expected HTTP/2, got %s", r.Proto)
				}
				if received.Add(1) == 1 {
					_, _ = io.WriteString(w, "start")
					w.(http.Flusher).Flush()
					select {
					case <-release:
					case <-r.Context().Done():
						return
					}
					_, _ = io.WriteString(w, "finish")
					return
				}
				w.WriteHeader(http.StatusNoContent)
			}))
			if protocol == "http2" {
				server.EnableHTTP2 = true
				server.Config.HTTP2 = &http.HTTP2Config{MaxConcurrentStreams: 1}
				server.StartTLS()
			} else {
				server.Start()
			}
			defer server.Close()
			defer releaseOnce.Do(func() { close(release) })
			transport := server.Client().Transport.(*http.Transport).Clone()
			transport.MaxConnsPerHost = 1
			transport.ForceAttemptHTTP2 = true
			client := &http.Client{Transport: newDispatchTransport(transport)}
			defer client.CloseIdleConnections()
			first, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL, nil)
			require.NoError(t, err)
			response, err := client.Do(first)
			require.NoError(t, err)
			defer response.Body.Close()
			waiting := make(chan struct{})
			trace := &httptrace.ClientTrace{GetConn: func(string) { close(waiting) }}
			queued, err := http.NewRequestWithContext(httptrace.WithClientTrace(ctx, trace), http.MethodGet, server.URL, nil)
			require.NoError(t, err)
			result := make(chan error, 1)
			go func() {
				response, err := client.Do(queued)
				if response != nil {
					_ = response.Body.Close()
				}
				result <- err
			}()
			select {
			case <-waiting:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			source.allowed.Store(false)
			releaseOnce.Do(func() { close(release) })
			body, err := io.ReadAll(response.Body)
			require.NoError(t, err)
			require.NoError(t, response.Body.Close())
			require.Equal(t, "startfinish", string(body))
			select {
			case err := <-result:
				var refusal *failure.Failure
				require.ErrorAs(t, err, &refusal)
				require.Equal(t, failure.GatewayUnavailable, refusal.Kind())
				require.Equal(t, failure.ScopeNone, refusal.ProviderDetails().StateScope)
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			require.EqualValues(t, 1, received.Load())
		})
	}
}
