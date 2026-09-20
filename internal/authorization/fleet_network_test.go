package authorization

import (
	"context"
	"io"
	"net"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// fleetNetwork cuts one replica's TCP access without changing either authority.
type fleetNetwork struct {
	mu          sync.Mutex
	listener    net.Listener
	target      string
	blocked     bool
	connections map[net.Conn]struct{}
	workers     sync.WaitGroup
}

func newFleetNetwork(t *testing.T, address string) (*fleetNetwork, string) {
	t.Helper()
	parsed, err := url.Parse(address)
	require.NoError(t, err)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	network := &fleetNetwork{listener: listener, target: parsed.Host, connections: make(map[net.Conn]struct{})}
	network.workers.Go(func() {
		for {
			connection, err := listener.Accept()
			if err != nil {
				return
			}
			network.mu.Lock()
			if network.blocked {
				network.mu.Unlock()
				_ = connection.Close()
				continue
			}
			network.connections[connection] = struct{}{}
			network.mu.Unlock()
			network.workers.Go(func() { network.forward(connection) })
		}
	})
	t.Cleanup(func() {
		_ = listener.Close()
		network.cut()
		network.workers.Wait()
	})
	parsed.Host = listener.Addr().String()
	return network, parsed.String()
}

func (n *fleetNetwork) forward(client net.Conn) {
	defer func() {
		_ = client.Close()
		n.mu.Lock()
		delete(n.connections, client)
		n.mu.Unlock()
	}()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	dialer := net.Dialer{}
	upstream, err := dialer.DialContext(ctx, "tcp", n.target)
	if err != nil {
		return
	}
	defer func() { _ = upstream.Close() }()
	n.mu.Lock()
	if n.blocked {
		n.mu.Unlock()
		return
	}
	n.connections[upstream] = struct{}{}
	n.mu.Unlock()
	defer func() { n.mu.Lock(); delete(n.connections, upstream); n.mu.Unlock() }()
	done := make(chan struct{})
	go func() { _, _ = io.Copy(upstream, client); _ = upstream.Close(); close(done) }()
	_, _ = io.Copy(client, upstream)
	_ = client.Close()
	<-done
}

func (n *fleetNetwork) cut() {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.blocked = true
	for connection := range n.connections {
		_ = connection.Close()
	}
}

func (n *fleetNetwork) restore() {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.blocked = false
}
