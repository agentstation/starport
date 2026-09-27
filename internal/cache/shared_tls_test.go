package cache

import (
	"github.com/agentstation/starport/internal/kvconnection/kvtest"
	"github.com/stretchr/testify/require"
	"net"
	"net/url"
	"os"
	"testing"
	"time"
)

func TestSharedCacheTLSWithRealService(t *testing.T) {
	raw := os.Getenv("TEST_SHARED_CACHE_URL")
	if raw == "" {
		t.Skip("UNVERIFIED: TEST_SHARED_CACHE_URL is not set")
	}
	backend, err := url.Parse(raw)
	require.NoError(t, err)
	for _, tc := range []struct {
		name                        string
		trusted, expired, wrongHost bool
	}{
		{name: "trusted", trusted: true}, {name: "untrusted"}, {name: "expired", trusted: true, expired: true}, {name: "hostname", trusted: true, wrongHost: true},
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
			store, err := OpenShared(SharedConfig{URL: endpoint, DeploymentID: "tls-" + tc.name, CAFile: caFile})
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, store.Close()) })
			valid := tc.trusted && !tc.expired && !tc.wrongHost
			select {
			case accepted := <-handshakes:
				require.Equal(t, valid, accepted)
			case <-time.After(3 * time.Second):
				t.Fatal("no TLS verification result")
			}
			if !valid {
				expectedState := map[string]string{"untrusted": "tls_untrusted", "expired": "tls_certificate_invalid", "hostname": "tls_hostname_mismatch"}[tc.name]
				require.Eventually(t, func() bool { return store.SharedStatus().State == expectedState }, 3*time.Second, time.Millisecond, "state: %s", store.SharedStatus().State)
				require.False(t, store.SharedStatus().Available)
				require.Error(t, store.Set(t.Context(), "blocked", []byte("value"), time.Second))
				require.Zero(t, forwarded.Load(), "invalid TLS must not reach the cache protocol")
				return
			}
			require.Eventually(t, func() bool { return store.SharedStatus().Available }, 3*time.Second, time.Millisecond)
			require.NoError(t, store.Set(t.Context(), "answer", []byte("verified"), time.Second))
			value, found, err := store.Get(t.Context(), "answer")
			require.NoError(t, err)
			require.True(t, found)
			require.Equal(t, "verified", string(value))
			require.Positive(t, forwarded.Load())
		})
	}
}
