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
