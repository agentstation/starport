package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	starportcli "github.com/agentstation/starport/internal/cli"
)

const consoleLinkLine = "Console (one-time launch link):"

// TestDevOpensTheBoundAddress keeps the console link on this gateway. The
// link that dev prints is the link that it opens. When another process holds
// the configured port, dev prints and opens no link to that port. When the
// port is free, the link names the port that this gateway serves, and this
// gateway spends its ticket.
func TestDevOpensTheBoundAddress(t *testing.T) {
	t.Run("busy port", func(t *testing.T) {
		firstRunEnvironment(t)
		busy := holdLoopbackPort(t)
		t.Setenv("STARPORT_SERVER_PORT", strconv.Itoa(busy))

		ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
		defer cancel()
		stdout, stderr := &firstRunOutput{}, &firstRunOutput{}
		code := runContext(ctx, []string{"starport", "dev", "--no-open"}, bytes.NewReader(nil), stdout, stderr,
			runServer, startDevelopment, runInitializer)

		for _, link := range printedLinks(stdout.String()) {
			if linkPort(t, link) == busy {
				t.Errorf("dev printed a link to port %d, which another process holds: %s", busy, redactedLink(link))
			}
		}
		if code != starportcli.ExitCodeRuntime {
			t.Errorf("exit code = %d, want %d; stderr = %s", code, starportcli.ExitCodeRuntime, stderr.String())
		}
	})

	t.Run("free port", func(t *testing.T) {
		_, port := firstRunEnvironment(t)
		stdout := &firstRunOutput{}
		status := 0
		code, stderr := commandUntilReady(t, []string{"starport", "dev", "--no-open"}, port, stdout,
			func(ctx context.Context, _ string) {
				console := printedLine(stdout.String(), consoleLinkLine)
				if console == "" {
					t.Errorf("dev printed no console link; stdout = %s", stdout.String())
					return
				}
				if got := linkPort(t, console); got != port {
					t.Errorf("console link port = %d, want the bound port %d", got, port)
				}
				status = launchStatus(ctx, t, console)
			})
		if code != 0 {
			t.Fatalf("exit code = %d, want 0; stderr = %s", code, stderr)
		}
		if status != http.StatusSeeOther {
			t.Errorf("console link status = %d, want %d from this gateway", status, http.StatusSeeOther)
		}
	})
}

// holdLoopbackPort holds a loopback port for the life of the test, as another
// process would, and returns it.
func holdLoopbackPort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("hold a port: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	return listener.Addr().(*net.TCPAddr).Port
}

// printedLinks returns the gateway URL and the console link that dev printed.
func printedLinks(output string) []string {
	var links []string
	for _, prefix := range []string{"URL:", consoleLinkLine} {
		if link := printedLine(output, prefix); link != "" {
			links = append(links, link)
		}
	}
	return links
}

// printedLine returns the value of the first output line with the prefix, or "".
func printedLine(output, prefix string) string {
	scanner := bufio.NewScanner(strings.NewReader(output))
	for scanner.Scan() {
		if value, found := strings.CutPrefix(scanner.Text(), prefix); found {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func linkPort(t *testing.T, link string) int {
	t.Helper()
	parsed, err := url.Parse(link)
	if err != nil {
		t.Fatalf("parse printed link %s: %v", redactedLink(link), errors.Unwrap(err))
	}
	port, err := strconv.Atoi(parsed.Port())
	if err != nil {
		t.Fatalf("printed link %s names no port: %v", redactedLink(link), err)
	}
	return port
}

// redactedLink drops the query, so a launch ticket never reaches test output.
func redactedLink(link string) string {
	base, _, _ := strings.Cut(link, "?")
	return base
}

// launchStatus follows the console link once, without the redirect, and
// returns the status. Only the gateway that minted the ticket answers 303.
func launchStatus(ctx context.Context, t *testing.T, link string) int {
	t.Helper()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, link, nil)
	if err != nil {
		t.Fatalf("build the launch request: %v", err)
	}
	client := &http.Client{
		Timeout:       5 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	response, err := client.Do(request)
	if err != nil {
		// The url.Error text carries the ticket, so only its cause is shown.
		t.Errorf("follow the console link %s: %v", redactedLink(link), errors.Unwrap(err))
		return 0
	}
	_ = response.Body.Close()
	return response.StatusCode
}
