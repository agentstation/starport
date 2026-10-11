package cli

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestAuthBootstrapUsesInjectedRunnerAndJSONOutput(t *testing.T) {
	deps, stdout, _ := testDependencies()
	var got InitOptions
	deps.Initialize = func(_ context.Context, options InitOptions) (InitResult, error) {
		got = options
		return InitResult{APIKeyName: options.APIKeyName, APIKey: "gateway-key"}, nil
	}
	if err := runAuth(t, deps, "bootstrap", "--name", "developer", "--json"); err != nil {
		t.Fatalf("run auth bootstrap: %v", err)
	}
	if got != (InitOptions{APIKeyName: "developer"}) {
		t.Errorf("bootstrap options = %#v", got)
	}
	var result InitResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("decode bootstrap JSON: %v", err)
	}
	if result.APIKey != "gateway-key" || result.APIKeyName != "developer" {
		t.Errorf("bootstrap output = %#v", result)
	}
}

func TestAuthBootstrapTextOutputNamesServe(t *testing.T) {
	deps, stdout, _ := testDependencies()
	deps.Initialize = func(_ context.Context, options InitOptions) (InitResult, error) {
		return InitResult{APIKeyName: options.APIKeyName, APIKey: "gateway-key"}, nil
	}
	if err := runAuth(t, deps, "bootstrap", "--name", "developer"); err != nil {
		t.Fatalf("run auth bootstrap: %v", err)
	}
	for _, want := range []string{"Gateway API key (shown once): gateway-key", "Run: starport serve"} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("bootstrap output does not contain %q: %q", want, stdout.String())
		}
	}
	if strings.Contains(stdout.String(), "Configuration:") {
		t.Errorf("shared storage bootstrap names a configuration file: %q", stdout.String())
	}
}

func TestAuthBootstrapUsageErrors(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "no operation", args: nil, want: "requires --name or --recover"},
		{name: "both operations", args: []string{"--name", "developer", "--recover"}, want: "accepts --name or --recover, not both"},
		{name: "empty name", args: []string{"--name", ""}, want: ""},
		{name: "invalid name", args: []string{"--name", "invalid name"}, want: ""},
		{name: "argument", args: []string{"--name", "developer", "extra"}, want: ""},
		{name: "removed flag", args: []string{"--configured-storage"}, want: ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			deps, _, _ := testDependencies()
			called := false
			deps.Initialize = func(context.Context, InitOptions) (InitResult, error) {
				called = true
				return InitResult{}, nil
			}
			err := runAuth(t, deps, append([]string{"bootstrap"}, test.args...)...)
			if err == nil || ExitCode(err) != ExitCodeUsage {
				t.Fatalf("usage error = %v, exit code = %d", err, ExitCode(err))
			}
			if !strings.Contains(err.Error(), test.want) {
				t.Errorf("usage error = %q, want %q", err, test.want)
			}
			if called {
				t.Fatal("auth bootstrap reached the initializer")
			}
		})
	}
}

func TestAuthBootstrapRollsBackWhenCredentialOutputFails(t *testing.T) {
	deps, _, _ := testDependencies()
	outputErr := errors.New("output unavailable")
	deps.Stdout = failingWriter{err: outputErr}
	rollbackCalls := 0
	deps.Initialize = func(context.Context, InitOptions) (InitResult, error) {
		return InitResult{
			APIKeyName: "developer", APIKey: "gateway-key",
			Rollback: func(context.Context) error {
				rollbackCalls++
				return nil
			},
		}, nil
	}
	err := runAuth(t, deps, "bootstrap", "--name", "developer")
	if !errors.Is(err, outputErr) || ExitCode(err) != ExitCodeRuntime {
		t.Fatalf("output error = %v, exit code = %d", err, ExitCode(err))
	}
	if rollbackCalls != 1 {
		t.Fatalf("rollback calls = %d, want 1", rollbackCalls)
	}
}

func TestAuthBootstrapReturnsCredentialBeforePostCommitFailure(t *testing.T) {
	deps, stdout, _ := testDependencies()
	closeErr := errors.New("storage close failed")
	deps.Initialize = func(context.Context, InitOptions) (InitResult, error) {
		return InitResult{APIKeyName: "developer", APIKey: "gateway-key"}, closeErr
	}
	err := runAuth(t, deps, "bootstrap", "--name", "developer")
	if !errors.Is(err, closeErr) || ExitCode(err) != ExitCodeRuntime {
		t.Fatalf("bootstrap error = %v, exit code = %d", err, ExitCode(err))
	}
	if !strings.Contains(stdout.String(), "gateway-key") {
		t.Errorf("bootstrap output = %q", stdout.String())
	}
}

func TestAuthBootstrapRecoverPrintsTheRetriedKey(t *testing.T) {
	deps, stdout, _ := testDependencies()
	var got InitOptions
	deps.Initialize = func(_ context.Context, options InitOptions) (InitResult, error) {
		got = options
		return InitResult{
			APIKeyName: "local-admin",
			ConfigFile: "/config/config.env", DataDir: "/config/data", APIKey: "gateway-key",
		}, nil
	}
	if err := runAuth(t, deps, "bootstrap", "--recover"); err != nil {
		t.Fatalf("run auth bootstrap --recover: %v", err)
	}
	if got != (InitOptions{Recover: true}) {
		t.Errorf("recover options = %#v", got)
	}
	if strings.Count(stdout.String(), "gateway-key") != 1 || !strings.Contains(stdout.String(), "Configuration: /config/config.env") {
		t.Errorf("recover output = %q", stdout.String())
	}
}

func TestAuthBootstrapRecoverReportsASettledSetup(t *testing.T) {
	for _, test := range []struct {
		name string
		args []string
		want string
	}{
		{name: "text", args: nil, want: "Recovered the interrupted local setup."},
		{name: "json", args: []string{"--json"}, want: `"recovered": true`},
	} {
		t.Run(test.name, func(t *testing.T) {
			deps, stdout, _ := testDependencies()
			deps.Initialize = func(context.Context, InitOptions) (InitResult, error) { return InitResult{}, nil }
			if err := runAuth(t, deps, append([]string{"bootstrap", "--recover"}, test.args...)...); err != nil {
				t.Fatalf("run auth bootstrap --recover: %v", err)
			}
			if !strings.Contains(stdout.String(), test.want) {
				t.Errorf("recover output = %q, want %q", stdout.String(), test.want)
			}
		})
	}
}

func TestAuthBootstrapRecoverFailureIsRuntimeError(t *testing.T) {
	deps, stdout, _ := testDependencies()
	recoverErr := errors.New("no interrupted local setup to recover")
	deps.Initialize = func(context.Context, InitOptions) (InitResult, error) { return InitResult{}, recoverErr }
	err := runAuth(t, deps, "bootstrap", "--recover")
	if !errors.Is(err, recoverErr) || ExitCode(err) != ExitCodeRuntime {
		t.Fatalf("recover error = %v, exit code = %d", err, ExitCode(err))
	}
	if stdout.Len() != 0 {
		t.Errorf("failed recovery wrote output: %q", stdout.String())
	}
}
