package endpoint

import (
	"context"
	"errors"
	"net"
)

// The developer fallback tries two fixed alternates, then a bounded number of
// random ports in one range, then any free port.
const (
	randomPortFirst    = 7000
	randomPortCount    = 1000
	randomPortAttempts = 20
)

var fixedFallbackPorts = []int{7373, 7678}

// DeveloperPorts returns the bind order of a development gateway without an
// explicit port: the first port, 7373, 7678, twenty draws from 7000 through
// 7999, then 0, which binds any free port. Draw returns a value in [0, n).
func DeveloperPorts(first int, draw func(n int) int) []int {
	ports := make([]int, 0, 1+len(fixedFallbackPorts)+randomPortAttempts+1)
	ports = append(ports, first)
	ports = append(ports, fixedFallbackPorts...)
	for range randomPortAttempts {
		ports = append(ports, randomPortFirst+draw(randomPortCount))
	}
	return append(ports, 0)
}

// Listener binds one host and port, as Listen does.
type Listener func(ctx context.Context, host string, port int) (net.Listener, error)

// BindFirst binds the first port in order that is free. It moves to the next
// port only after a *BusyError. Any other error stops the search, because the
// next port fails the same way. When every port is busy, the error names the
// first port.
func BindFirst(ctx context.Context, host string, ports []int, listen Listener) (net.Listener, error) {
	var first error
	for _, port := range ports {
		listener, err := listen(ctx, host, port)
		if err == nil {
			return listener, nil
		}
		if _, busy := errors.AsType[*BusyError](err); !busy {
			return nil, err
		}
		if first == nil {
			first = err
		}
	}
	if first == nil {
		return nil, errors.New("no port to bind")
	}
	return nil, first
}
