package cli

import (
	"context"
	"strings"
	"testing"
)

// TestDevSecondRunPrintsNoKey starts a session that requires a key but minted
// none, as a second run on an initialized root does. The banner still names
// the URL, the authentication state, and the console link, and prints no key.
func TestDevSecondRunPrintsNoKey(t *testing.T) {
	deps, stdout, _ := testDependencies()
	const consoleURL = "http://127.0.0.1:7827/console/launch#ticket"
	ran := false
	deps.StartDevelopment = func(context.Context, DevelopmentOptions, CredentialDelivery) (DevelopmentSession, error) {
		return DevelopmentSession{
			URL: "http://127.0.0.1:7827", ConsoleURL: consoleURL,
			Run: func(context.Context) error {
				ran = true
				return nil
			},
			Close: func(context.Context) error { return nil },
		}, nil
	}
	if err := Run(context.Background(), []string{"starport", "dev", "--no-open"}, deps); err != nil {
		t.Fatalf("run dev: %v", err)
	}
	if !ran {
		t.Error("the session did not run")
	}
	output := stdout.String()
	if strings.Contains(output, "Gateway API key") {
		t.Errorf("a second run printed a key line; output = %q", output)
	}
	for _, want := range []string{"URL: http://127.0.0.1:7827", "Authentication: required", consoleURL} {
		if !strings.Contains(output, want) {
			t.Errorf("output lacks %q; output = %q", want, output)
		}
	}
}

// TestDevReportsTheBusyPort names the busy standard port and the bound port
// after the heading and before the URL line. A session that did not fall back
// prints no such line.
func TestDevReportsTheBusyPort(t *testing.T) {
	for _, test := range []struct {
		name     string
		busyPort int
		want     string
	}{
		{name: "fallback", busyPort: 7827, want: "Starport development gateway\nPort 7827 is busy. Bound port 7373.\nURL: http://127.0.0.1:7373\n"},
		{name: "standard port", want: "Starport development gateway\nURL: http://127.0.0.1:7373\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			deps, stdout, _ := testDependencies()
			deps.StartDevelopment = func(context.Context, DevelopmentOptions, CredentialDelivery) (DevelopmentSession, error) {
				return DevelopmentSession{
					URL: "http://127.0.0.1:7373", BusyPort: test.busyPort,
					Run:   func(context.Context) error { return nil },
					Close: func(context.Context) error { return nil },
				}, nil
			}
			if err := Run(context.Background(), []string{"starport", "dev", "--no-open"}, deps); err != nil {
				t.Fatalf("run dev: %v", err)
			}
			if output := stdout.String(); !strings.HasPrefix(output, test.want) {
				t.Errorf("output = %q, want the prefix %q", output, test.want)
			}
		})
	}
}
