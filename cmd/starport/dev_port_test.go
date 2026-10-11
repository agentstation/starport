package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	starportcli "github.com/agentstation/starport/internal/cli"
	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/endpoint"
)

const consoleLinkLine = "Console (one-time launch link):"

// TestDevOpensTheBoundAddress keeps the console link on this gateway. The
// link that dev prints is the link that it opens. When another process holds
// the configured port, dev prints and opens no link to that port. When the
// port is free, the link names the port that this gateway serves, and this
// gateway spends its ticket. When no setting names the port and the first port
// is busy, dev binds the next free port, names both ports, and links the bound
// port.
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

	t.Run("fallback port", func(t *testing.T) {
		firstRunEnvironment(t)
		unsetServerPort(t)
		busy, free := holdLoopbackPort(t), freeLoopbackPort(t)

		stdout := &firstRunOutput{}
		status := 0
		code, stderr := developmentUntilReady(t, developmentOnPorts(busy, free), []string{"starport", "dev", "--no-open"}, free, stdout,
			func(ctx context.Context, _ string) {
				output := stdout.String()
				fallback := "Port " + strconv.Itoa(busy) + " is busy. Bound port " + strconv.Itoa(free) + "."
				if at, url := strings.Index(output, fallback), strings.Index(output, "URL:"); at < 0 || at > url {
					t.Errorf("dev did not name the busy and bound ports before the URL line; stdout = %s", output)
				}
				for _, link := range printedLinks(output) {
					if got := linkPort(t, link); got != free {
						t.Errorf("link %s port = %d, want the bound port %d", redactedLink(link), got, free)
					}
				}
				status = launchStatus(ctx, t, printedLine(output, consoleLinkLine))
			})
		if code != 0 {
			t.Fatalf("exit code = %d, want 0; stderr = %s", code, stderr)
		}
		if status != http.StatusSeeOther {
			t.Errorf("console link status = %d, want %d from this gateway", status, http.StatusSeeOther)
		}
	})
}

// TestDevWritesEndpointRecord records the bound port while dev runs and
// removes the record when dev exits. The record names the port in the console
// link, not the busy first port.
func TestDevWritesEndpointRecord(t *testing.T) {
	firstRunEnvironment(t)
	unsetServerPort(t)
	busy, free := holdLoopbackPort(t), freeLoopbackPort(t)
	cfg, err := config.LoadWithDefaults(t.Context())
	if err != nil {
		t.Fatalf("load the configuration: %v", err)
	}
	path := cfg.EndpointRecordFile()
	if path == "" {
		t.Fatal("the persistent profile selects no endpoint record")
	}

	stdout := &firstRunOutput{}
	code, stderr := developmentUntilReady(t, developmentOnPorts(busy, free), []string{"starport", "dev", "--no-open"}, free, stdout,
		func(context.Context, string) {
			record, live, err := endpoint.ReadRecord(path)
			if err != nil || !live {
				t.Errorf("no live endpoint record during the run: live = %t, err = %v", live, err)
				return
			}
			if console := printedLine(stdout.String(), consoleLinkLine); record.Port != linkPort(t, console) {
				t.Errorf("record port = %d, want the console link port %d", record.Port, linkPort(t, console))
			}
			if record.Port != free || record.PID != os.Getpid() || record.Scheme != "http" {
				t.Errorf("record = %+v, want port %d, pid %d, and scheme http", record, free, os.Getpid())
			}
		})
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr = %s", code, stderr)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the endpoint record remains after exit: %v", err)
	}
}

// TestDevKeepsExplicitPort binds only the port that the environment names.
// When another process holds it, dev exits 1 and names the port, although a
// later port in the order is free.
func TestDevKeepsExplicitPort(t *testing.T) {
	firstRunEnvironment(t)
	busy, free := holdLoopbackPort(t), freeLoopbackPort(t)
	t.Setenv("STARPORT_SERVER_PORT", strconv.Itoa(busy))

	assertDevKeepsBusyPort(t, busy, free)
}

// TestDevKeepsConfigurationFilePort binds only the port that the
// configuration file names, the same as a port from the environment.
func TestDevKeepsConfigurationFilePort(t *testing.T) {
	home, port := firstRunEnvironment(t)
	// A first run initializes the root and writes the configuration file.
	if code, stderr := commandUntilReady(t, []string{"starport", "dev", "--no-open"}, port, &firstRunOutput{}, nil); code != 0 {
		t.Fatalf("first run exit code = %d, want 0; stderr = %s", code, stderr)
	}
	unsetServerPort(t)
	busy, free := holdLoopbackPort(t), freeLoopbackPort(t)
	file, err := os.OpenFile(filepath.Join(home, "config", "config.env"), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString("STARPORT_SERVER_PORT=" + strconv.Itoa(busy) + "\n"); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	assertDevKeepsBusyPort(t, busy, free)
}

// assertDevKeepsBusyPort runs dev with an order that offers a free port after
// the busy port, and expects dev to refuse the fallback.
func assertDevKeepsBusyPort(t *testing.T, busy, free int) {
	t.Helper()
	code, stdout, stderr := developmentUntilExit(t, developmentOnPorts(busy, free), "dev", "--no-open")
	if code != starportcli.ExitCodeRuntime {
		t.Errorf("exit code = %d, want %d; stderr = %s", code, starportcli.ExitCodeRuntime, stderr)
	}
	if want := "port " + strconv.Itoa(busy) + " is busy"; !strings.Contains(stderr, want) {
		t.Errorf("stderr does not name the busy port %q; stderr = %s", want, stderr)
	}
	if strings.Contains(stdout, "is busy. Bound port") || len(printedLinks(stdout)) != 0 {
		t.Errorf("dev fell back to another port; stdout = %s", stdout)
	}
}

// developmentOnPorts starts dev with a fixed port order in place of the
// developer port order.
func developmentOnPorts(ports ...int) starportcli.DevelopmentStarter {
	return func(ctx context.Context, options starportcli.DevelopmentOptions, deliver starportcli.CredentialDelivery) (starportcli.DevelopmentSession, error) {
		return startDevelopmentOn(ctx, options, deliver, func(int) []int { return ports })
	}
}

// unsetServerPort removes the port that firstRunEnvironment sets, so the
// developer port order applies.
func unsetServerPort(t *testing.T) {
	t.Helper()
	t.Setenv("STARPORT_SERVER_PORT", "")
	if err := os.Unsetenv("STARPORT_SERVER_PORT"); err != nil {
		t.Fatal(err)
	}
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
