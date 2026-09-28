package catalog

import (
	"io"
	"net"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// fleetBackendProxy changes the actual server behind an unchanged client address.
// It closes old connections so the native identity check must run on the new process.
type fleetBackendProxy struct {
	listener    net.Listener
	mu          sync.Mutex
	target      string
	closed      bool
	connections map[net.Conn]bool
	workers     sync.WaitGroup
}

func newFleetBackendProxy(t *testing.T, address string) (*fleetBackendProxy, string) {
	t.Helper()
	upstream, err := url.Parse(address)
	require.NoError(t, err)
	require.Equal(t, "redis", upstream.Scheme)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	p := &fleetBackendProxy{listener: listener, target: upstream.Host, connections: make(map[net.Conn]bool)}
	p.workers.Go(func() {
		for {
			client, err := listener.Accept()
			if err != nil {
				return
			}
			p.mu.Lock()
			if p.closed {
				p.mu.Unlock()
				_ = client.Close()
				return
			}
			p.connections[client] = true
			target := p.target
			p.mu.Unlock()
			p.workers.Go(func() { p.relay(client, target) })
		}
	})
	t.Cleanup(func() {
		p.mu.Lock()
		p.closed = true
		_ = p.listener.Close()
		for c := range p.connections {
			_ = c.Close()
		}
		p.mu.Unlock()
		p.workers.Wait()
	})
	upstream.Host = listener.Addr().String()
	return p, upstream.String()
}

func (p *fleetBackendProxy) relay(client net.Conn, target string) {
	server, err := net.DialTimeout("tcp", target, time.Second)
	if err != nil {
		_ = client.Close()
		p.mu.Lock()
		delete(p.connections, client)
		p.mu.Unlock()
		return
	}
	p.mu.Lock()
	if p.closed || !p.connections[client] || p.target != target {
		delete(p.connections, client)
		p.mu.Unlock()
		_ = client.Close()
		_ = server.Close()
		return
	}
	p.connections[server] = true
	p.mu.Unlock()
	finish := sync.OnceFunc(func() {
		_ = client.Close()
		_ = server.Close()
		p.mu.Lock()
		delete(p.connections, client)
		delete(p.connections, server)
		p.mu.Unlock()
	})
	p.workers.Go(func() { defer finish(); _, _ = io.Copy(client, server) })
	defer finish()
	_, _ = io.Copy(server, client)
}

func (p *fleetBackendProxy) selectBackend(target string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.target = target
	for connection := range p.connections {
		_ = connection.Close()
	}
	clear(p.connections)
}
