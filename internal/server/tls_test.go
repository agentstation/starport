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
	go func() { completed <- gateway.Start() }()
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
	client := &http.Client{Transport: transport, Timeout: time.Second}
	deadline := time.Now().Add(3 * time.Second)
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
	plain := &http.Client{Timeout: time.Second}
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
	gateway := newTestServer(t, &Config{Host: "::1", Port: 8080})
	require.Equal(t, "[::1]:8080", gateway.httpServer.Addr)
}
