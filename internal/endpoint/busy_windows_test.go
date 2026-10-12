package endpoint

import (
	"errors"
	"net"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/windows"
)

// TestWindowsBusyErrors treats each Windows refusal of a held port as busy,
// so the developer fallback moves to the next port.
func TestWindowsBusyErrors(t *testing.T) {
	for _, errno := range []error{windows.WSAEADDRINUSE, windows.WSAEACCES} {
		err := &net.OpError{Op: "listen", Net: "tcp", Err: os.NewSyscallError("bind", errno)}
		require.True(t, addressInUse(err), "%v is a busy port", errno)
	}
	require.False(t, addressInUse(errors.New("permission denied")))
}
