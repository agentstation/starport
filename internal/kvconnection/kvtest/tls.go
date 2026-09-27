// Package kvtest supplies TLS transport fixtures for real KV service tests.
package kvtest

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
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Certificate supplies a server certificate and its private test CA file.
func Certificate(t *testing.T, expired bool) (tls.Certificate, string) {
	t.Helper()
	rootKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	now := time.Now()
	root := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "KV test root"}, NotBefore: now.Add(-2 * time.Hour), NotAfter: now.Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature}
	rootDER, err := x509.CreateCertificate(rand.Reader, root, root, &rootKey.PublicKey, rootKey)
	require.NoError(t, err)
	path := filepath.Join(t.TempDir(), "kv-ca.pem")
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

// Relay forwards verified TLS connections to a real KV service until test cleanup.
func Relay(t *testing.T, backend string, certificate tls.Certificate) (string, <-chan bool, *atomic.Int64) {
	t.Helper()
	listener, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
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
				defer func() { _ = socket.Close() }()
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
				defer func() { _ = target.Close() }()
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
