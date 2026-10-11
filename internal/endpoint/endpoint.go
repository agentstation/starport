// Package endpoint owns where a gateway listens. It binds the listener before
// anything serves, chooses a free developer port when the standard port is
// busy, and keeps the endpoint record that sibling commands read to find a
// running gateway.
package endpoint

import (
	"context"
	"net"
	"strconv"
)

// BusyError reports a port that another process holds.
type BusyError struct {
	Port int
	Err  error
}

func (e *BusyError) Error() string { return "port " + strconv.Itoa(e.Port) + " is busy" }

func (e *BusyError) Unwrap() error { return e.Err }

// Listen binds a TCP listener on the host and port. A port that another
// process holds returns a *BusyError that names the port.
func Listen(ctx context.Context, host string, port int) (net.Listener, error) {
	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		if addressInUse(err) {
			return nil, &BusyError{Port: port, Err: err}
		}
		return nil, err
	}
	return listener, nil
}

// Port returns the TCP port of a bound address, or 0 for another address.
func Port(address net.Addr) int {
	if tcp, ok := address.(*net.TCPAddr); ok {
		return tcp.Port
	}
	return 0
}
