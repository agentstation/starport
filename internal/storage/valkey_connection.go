package storage

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"sync"
	"time"

	"github.com/agentstation/starport/internal/kvconnection"
	"github.com/valkey-io/valkey-go"
)

// ValidateConnection checks the selected transport without reading files or dialing.
func (c ValkeyConfig) ValidateConnection() error {
	u, err := kvconnection.Parse(c.URL, c.AllowInsecure)
	if err != nil {
		return err
	}
	if u.User != nil {
		if c.Username != "" && u.User.Username() != "" && c.Username != u.User.Username() {
			return errors.New("durable KV username conflicts with the URL")
		}
		password, present := u.User.Password()
		if present && c.Password != "" && c.Password != password {
			return errors.New("durable KV password conflicts with the URL")
		}
	}
	if c.ClusterMode {
		return errors.New("durable KV requires a single writable primary; cluster mode is not qualified")
	}
	if c.CAFile != "" && u.Scheme != "valkeys" && u.Scheme != "rediss" {
		return errors.New("durable KV CA file requires a TLS endpoint")
	}
	if c.DB < 0 || c.DB > 65535 {
		return errors.New("durable KV database is invalid")
	}
	if c.DB != 0 && u.Path != "" {
		return errors.New("durable KV database must be selected in the URL or DB field, not both")
	}
	if c.DialTimeout < 0 || c.ReadTimeout < 0 || c.WriteTimeout < 0 || c.IdleTimeout < 0 {
		return errors.New("durable KV timeouts cannot be negative")
	}
	if c.ReadTimeout != c.WriteTimeout {
		return errors.New("durable KV requires equal read and write timeouts")
	}
	maximum := c.MaxConnections
	if maximum == 0 {
		maximum = 50
	}
	if maximum < 2 || c.MinIdleConns < 0 || c.MinIdleConns >= maximum {
		return errors.New("durable KV requires at least two connections and fewer idle connections than the maximum")
	}
	return nil
}

func valkeyConnectionOptions(c ValkeyConfig) (valkey.ClientOption, error) {
	if err := c.ValidateConnection(); err != nil {
		return valkey.ClientOption{}, err
	}
	u, _ := kvconnection.Parse(c.URL, c.AllowInsecure)
	options, err := valkey.ParseURL(u.String())
	if err != nil {
		return valkey.ClientOption{}, errors.New("durable KV connection settings are invalid")
	}
	// Validation rejects conflicting values before explicit fields complete URI credentials.
	if c.Username != "" {
		options.Username = c.Username
	}
	if c.Password != "" {
		options.Password = c.Password
	}
	if u.Path == "" {
		options.SelectDB = c.DB
	}
	if c.CAFile != "" {
		roots, err := kvconnection.LoadRoots(c.CAFile)
		if err != nil {
			return valkey.ClientOption{}, err
		}
		options.TLSConfig.RootCAs = roots
	}
	options.ForceSingleClient = true
	options.DisableCache = true
	options.DisableRetry = true
	options.ClientName = "starport"
	options.Dialer.Timeout = c.DialTimeout
	if options.Dialer.Timeout == 0 {
		options.Dialer.Timeout = 5 * time.Second
	}
	options.PipelineMultiplex = -1
	maximum := c.MaxConnections
	if maximum == 0 {
		maximum = 50
	}
	options.BlockingPoolSize = maximum - 1
	options.BlockingPoolMinSize = c.MinIdleConns
	options.BlockingPoolCleanup = c.IdleTimeout
	if options.BlockingPoolCleanup == 0 {
		options.BlockingPoolCleanup = 5 * time.Minute
	}
	options.ConnWriteTimeout = c.ReadTimeout
	if options.ConnWriteTimeout == 0 {
		options.ConnWriteTimeout = 3 * time.Second
	}
	options.DialCtxFn = boundedValkeyDial(maximum)
	return options, nil
}

// Connection errors expose a recovery category without endpoint or server text.
func valkeyConnectionError(err error) error {
	var certificate *tls.CertificateVerificationError
	if errors.As(err, &certificate) {
		return errors.New("durable KV TLS verification failed; check the CA bundle and endpoint identity")
	}
	var network net.Error
	if errors.As(err, &network) && network.Timeout() {
		return errors.New("durable KV connection timed out")
	}
	return errors.New("durable KV connection failed; check connectivity, TLS, and credentials")
}

// boundedValkeyDial counts live sockets across the client's pipeline and pools.
// Acquiring a connection shares the dial deadline, including TLS negotiation.
func boundedValkeyDial(maximum int) func(context.Context, string, *net.Dialer, *tls.Config) (net.Conn, error) {
	slots := make(chan struct{}, maximum)
	return func(ctx context.Context, address string, dialer *net.Dialer, trust *tls.Config) (net.Conn, error) {
		ctx, cancel := context.WithTimeout(ctx, dialer.Timeout)
		defer cancel()
		select {
		case slots <- struct{}{}:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		var conn net.Conn
		var err error
		if trust == nil {
			conn, err = dialer.DialContext(ctx, "tcp", address)
		} else {
			conn, err = (&tls.Dialer{NetDialer: dialer, Config: trust}).DialContext(ctx, "tcp", address)
		}
		if err != nil {
			<-slots
			return nil, err
		}
		return &boundedValkeyConn{Conn: conn, release: func() { <-slots }}, nil
	}
}

type boundedValkeyConn struct {
	net.Conn
	once    sync.Once
	release func()
}

func (c *boundedValkeyConn) Close() error {
	err := c.Conn.Close()
	c.once.Do(c.release)
	return err
}
