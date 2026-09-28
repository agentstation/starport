package assetfetch

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/jobs"
	"github.com/stretchr/testify/require"
)

func TestOriginGrants(t *testing.T) {
	credentialOrigin := (&url.URL{Scheme: "https", Host: "media.example", User: url.UserPassword("u", "secret")}).String()
	for _, origin := range []string{"https://media.example", "https://MEDIA.example:443", "http://127.0.0.1:1234", "http://[::1]:1234"} {
		client, err := New([]string{origin})
		require.NoError(t, err, origin)
		require.NoError(t, client.Close())
	}
	for _, origin := range []string{"", "http://media.example", "http://localhost", "https://media.example/", "https://media.example/video", credentialOrigin, "https://media.example?token=secret", "https://media.example#", "https://*.example", "https://media.example:65536", "https://media.example:0", "//media.example"} {
		_, err := New([]string{origin})
		require.ErrorIs(t, err, ErrInvalidOrigin, origin)
		require.NotContains(t, err.Error(), "secret")
	}
}

func TestFetchNeedsExplicitGrant(t *testing.T) {
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		require.Equal(t, http.MethodGet, r.Method)
		for _, header := range []string{"Authorization", "Proxy-Authorization", "Cookie", "Referer", "X-API-Key"} {
			require.Empty(t, r.Header.Get(header))
		}
		require.Equal(t, "private", r.URL.Query().Get("signature"))
		w.Header().Set("Content-Type", "video/mp4")
		_, _ = w.Write([]byte("video"))
	}))
	defer server.Close()
	unapproved, err := New(nil)
	require.NoError(t, err)
	defer unapproved.Close()
	_, err = unapproved.Fetch(t.Context(), server.URL+"/video?signature=private", 5)
	require.ErrorIs(t, err, jobs.ErrAssetDownloadBlocked)
	require.Zero(t, calls.Load())
	approved, err := New([]string{server.URL})
	require.NoError(t, err)
	defer approved.Close()
	asset, err := approved.Fetch(t.Context(), server.URL+"/video?signature=private", 5)
	require.NoError(t, err)
	require.Equal(t, []byte("video"), asset.Bytes)
	require.Equal(t, int64(1), calls.Load())
	for _, reference := range []string{strings.Replace(server.URL, "127.0.0.1", "localhost", 1), server.URL + "#fragment", "https://other.example/private?signature=secret"} {
		_, err := approved.Fetch(t.Context(), reference, 5)
		require.ErrorIs(t, err, jobs.ErrAssetDownloadBlocked)
		require.NotContains(t, err.Error(), "signature")
	}
	require.Equal(t, int64(1), calls.Load())
}

func TestFetchResponseBounds(t *testing.T) {
	for _, test := range []struct {
		name                          string
		status                        int
		media, encoding, length, body string
		chunked                       bool
		want                          error
	}{
		{name: "bounded", status: 200, media: "video/mp4", body: "video"},
		{name: "oversize header", status: 200, media: "video/mp4", body: "video!", want: jobs.ErrAssetTooLarge},
		{name: "oversize chunked", status: 200, media: "video/mp4", body: "video!", chunked: true, want: jobs.ErrAssetTooLarge},
		{name: "truncated", status: 200, media: "video/mp4", length: "5", body: "vid", want: jobs.ErrAssetDownloadUnavailable},
		{name: "empty", status: 200, media: "video/mp4", want: jobs.ErrAssetDownloadInvalid},
		{name: "wrong media", status: 200, media: "text/html", body: "video", want: jobs.ErrAssetDownloadInvalid},
		{name: "compressed", status: 200, media: "video/mp4", encoding: "gzip", body: "video", want: jobs.ErrAssetDownloadInvalid},
		{name: "partial", status: 206, media: "video/mp4", body: "video", want: jobs.ErrAssetDownloadUnavailable},
		{name: "unavailable", status: 503, media: "video/mp4", body: "secret", want: jobs.ErrAssetDownloadUnavailable},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", test.media)
				if test.encoding != "" {
					w.Header().Set("Content-Encoding", test.encoding)
				}
				if test.length != "" {
					w.Header().Set("Content-Length", test.length)
				}
				w.WriteHeader(test.status)
				if test.chunked {
					w.(http.Flusher).Flush()
				}
				_, _ = fmt.Fprint(w, test.body)
			}))
			defer server.Close()
			client, err := New([]string{server.URL})
			require.NoError(t, err)
			defer client.Close()
			asset, err := client.Fetch(t.Context(), server.URL+"/private?signature=secret", 5)
			if test.want != nil {
				require.ErrorIs(t, err, test.want)
				require.Empty(t, asset.Bytes)
				require.NotContains(t, err.Error(), "secret")
			} else {
				require.NoError(t, err)
				require.Equal(t, []byte(test.body), asset.Bytes)
			}
		})
	}
}

func TestFetchNeverFollowsRedirect(t *testing.T) {
	var targetCalls atomic.Int64
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { targetCalls.Add(1) }))
	defer target.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/video", http.StatusFound)
	}))
	defer server.Close()
	client, err := New([]string{server.URL, target.URL})
	require.NoError(t, err)
	defer client.Close()
	_, err = client.Fetch(t.Context(), server.URL+"/video?signature=secret", 5)
	require.ErrorIs(t, err, jobs.ErrAssetDownloadUnavailable)
	require.Zero(t, targetCalls.Load())
}

func TestFetchCancellation(t *testing.T) {
	started := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
	}))
	defer server.Close()
	client, err := New([]string{server.URL})
	require.NoError(t, err)
	defer client.Close()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { _, err := client.Fetch(ctx, server.URL, 5); done <- err }()
	<-started
	cancel()
	select {
	case err := <-done:
		require.True(t, errors.Is(err, jobs.ErrAssetDownloadUnavailable))
	case <-time.After(time.Second):
		t.Fatal("download ignored cancellation")
	}
}

func TestFetchHTTPSRequiresValidCertificate(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Empty(t, r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "video/mp4")
		_, _ = w.Write([]byte("video"))
	}))
	defer server.Close()
	client, err := New([]string{server.URL})
	require.NoError(t, err)
	defer client.Close()
	_, err = client.Fetch(t.Context(), server.URL+"/video?signature=private", 5)
	require.ErrorIs(t, err, jobs.ErrAssetDownloadUnavailable)
	require.NotContains(t, err.Error(), "private")
	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	client.client.Transport.(*http.Transport).TLSClientConfig = &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}
	asset, err := client.Fetch(t.Context(), server.URL+"/video?signature=private", 5)
	require.NoError(t, err)
	require.Equal(t, []byte("video"), asset.Bytes)
	require.Nil(t, client.client.Transport.(*http.Transport).Proxy, "ambient proxies cannot gain asset URLs")
}
