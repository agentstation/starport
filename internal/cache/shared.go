package cache

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/agentstation/starport/internal/cache/connection"
	"github.com/valkey-io/valkey-go"
)

var errSharedCacheUnavailable = errors.New("shared cache unavailable")

// SharedStatus reports connection health without endpoint or credential values.
type SharedStatus struct {
	Configured bool `json:"configured"`
	Available  bool `json:"available"`
}

// SharedStore owns an optional connection independently of authoritative storage.
type SharedStore struct {
	mu        sync.RWMutex
	client    valkey.Client
	cancel    context.CancelFunc
	workers   sync.WaitGroup
	once      sync.Once
	prefix    string
	available atomic.Bool
	closed    atomic.Bool
}

// OpenShared starts bounded connection work without delaying application startup.
func OpenShared(raw, namespace string, allowInsecure bool) (*SharedStore, error) {
	u, err := connection.Parse(raw, namespace, allowInsecure)
	if err != nil {
		return nil, err
	}
	options, err := valkey.ParseURL(u.String())
	if err != nil {
		return nil, errors.New("cache connection settings are invalid")
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &SharedStore{cancel: cancel, prefix: "starport:cache:v1:" + namespace + ":"}
	options.DisableCache = true
	options.DisableRetry = true
	options.ForceSingleClient = true
	options.PipelineMultiplex = -1
	options.BlockingPoolSize = 1
	options.RingScaleEachConn = 6
	options.ReadBufferEachConn = 32 << 10
	options.WriteBufferEachConn = 32 << 10
	options.Dialer.Timeout = 500 * time.Millisecond
	options.ConnWriteTimeout = 500 * time.Millisecond
	options.ClientName = "starport-cache"
	options.DialCtxFn = func(callCtx context.Context, address string, dialer *net.Dialer, tlsConfig *tls.Config) (net.Conn, error) {
		dialCtx, stop := context.WithCancel(callCtx)
		defer stop()
		undo := context.AfterFunc(ctx, stop)
		defer undo()
		if tlsConfig != nil {
			return (&tls.Dialer{NetDialer: dialer, Config: tlsConfig}).DialContext(dialCtx, "tcp", address)
		}
		return dialer.DialContext(dialCtx, "tcp", address)
	}
	s.workers.Go(func() { s.connect(ctx, options) })
	return s, nil
}

func (s *SharedStore) connect(ctx context.Context, options valkey.ClientOption) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		client := s.current()
		if client == nil {
			candidate, err := valkey.NewClient(options)
			if err == nil {
				if ctx.Err() != nil {
					candidate.Close()
					return
				}
				s.mu.Lock()
				s.client = candidate
				s.mu.Unlock()
				client = candidate
			}
		}
		if client != nil {
			pingCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
			err := client.Do(pingCtx, client.B().Ping().Build()).Error()
			cancel()
			s.available.Store(err == nil)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *SharedStore) current() valkey.Client {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.client
}

var sharedRead = valkey.NewLuaScript(`
local size = redis.call('STRLEN', KEYS[1])
if size > tonumber(ARGV[1]) then return false end
return redis.call('GET', KEYS[1])
`)

// Get bounds a response before the server transfers its payload.
func (s *SharedStore) Get(ctx context.Context, key string) ([]byte, bool, error) {
	client := s.current()
	if client == nil {
		return nil, false, errSharedCacheUnavailable
	}
	data, err := sharedRead.Exec(ctx, client, []string{s.prefix + key}, []string{"1048576"}).AsBytes()
	if valkey.IsValkeyNil(err) {
		s.available.Store(true)
		return nil, false, nil
	}
	if err != nil {
		s.available.Store(false)
		return nil, false, errSharedCacheUnavailable
	}
	s.available.Store(true)
	return bytes.Clone(data), true, nil
}

// Set writes only finite, bounded cache entries.
func (s *SharedStore) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	if len(value) > 1<<20 || ttl < time.Millisecond {
		return nil
	}
	client := s.current()
	if client == nil {
		return errSharedCacheUnavailable
	}
	err := client.Do(ctx, client.B().Set().Key(s.prefix+key).Value(string(value)).Px(ttl).Build()).Error()
	s.available.Store(err == nil)
	if err != nil {
		return errSharedCacheUnavailable
	}
	return nil
}

// Stats excludes service identifiers and credentials.
func (s *SharedStore) Stats() Stats { return Stats{} }

// SharedStatus reads local connection evidence.
func (s *SharedStore) SharedStatus() SharedStatus {
	return SharedStatus{Configured: true, Available: s.available.Load() && !s.closed.Load()}
}

// Close cancels connection attempts and joins the connection owner.
func (s *SharedStore) Close() error {
	s.once.Do(func() {
		s.closed.Store(true)
		s.cancel()
		s.workers.Wait()
		s.mu.Lock()
		client := s.client
		s.client = nil
		s.mu.Unlock()
		if client != nil {
			client.Close()
		}
		s.available.Store(false)
	})
	return nil
}
