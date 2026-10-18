package server

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestStartServesTLSWithoutPlaintextFallback(t *testing.T) {
	certificateSource := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	certificate := certificateSource.TLS.Certificates[0]
	certificateSource.Close()
	roots := x509.NewCertPool()
	parsed, err := x509.ParseCertificate(certificate.Certificate[0])
	require.NoError(t, err)
	roots.AddCert(parsed)

	reservation, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	address := reservation.Addr().String()
	_, port, err := net.SplitHostPort(address)
	require.NoError(t, err)
	portNumber, err := strconv.Atoi(port)
	require.NoError(t, err)
	require.NoError(t, reservation.Close())

	gateway := newTestServer(t, &Config{Host: "127.0.0.1", Port: portNumber, ShutdownTimeout: time.Second, TLSCertificate: &certificate})
	completed := make(chan error, 1)
	go func() { completed <- gateway.Serve() }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		require.NoError(t, gateway.Shutdown(ctx))
		select {
		case err := <-completed:
			require.NoError(t, err)
		case <-time.After(3 * time.Second):
			t.Error("server did not stop")
		}
	})
	transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}}
	t.Cleanup(transport.CloseIdleConnections)
	// A fixed 3 s wait flaked on windows-2025 under the race detector (#473),
	// so readiness waits until half of the test deadline.
	deadline := time.Now().Add(30 * time.Second)
	if testDeadline, ok := t.Deadline(); ok {
		deadline = time.Now().Add(time.Until(testDeadline) / 2)
	}
	client := &http.Client{Transport: transport, Timeout: time.Until(deadline)}
	for {
		response, requestErr := client.Get("https://" + address + "/health/live")
		if requestErr == nil {
			require.Equal(t, http.StatusOK, response.StatusCode)
			_, err = io.Copy(io.Discard, response.Body)
			require.NoError(t, err)
			require.NoError(t, response.Body.Close())
			break
		}
		if strings.Contains(requestErr.Error(), "HTTP response to HTTPS") || time.Now().After(deadline) {
			t.Fatalf("HTTPS listener unavailable: %v", requestErr)
		}
		time.Sleep(10 * time.Millisecond)
	}
	plain := &http.Client{Timeout: time.Until(deadline)}
	response, err := plain.Get("http://" + address + "/health/live")
	require.NoError(t, err)
	require.Equal(t, http.StatusBadRequest, response.StatusCode)
	require.NoError(t, response.Body.Close())
}

func TestNewRejectsIncompleteTLSCertificate(t *testing.T) {
	_, err := New(&Config{TLSCertificate: &tls.Certificate{}}, Dependencies{})
	require.ErrorContains(t, err, "server TLS requires")
}

func TestServerAddressUsesIPv6Brackets(t *testing.T) {
	gateway := newTestServer(t, &Config{Host: "::1", Port: 7827})
	require.Equal(t, "[::1]:7827", gateway.httpServer.Addr)
}
