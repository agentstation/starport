package cache

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func cacheTLSCertificate(t *testing.T, expired bool) (tls.Certificate, string) {
	t.Helper()
	rootKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	now := time.Now()
	root := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "cache test root"}, NotBefore: now.Add(-2 * time.Hour), NotAfter: now.Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature}
	rootDER, err := x509.CreateCertificate(rand.Reader, root, root, &rootKey.PublicKey, rootKey)
	require.NoError(t, err)
	path := filepath.Join(t.TempDir(), "cache-ca.pem")
	require.NoError(t, os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: rootDER}), 0o600))
	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	leaf := &x509.Certificate{SerialNumber: big.NewInt(2), NotBefore: now.Add(-2 * time.Hour), NotAfter: now.Add(time.Hour), IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	if expired {
		leaf.NotAfter = now.Add(-time.Hour)
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leaf, root, &leafKey.PublicKey, rootKey)
	require.NoError(t, err)
	return tls.Certificate{Certificate: [][]byte{leafDER, rootDER}, PrivateKey: leafKey}, path
}

func cacheTLSRelay(t *testing.T, backend string, certificate tls.Certificate) (string, <-chan bool, *atomic.Int64) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(t.Context())
	handshakes := make(chan bool, 16)
	forwarded := new(atomic.Int64)
	var workers sync.WaitGroup
	workers.Go(func() {
		for {
			socket, err := listener.Accept()
			if err != nil {
				return
			}
			workers.Go(func() {
				defer socket.Close()
				stop := context.AfterFunc(ctx, func() { _ = socket.Close() })
				defer stop()
				secured := tls.Server(socket, &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{certificate}})
				handshake, cancel := context.WithTimeout(ctx, time.Second)
				err := secured.HandshakeContext(handshake)
				cancel()
				select {
				case handshakes <- err == nil:
				default:
				}
				if err != nil {
					return
				}
				target, err := (&net.Dialer{Timeout: time.Second}).DialContext(ctx, "tcp", backend)
				if err != nil {
					return
				}
				defer target.Close()
				stopTarget := context.AfterFunc(ctx, func() { _ = target.Close() })
				defer stopTarget()
				forwarded.Add(1)
				var transfers sync.WaitGroup
				transfers.Go(func() { _, _ = io.Copy(target, secured); _ = target.Close() })
				_, _ = io.Copy(secured, target)
				_ = secured.Close()
				transfers.Wait()
			})
		}
	})
	t.Cleanup(func() { cancel(); _ = listener.Close(); workers.Wait() })
	return "valkeys://" + listener.Addr().String(), handshakes, forwarded
}

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
			certificate, caFile := cacheTLSCertificate(t, tc.expired)
			endpoint, handshakes, forwarded := cacheTLSRelay(t, backend.Host, certificate)
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
