package server

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/agentstation/starport/internal/endpoint"
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

	t.Run("busy port names the port", func(t *testing.T) {
		held, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
		require.NoError(t, err)
		t.Cleanup(func() { _ = held.Close() })
		port := held.Addr().(*net.TCPAddr).Port

		gateway := newTestServer(t, &Config{Host: "127.0.0.1", Port: port, ShutdownTimeout: time.Second})
		_, err = gateway.Bind()
		busy, ok := errors.AsType[*endpoint.BusyError](err)
		require.True(t, ok, "bind error = %v, want a busy port", err)
		require.Equal(t, port, busy.Port)
		require.EqualError(t, gateway.Serve(), "port "+strconv.Itoa(port)+" is busy")
	})

	t.Run("serve the caller listener", func(t *testing.T) {
		bound, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
		require.NoError(t, err)
		gateway := newTestServer(t, &Config{Host: "127.0.0.1", Port: 7827, Listener: bound, ShutdownTimeout: time.Second})
		address, err := gateway.Bind()
		require.NoError(t, err)
		require.Equal(t, bound.Addr().String(), address.String(), "bind keeps the caller listener, not the configured port")
		require.NoError(t, gateway.Close())
		_, err = bound.Accept()
		require.ErrorIs(t, err, net.ErrClosed, "close releases the caller listener")
	})
}
