package storage

import (
	"context"
	"net"
	"net/url"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/kvconnection/kvtest"
	"github.com/stretchr/testify/require"
)

func TestValkeyConnectionRefusesUnsafeSettings(t *testing.T) {
	for _, c := range []ValkeyConfig{
		{URL: "valkey://operator:private-value@remote.example"},
		{URL: "valkeys://operator:private-value@remote.example?skip_verify=true"},
		{URL: "valkeys://operator:private-value@remote.example#fragment"},
		{URL: "valkeys://operator:private-value@remote.example/-1"},
		{URL: "valkey://localhost", CAFile: "private-value"},
		{URL: "valkey://localhost", ClusterMode: true},
		{URL: "valkey://localhost/1", DB: 2},
		{URL: "valkey://localhost", DialTimeout: -time.Second},
	} {
		err := c.ValidateConnection()
		require.Error(t, err)
		require.NotContains(t, err.Error(), "private-value")
	}
	require.NoError(t, (ValkeyConfig{URL: "valkey://remote.example", AllowInsecure: true}).ValidateConnection())
}

func TestValkeyCredentialConflicts(t *testing.T) {
	for _, tc := range []struct {
		name     string
		username string
		password string
		valid    bool
	}{
		{name: "URI only", valid: true},
		{name: "matching fields", username: "uri-user", password: "uri-private-value", valid: true},
		{name: "different username", username: "field-user"},
		{name: "different password", password: "field-private-value"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := ValkeyConfig{URL: "valkeys://uri-user:uri-private-value@durable.example/2", Username: tc.username, Password: tc.password}
			err := cfg.ValidateConnection()
			if tc.valid {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			require.NotContains(t, err.Error(), "private-value")
			require.NotContains(t, err.Error(), "uri-user")
			require.NotContains(t, err.Error(), "field-user")
		})
	}
}

func TestValkeyTLSIdentity(t *testing.T) {
	raw := os.Getenv("TEST_VALKEY_URL")
	if raw == "" {
		t.Skip("UNVERIFIED: TEST_VALKEY_URL is required")
	}
	backend, err := url.Parse(raw)
	require.NoError(t, err)
	for _, tc := range []struct {
		name                        string
		trusted, expired, wrongHost bool
	}{
		{name: "trusted", trusted: true}, {name: "untrusted"},
		{name: "expired", trusted: true, expired: true},
		{name: "hostname", trusted: true, wrongHost: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			certificate, caFile := kvtest.Certificate(t, tc.expired)
			endpoint, handshakes, forwarded := kvtest.Relay(t, backend.Host, certificate)
			if tc.wrongHost {
				u, err := url.Parse(endpoint)
				require.NoError(t, err)
				u.Host = net.JoinHostPort("localhost", u.Port())
				endpoint = u.String()
			}
			if !tc.trusted {
				caFile = ""
			}
			store, err := openUnscopedValkeyForTest(ValkeyConfig{URL: endpoint, CAFile: caFile})
			valid := tc.trusted && !tc.expired && !tc.wrongHost
			if !valid {
				require.Error(t, err)
				require.NotContains(t, err.Error(), endpoint)
				if caFile != "" {
					require.NotContains(t, err.Error(), caFile)
				}
				require.Zero(t, forwarded.Load(), "invalid TLS reached the storage protocol")
			} else {
				require.NoError(t, err)
				t.Cleanup(func() { require.NoError(t, store.Close()) })
				require.NoError(t, store.Set(t.Context(), "tls-identity", []byte("verified")))
				value, err := store.Get(t.Context(), "tls-identity")
				require.NoError(t, err)
				require.Equal(t, "verified", string(value))
				defer func() { require.NoError(t, store.Delete(t.Context(), "tls-identity")) }()
			}
			select {
			case accepted := <-handshakes:
				require.Equal(t, valid, accepted)
			case <-time.After(3 * time.Second):
				t.Fatal("no TLS handshake result")
			}
		})
	}
}

func TestValkeyUsernamePassword(t *testing.T) {
	raw := os.Getenv("TEST_VALKEY_URL")
	if raw == "" {
		t.Skip("UNVERIFIED: TEST_VALKEY_URL is required")
	}
	control, err := openUnscopedValkeyForTest(ValkeyConfig{URL: raw})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, control.Close()) })
	client := control.(*ValkeyStore).client
	username, password := "endpoint-test", "test-p@ss:word"
	require.NoError(t, client.Do(t.Context(), client.B().Arbitrary("ACL", "SETUSER", username, "reset", "on", ">"+password, "~*", "&*", "+@all").Build()).Error())
	defer func() {
		require.NoError(t, client.Do(t.Context(), client.B().Arbitrary("ACL", "DELUSER", username).Build()).Error())
	}()
	for _, fields := range []bool{false, true} {
		cfg := ValkeyConfig{URL: raw}
		if fields {
			cfg.Username, cfg.Password = username, password
		} else {
			u, err := url.Parse(raw)
			require.NoError(t, err)
			u.User = url.UserPassword(username, password)
			cfg.URL = u.String()
		}
		store, err := openUnscopedValkeyForTest(cfg)
		require.NoError(t, err)
		require.NoError(t, store.Ping(t.Context()))
		require.NoError(t, store.Close())
		cfg.Password = "incorrect-private-value"
		_, err = openUnscopedValkeyForTest(cfg)
		require.Error(t, err)
		require.NotContains(t, err.Error(), "private-value")
		require.NotContains(t, err.Error(), password)
	}
}

func TestValkeyEffectiveConnectionOptions(t *testing.T) {
	raw := os.Getenv("TEST_VALKEY_URL")
	if raw == "" {
		t.Skip("UNVERIFIED: TEST_VALKEY_URL is required")
	}
	cfg := ValkeyConfig{URL: raw, MaxConnections: 3, MinIdleConns: 1,
		DialTimeout: 100 * time.Millisecond, ReadTimeout: 100 * time.Millisecond,
		WriteTimeout: 100 * time.Millisecond, IdleTimeout: 20 * time.Millisecond}
	store, err := openUnscopedValkeyForTest(cfg)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	client := store.(*ValkeyStore).client
	control, err := openUnscopedValkeyForTest(ValkeyConfig{URL: raw})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, control.Close()) })
	admin := control.(*ValkeyStore).client

	// A server-side pause proves the operation timeout reaches the network client.
	require.NoError(t, admin.Do(t.Context(), admin.B().Arbitrary("CLIENT", "PAUSE", "1000", "ALL").Build()).Error())
	started := time.Now()
	_, err = store.Get(t.Context(), "timeout-probe")
	require.Error(t, err)
	require.Less(t, time.Since(started), 750*time.Millisecond)
	// The control client waits for the pause to end before the connection-pool probe.
	require.NoError(t, control.Ping(t.Context()))
	require.NoError(t, store.Ping(t.Context()))

	first, releaseFirstRaw := client.Dedicate()
	releaseFirst := sync.OnceFunc(releaseFirstRaw)
	defer releaseFirst()
	require.NoError(t, first.Do(t.Context(), first.B().Ping().Build()).Error())
	second, releaseSecond := client.Dedicate()
	defer releaseSecond()
	require.NoError(t, second.Do(t.Context(), second.B().Ping().Build()).Error())
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	completed := make(chan error, 1)
	go func() {
		third, releaseThird := client.Dedicate()
		defer releaseThird()
		completed <- third.Do(ctx, third.B().Ping().Build()).Error()
	}()
	select {
	case err := <-completed:
		t.Fatalf("third dedicated connection escaped the pool bound: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	releaseFirst()
	select {
	case err := <-completed:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("released connection did not unblock the pool")
	}
}
