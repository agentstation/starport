package app

import (
	"context"
	"github.com/stretchr/testify/require"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"sync/atomic"
	"testing"
	"time"
)

func TestRecoveryActivationBlobTransportConnectionProbe(t *testing.T) {
	target, err := url.Parse(os.Getenv("TEST_BLOB_S3_ENDPOINT"))
	require.NoError(t, err)
	proxy := httputil.NewSingleHostReverseProxy(target)
	transport := http.DefaultTransport.(*http.Transport).Clone()
	proxy.Transport = transport
	defer transport.CloseIdleConnections()
	var requests, connections atomic.Int64
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1); proxy.ServeHTTP(w, r) }))
	server.Config.ConnState = func(_ net.Conn, s http.ConnState) {
		if s == http.StateNew {
			connections.Add(1)
		}
	}
	server.Start()
	defer server.Close()
	t.Setenv("TEST_BLOB_S3_ENDPOINT", server.URL)
	cfg, request := activationFleetFixture(t)
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	result, err := ActivateRecovery(ctx, cfg, request)
	require.NoError(t, err)
	require.True(t, result.HistoricallyComplete)
	t.Logf("native blob requests=%d downstream TCP connections=%d", requests.Load(), connections.Load())
}
