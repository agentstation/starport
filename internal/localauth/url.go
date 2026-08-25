package localauth

import (
	"net"
	"net/url"
	"strconv"
	"strings"
)

// LaunchPath is the route that exchanges a launch ticket for a console
// session. It lives here rather than in the router because the CLI writes URLs
// that point at it and the router serves it, and a path spelled in two places
// is a path that can be spelled two ways.
const LaunchPath = "/launch"

// LaunchURL puts a ticket on a base URL.
//
// The ticket travels in the query string, which is the one place this package
// otherwise refuses to put a credential. It is the exception a browser forces:
// nothing else survives a person clicking a link. Everything about a ticket is
// built for that exposure — it expires in TicketTTL, it works once, and the
// route redirects so it leaves the address bar as it is spent.
func LaunchURL(base string, ticket string) (string, error) {
	parsed, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	parsed.Path = LaunchPath
	parsed.RawQuery = url.Values{TicketParam: []string{ticket}}.Encode()
	return parsed.String(), nil
}

// BrowsableBase is the URL a browser on this machine should open to reach a
// gateway bound to host:port.
//
// A gateway that binds every interface has no address of its own, and 0.0.0.0
// or :: in an address bar is a URL a person cannot reason about even where a
// browser resolves it. So an unspecified bind becomes loopback, which is the
// interface the browser running this command is on.
func BrowsableBase(host string, port int, secure bool) string {
	scheme := "http"
	if secure {
		scheme = "https"
	}
	return scheme + "://" + net.JoinHostPort(browsableHost(host), strconv.Itoa(port))
}

func browsableHost(host string) string {
	host = strings.TrimSpace(host)
	if host == "" {
		return "127.0.0.1"
	}
	bare := strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")
	ip := net.ParseIP(bare)
	if ip == nil {
		return host
	}
	if ip.IsUnspecified() {
		if ip.To4() != nil {
			return "127.0.0.1"
		}
		return "::1"
	}
	return bare
}
