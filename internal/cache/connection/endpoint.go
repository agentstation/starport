// Package connection validates the optional shared-cache endpoint.
package connection

import (
	"errors"
	"net"
	"net/url"
	"strconv"
	"strings"
)

// Parse accepts one endpoint with verified TLS or explicit plaintext permission.
// Errors contain no URI or credential values.
func Parse(raw string, allowInsecure bool) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || u.Opaque != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawFragment != "" || u.RawPath != "" {
		return nil, errors.New("cache URL must name one host without query options or fragments")
	}
	switch u.Scheme {
	case "valkeys", "rediss":
	case "valkey", "redis":
		if !allowInsecure && !loopback(u.Hostname()) {
			return nil, errors.New("remote cache requires TLS or explicit allow_insecure")
		}
	default:
		return nil, errors.New("cache URL requires valkey, valkeys, redis, or rediss scheme")
	}
	if port := u.Port(); port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return nil, errors.New("cache URL port is invalid")
		}
	}
	if u.Path != "" {
		n, err := strconv.Atoi(strings.TrimPrefix(u.Path, "/"))
		if err != nil || n < 0 || n > 65535 {
			return nil, errors.New("cache URL database is invalid")
		}
	}
	host := u.Hostname()
	if strings.Contains(host, ":") && net.ParseIP(host) == nil {
		return nil, errors.New("cache URL host is invalid")
	}
	port := u.Port()
	if port == "" {
		port = "6379"
	}
	u.Host = net.JoinHostPort(host, port)
	return u, nil
}

func loopback(host string) bool {
	return strings.EqualFold(strings.TrimSuffix(host, "."), "localhost") || net.ParseIP(host).IsLoopback()
}

// SameServer rejects a shared cache on the declared durable KV server.
// Different credentials, databases, namespaces, and TLS schemes do not isolate eviction.
// DNS aliases and external proxies still require deployment-level isolation checks.
func SameServer(a, b string) bool {
	left, le := url.Parse(a)
	right, re := url.Parse(b)
	return le == nil && re == nil && endpoint(left) == endpoint(right)
}

func endpoint(u *url.URL) string {
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	if loopback(host) {
		host = "loopback"
	}
	port := u.Port()
	if port == "" {
		port = "6379"
	}
	return net.JoinHostPort(host, port)
}
