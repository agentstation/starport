package connectors

import (
	"net"
	"net/http"
	"net/http/httptrace"
	"sync"
	"sync/atomic"
	"time"

	providerauth "github.com/agentstation/starport/internal/providers/auth"
)

// dispatchTransport reserves protocol capacity before credential validation.
// Execution owns retries. Connections never retry a transmitted request here.
type dispatchTransport struct {
	base           *http.Transport
	maxConnections int
	mu             sync.Mutex
	connections    []*dispatchConnection
	dialing        map[string]int
	wake           dispatchWake
}

type dispatchConnection struct {
	conn      *http.ClientConn
	origin    string
	idleSince atomic.Int64
	retired   atomic.Bool
	timer     *time.Timer
}

func newDispatchTransport(base *http.Transport) *dispatchTransport {
	limit := base.MaxConnsPerHost
	base = base.Clone()
	// ClientConn capacity belongs to this pool, not Transport.RoundTrip.
	base.MaxConnsPerHost = 0
	return &dispatchTransport{maxConnections: limit, base: base, dialing: make(map[string]int)}
}

func (t *dispatchTransport) signal() { t.wake.signal() }

func (t *dispatchTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if err := providerauth.CheckRequestValidity(request); err != nil {
		closeRequestBody(request)
		return nil, err
	}
	conn, err := t.reserve(request)
	if err != nil {
		closeRequestBody(request)
		return nil, err
	}
	if err = request.Context().Err(); err == nil {
		err = providerauth.CheckRequestValidity(request)
	}
	if err != nil {
		conn.Release()
		t.signal()
		closeRequestBody(request)
		return nil, err
	}
	return conn.RoundTrip(request)
}

func closeRequestBody(request *http.Request) {
	if request.Body != nil {
		_ = request.Body.Close()
	}
}

func (t *dispatchTransport) reserve(request *http.Request) (*http.ClientConn, error) {
	port := request.URL.Port()
	if port == "" {
		port = "80"
		if request.URL.Scheme == "https" {
			port = "443"
		}
	}
	address := net.JoinHostPort(request.URL.Hostname(), port)
	origin := request.URL.Scheme + "://" + address
	if trace := httptrace.ContextClientTrace(request.Context()); trace != nil && trace.GetConn != nil {
		trace.GetConn(address)
	}
	for {
		revision := t.wake.revision.Load()
		if err := request.Context().Err(); err != nil {
			return nil, err
		}
		if err := providerauth.CheckRequestValidity(request); err != nil {
			return nil, err
		}
		t.mu.Lock()
		count := t.dialing[origin]
		for i := len(t.connections) - 1; i >= 0; i-- {
			entry := t.connections[i]
			if entry.conn.Err() != nil {
				entry.timer.Stop()
				t.connections = append(t.connections[:i], t.connections[i+1:]...)
				continue
			}
			if entry.origin != origin {
				continue
			}
			count++
			if entry.retired.Load() {
				continue
			}
			if entry.conn.Reserve() == nil {
				t.mu.Unlock()
				t.signal()
				return entry.conn, nil
			}
		}
		if t.maxConnections <= 0 || count < t.maxConnections {
			t.dialing[origin]++
			t.mu.Unlock()
			conn, err := t.base.NewClientConn(request.Context(), request.URL.Scheme, address)
			t.mu.Lock()
			t.dialing[origin]--
			if t.dialing[origin] == 0 {
				delete(t.dialing, origin)
			}
			if err == nil {
				t.addConnection(conn, origin)
			}
			t.mu.Unlock()
			t.signal()
			if err != nil {
				return nil, err
			}
			continue
		}
		t.mu.Unlock()
		changed := t.wake.subscribe(revision)
		if changed == nil {
			continue
		}
		select {
		case <-request.Context().Done():
			return nil, request.Context().Err()
		case <-changed:
		}
	}
}

func (t *dispatchTransport) addConnection(conn *http.ClientConn, origin string) {
	entry := &dispatchConnection{conn: conn, origin: origin}
	idle := t.base.IdleConnTimeout
	if idle <= 0 {
		idle = providerIdleConnectionTimeout
	}
	entry.idleSince.Store(time.Now().UnixNano())
	entry.timer = time.AfterFunc(idle, t.maintainIdle)
	t.connections = append(t.connections, entry)
	conn.SetStateHook(func(conn *http.ClientConn) {
		if conn.Err() != nil {
			entry.timer.Reset(0)
		} else if conn.InFlight() == 0 {
			entry.idleSince.Store(time.Now().UnixNano())
			entry.timer.Reset(0)
		}
		t.signal()
	})
}

func (t *dispatchTransport) CloseIdleConnections() {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, entry := range t.connections {
		entry.retired.Store(true)
		if entry.conn.InFlight() == 0 {
			entry.timer.Stop()
			_ = entry.conn.Close()
		}
	}
	t.signal()
}

var _ http.RoundTripper = (*dispatchTransport)(nil)
