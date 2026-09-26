package cache

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/agentstation/starport/internal/cache/connection"
	"github.com/agentstation/starport/internal/deployment"
	"github.com/valkey-io/valkey-go"
)

var errSharedCacheUnavailable = errors.New("shared cache unavailable")

// SharedStatus reports connection health without endpoint or credential values.
type SharedStatus struct {
	Configured bool   `json:"configured"`
	Available  bool   `json:"available"`
	State      string `json:"state"`
	KeyPrefix  string `json:"key_prefix"`
}

// SharedStore owns an optional connection independently of authoritative storage.
type SharedStore struct {
	mu      sync.RWMutex
	client  valkey.Client
	cancel  context.CancelFunc
	workers sync.WaitGroup
	once    sync.Once
	prefix  string
	state   atomic.Uint32
	closed  atomic.Bool
}

// SharedConfig selects one cache endpoint and its trust roots.
type SharedConfig struct {
	URL           string
	DeploymentID  string
	AllowInsecure bool
	CAFile        string
}

// OpenShared starts bounded connection work without delaying application startup.
func OpenShared(config SharedConfig) (*SharedStore, error) {
	prefix, err := deployment.KeyPrefix(config.DeploymentID)
	if err != nil {
		return nil, err
	}
	u, err := connection.Parse(config.URL, config.AllowInsecure)
	if err != nil {
		return nil, err
	}
	options, err := valkey.ParseURL(u.String())
	if err != nil {
		return nil, errors.New("cache connection settings are invalid")
	}
	if config.CAFile != "" {
		if options.TLSConfig == nil {
			return nil, errors.New("cache CA file requires a TLS endpoint")
		}
		roots, err := connection.LoadRoots(config.CAFile)
		if err != nil {
			return nil, err
		}
		options.TLSConfig.RootCAs = roots
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &SharedStore{cancel: cancel, prefix: prefix + "cache:"}
	s.state.Store(sharedConnecting)
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
			if err != nil {
				s.recordFailure(err)
			}
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
			err := s.checkNamespace(pingCtx, client)
			cancel()
			if err == nil {
				s.state.Store(sharedReady)
			} else {
				s.recordFailure(err)
			}
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
		return nil, false, nil
	}
	if err != nil {
		s.recordFailure(err)
		return nil, false, errSharedCacheUnavailable
	}
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
	if err != nil {
		s.recordFailure(err)
		return errSharedCacheUnavailable
	}
	return nil
}

// Stats excludes service identifiers and credentials.
func (s *SharedStore) Stats() Stats { return Stats{} }

// SharedStatus reads local connection evidence.
func (s *SharedStore) SharedStatus() SharedStatus {
	state := s.state.Load()
	if s.closed.Load() {
		return SharedStatus{Configured: true, KeyPrefix: s.prefix, State: "closed"}
	}
	return SharedStatus{Configured: true, KeyPrefix: s.prefix, Available: state == sharedReady, State: sharedStateNames[state]}
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
		s.state.Store(sharedUnavailable)
	})
	return nil
}

// checkNamespace verifies the cache credential's read and write scope.
// The reserved probe expires and never stores caller content.
func (s *SharedStore) checkNamespace(ctx context.Context, client valkey.Client) error {
	key := s.prefix + "__starport_health_v1__"
	if err := client.Do(ctx, client.B().Set().Key(key).Value("1").Px(time.Second).Build()).Error(); err != nil {
		return err
	}
	value, err := sharedRead.Exec(ctx, client, []string{key}, []string{"1"}).ToString()
	if err != nil {
		return err
	}
	if value != "1" {
		return errSharedCacheUnavailable
	}
	return nil
}

const (
	sharedConnecting uint32 = iota
	sharedReady
	sharedUnavailable
	sharedUntrusted
	sharedHostname
	sharedCertificate
	sharedAuthentication
	sharedNamespace
)

var sharedStateNames = [...]string{"connecting", "ready", "unavailable", "tls_untrusted", "tls_hostname_mismatch", "tls_certificate_invalid", "authentication_failed", "namespace_denied"}

func sharedFailureState(err error) uint32 {
	if _, ok := errors.AsType[x509.UnknownAuthorityError](err); ok {
		return sharedUntrusted
	}
	if _, ok := errors.AsType[x509.HostnameError](err); ok {
		return sharedHostname
	}
	if _, ok := errors.AsType[x509.CertificateInvalidError](err); ok {
		return sharedCertificate
	}
	if protocol, ok := valkey.IsValkeyErr(err); ok {
		switch {
		case strings.HasPrefix(protocol.Error(), "WRONGPASS"), strings.HasPrefix(protocol.Error(), "NOAUTH"):
			return sharedAuthentication
		case strings.HasPrefix(protocol.Error(), "NOPERM"):
			return sharedNamespace
		}
	}
	return sharedUnavailable
}

func (s *SharedStore) recordFailure(err error) {
	if errors.Is(err, context.Canceled) {
		return
	}
	s.state.Store(sharedFailureState(err))
}
