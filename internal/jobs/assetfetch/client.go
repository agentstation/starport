// Package assetfetch downloads video assets from operator-approved origins.
package assetfetch

import (
	"context"
	"errors"
	"io"
	"math"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/agentstation/starport/internal/jobs"
)

// ErrInvalidOrigin reports an invalid configured download origin without its value.
var ErrInvalidOrigin = errors.New("assetfetch: configure an exact HTTPS origin or a literal loopback HTTP origin")

// Client holds immutable origin grants and a separate credential-free transport.
type Client struct {
	origins map[string]bool
	client  *http.Client
}

// New validates explicit origin grants. An empty list denies every download.
func New(origins []string) (*Client, error) {
	allowed := make(map[string]bool, len(origins))
	for _, raw := range origins {
		u, err := url.Parse(raw)
		if err != nil || u.Path != "" || u.RawQuery != "" || u.ForceQuery || raw != u.Scheme+"://"+u.Host {
			return nil, ErrInvalidOrigin
		}
		origin, ok := canonicalOrigin(u)
		if !ok {
			return nil, ErrInvalidOrigin
		}
		allowed[origin] = true
	}
	transport := &http.Transport{
		DialContext:           (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 10 * time.Second,
		IdleConnTimeout:       30 * time.Second,
		MaxIdleConns:          8,
		DisableCompression:    true,
	}
	return &Client{origins: allowed, client: &http.Client{
		Transport: transport, Timeout: 30 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}, nil
}

// Close releases idle connections after the job workers stop.
func (c *Client) Close() error { c.client.CloseIdleConnections(); return nil }

// Fetch sends one bounded GET. It refuses redirects and ignores environment proxies.
// Errors contain no URL, query value, response body, or authentication value.
func (c *Client) Fetch(ctx context.Context, reference string, maxBytes int64) (jobs.Asset, error) {
	u, err := url.Parse(reference)
	if err != nil {
		return jobs.Asset{}, jobs.ErrAssetDownloadBlocked
	}
	origin, ok := canonicalOrigin(u)
	if !ok || !c.origins[origin] {
		return jobs.Asset{}, jobs.ErrAssetDownloadBlocked
	}
	if maxBytes <= 0 || maxBytes == math.MaxInt64 {
		return jobs.Asset{}, jobs.ErrAssetTooLarge
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return jobs.Asset{}, jobs.ErrAssetDownloadBlocked
	}
	req.Header.Set("Accept", "video/*")
	response, err := c.client.Do(req)
	if err != nil {
		return jobs.Asset{}, jobs.ErrAssetDownloadUnavailable
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return jobs.Asset{}, jobs.ErrAssetDownloadUnavailable
	}
	if response.ContentLength > maxBytes {
		return jobs.Asset{}, jobs.ErrAssetTooLarge
	}
	mediaType, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil || !strings.HasPrefix(mediaType, "video/") || response.Header.Get("Content-Encoding") != "" {
		return jobs.Asset{}, jobs.ErrAssetDownloadInvalid
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxBytes+1))
	if err != nil {
		return jobs.Asset{}, jobs.ErrAssetDownloadUnavailable
	}
	if int64(len(data)) > maxBytes {
		return jobs.Asset{}, jobs.ErrAssetTooLarge
	}
	if len(data) == 0 {
		return jobs.Asset{}, jobs.ErrAssetDownloadInvalid
	}
	return jobs.Asset{ContentType: mediaType, Bytes: data}, nil
}

func canonicalOrigin(u *url.URL) (string, bool) {
	if u == nil || u.User != nil || u.Fragment != "" || u.Opaque != "" || u.Host == "" {
		return "", false
	}
	host := strings.ToLower(u.Hostname())
	if strings.ContainsAny(host, "%\\* ") || host == "" {
		return "", false
	}
	port := u.Port()
	switch u.Scheme {
	case "https":
		if port == "" {
			port = "443"
		}
	case "http":
		address, err := netip.ParseAddr(host)
		if err != nil || !address.IsLoopback() {
			return "", false
		}
		if port == "" {
			port = "80"
		}
	default:
		return "", false
	}
	number, err := strconv.ParseUint(port, 10, 16)
	if err != nil || number == 0 {
		return "", false
	}
	return u.Scheme + "://" + net.JoinHostPort(host, strconv.FormatUint(number, 10)), true
}
