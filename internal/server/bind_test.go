package server

import (
	"context"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestServerBindServeClose pins the listener contract. Bind returns the
// address that the gateway holds before anything serves, Serve answers on that
// address, and Close releases a listener that never served.
func TestServerBindServeClose(t *testing.T) {
	t.Run("serve the bound address", func(t *testing.T) {
		gateway := newTestServer(t, &Config{Host: "127.0.0.1", ShutdownTimeout: time.Second})
		address, err := gateway.Bind()
		require.NoError(t, err)
		require.NotZero(t, address.(*net.TCPAddr).Port, "bind returns the assigned port, not the configured 0")
		again, err := gateway.Bind()
		require.NoError(t, err)
		require.Equal(t, address.String(), again.String(), "a second bind keeps the first listener")

		served := make(chan error, 1)
		go func() { served <- gateway.Serve() }()
		client := &http.Client{Timeout: 3 * time.Second}
		response, err := client.Get("http://" + address.String() + "/health/live")
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, response.StatusCode)
		require.NoError(t, response.Body.Close())

		ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
		defer cancel()
		require.NoError(t, gateway.Shutdown(ctx))
		select {
		case err := <-served:
			require.NoError(t, err)
		case <-time.After(3 * time.Second):
			t.Fatal("serve did not return after shutdown")
		}
		require.NoError(t, gateway.Close())
	})

	t.Run("close releases an unserved listener", func(t *testing.T) {
		gateway := newTestServer(t, &Config{Host: "127.0.0.1", ShutdownTimeout: time.Second})
		address, err := gateway.Bind()
		require.NoError(t, err)

		require.NoError(t, gateway.Close())
		require.NoError(t, gateway.Close(), "close is idempotent")
		reclaimed, err := net.Listen("tcp", address.String())
		require.NoError(t, err, "close left the port bound")
		require.NoError(t, reclaimed.Close())
		require.Error(t, gateway.Serve(), "a closed gateway does not bind again")
	})
}
