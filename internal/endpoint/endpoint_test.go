package endpoint

import (
	"errors"
	"net"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestListenReportsTheBusyPort(t *testing.T) {
	held, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = held.Close() })
	port := Port(held.Addr())

	_, err = Listen(t.Context(), "127.0.0.1", port)
	busy, ok := errors.AsType[*BusyError](err)
	require.True(t, ok, "listen error = %v, want a busy port", err)
	require.Equal(t, port, busy.Port)
	require.NotNil(t, errors.Unwrap(err), "the busy error keeps its cause")

	listener, err := Listen(t.Context(), "127.0.0.1", 0)
	require.NoError(t, err)
	require.NotZero(t, Port(listener.Addr()))
	require.NoError(t, listener.Close())
}
