package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"runtime"
	"strings"
	"testing"

	starportcli "github.com/agentstation/starport/internal/cli"
)

func TestRunContextNoArgumentsShowsHelp(t *testing.T) {
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	calls := 0
	code := runContext(
		context.Background(),
		[]string{"starport"},
		bytes.NewReader(nil),
		stdout,
		stderr,
		func(context.Context, starportcli.GatewayOptions) error {
			calls++
			return nil
		},
		noopDevelopmentStarter,
		noopInitializer,
	)
	if code != 0 {
		t.Errorf("exit code = %d, want 0; stderr = %s", code, stderr.String())
	}
	if calls != 0 {
		t.Errorf("server calls = %d, want 0", calls)
	}
	if !strings.Contains(stdout.String(), "COMMANDS:") {
		t.Errorf("help output = %q", stdout.String())
	}
}

func TestRunContextServeDelegatesToRunner(t *testing.T) {
	called := false
	code := runContext(
		context.Background(),
		[]string{"starport", "serve"},
		bytes.NewReader(nil),
		&bytes.Buffer{},
		&bytes.Buffer{},
		func(context.Context, starportcli.GatewayOptions) error {
			called = true
			return nil
		},
		noopDevelopmentStarter,
		noopInitializer,
	)
	if code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
	if !called {
		t.Fatal("serve did not reach the process runner")
	}
}

func TestRunContextMapsRuntimeError(t *testing.T) {
	serverErr := errors.New("server failed")
	stderr := &bytes.Buffer{}
	code := runContext(
		context.Background(),
		[]string{"starport", "serve"},
		bytes.NewReader(nil),
		&bytes.Buffer{},
		stderr,
		func(context.Context, starportcli.GatewayOptions) error { return serverErr },
		noopDevelopmentStarter,
		noopInitializer,
	)
	if code != starportcli.ExitCodeRuntime {
		t.Errorf("exit code = %d, want %d", code, starportcli.ExitCodeRuntime)
	}
	if strings.Count(stderr.String(), serverErr.Error()) != 1 {
		t.Errorf("error output = %q, want one error", stderr.String())
	}
}

func TestRunContextWritesUsageErrorOnce(t *testing.T) {
	stderr := &bytes.Buffer{}
	code := runContext(
		context.Background(),
		[]string{"starport", "serve", "--invalid"},
		bytes.NewReader(nil),
		&bytes.Buffer{},
		stderr,
		func(context.Context, starportcli.GatewayOptions) error { return nil },
		noopDevelopmentStarter,
		noopInitializer,
	)
	if code != starportcli.ExitCodeUsage {
		t.Errorf("exit code = %d, want %d", code, starportcli.ExitCodeUsage)
	}
	if strings.Count(stderr.String(), "flag provided but not defined") != 1 {
		t.Errorf("error output = %q, want one diagnostic", stderr.String())
	}
}

func TestRunContextNormalizesMissingHelpTopic(t *testing.T) {
	code := runContext(
		context.Background(),
		[]string{"starport", "help", "missing"},
		bytes.NewReader(nil),
		&bytes.Buffer{},
		&bytes.Buffer{},
		func(context.Context, starportcli.GatewayOptions) error { return nil },
		noopDevelopmentStarter,
		noopInitializer,
	)
	if code != starportcli.ExitCodeUsage {
		t.Errorf("exit code = %d, want %d", code, starportcli.ExitCodeUsage)
	}
}

func TestRunContextWritesInvalidHelpErrorOnce(t *testing.T) {
	stderr := &bytes.Buffer{}
	code := runContext(
		context.Background(),
		[]string{"starport", "help", "--invalid"},
		bytes.NewReader(nil),
		&bytes.Buffer{},
		stderr,
		func(context.Context, starportcli.GatewayOptions) error { return nil },
		noopDevelopmentStarter,
		noopInitializer,
	)
	if code != starportcli.ExitCodeUsage {
		t.Errorf("exit code = %d, want %d", code, starportcli.ExitCodeUsage)
	}
	if strings.Count(stderr.String(), "flag provided but not defined") != 1 {
		t.Errorf("error output = %q, want one diagnostic", stderr.String())
	}
}

func TestRunContextVersionJSON(t *testing.T) {
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	code := runContext(
		context.Background(),
		[]string{"starport", "version", "--json"},
		bytes.NewReader(nil),
		stdout,
		stderr,
		func(context.Context, starportcli.GatewayOptions) error { return nil },
		noopDevelopmentStarter,
		noopInitializer,
	)
	if code != 0 {
		t.Fatalf("exit code = %d, stderr = %s", code, stderr.String())
	}
	var got starportcli.BuildInfo
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("decode version JSON: %v", err)
	}
	if got.Version != version || got.OS != runtime.GOOS || got.Arch != runtime.GOARCH {
		t.Errorf("version output = %#v", got)
	}
}

func TestRunContextInitDelegatesToInitializer(t *testing.T) {
	stdout := &bytes.Buffer{}
	var got starportcli.InitOptions
	code := runContext(
		context.Background(),
		[]string{"starport", "init", "--json"},
		bytes.NewReader(nil),
		stdout,
		&bytes.Buffer{},
		func(context.Context, starportcli.GatewayOptions) error { return nil },
		noopDevelopmentStarter,
		func(_ context.Context, options starportcli.InitOptions) (starportcli.InitResult, error) {
			got = options
			return starportcli.InitResult{
				APIKeyName: options.APIKeyName,
				ConfigFile: "/config/config.env", DataDir: "/config/data", APIKey: "gateway-key",
			}, nil
		},
	)
	if code != 0 {
		t.Fatalf("exit code = %d", code)
	}
	if got.APIKeyName != "local-admin" {
		t.Errorf("API key name = %q, want local-admin", got.APIKeyName)
	}
	var result starportcli.InitResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("decode init JSON: %v", err)
	}
	if result.APIKey != "gateway-key" {
		t.Errorf("API key = %q", result.APIKey)
	}
}

func TestBuildInformationUsesRuntimeGoVersionFallback(t *testing.T) {
	info := buildInformation()
	if goVersion == "unknown" && info.GoVersion != runtime.Version() {
		t.Errorf("Go version = %q, want %q", info.GoVersion, runtime.Version())
	}
}

func noopInitializer(context.Context, starportcli.InitOptions) (starportcli.InitResult, error) {
	return starportcli.InitResult{}, nil
}

func noopDevelopmentStarter(
	context.Context,
	starportcli.GatewayOptions,
) (starportcli.DevelopmentSession, error) {
	return starportcli.DevelopmentSession{
		URL: "http://127.0.0.1:8080", APIKey: "development-key",
		Run:   func(context.Context) error { return nil },
		Close: func(context.Context) error { return nil },
	}, nil
}
