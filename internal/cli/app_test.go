package cli

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	urfavecli "github.com/urfave/cli/v3"
)

func TestNoArgumentsShowHelp(t *testing.T) {
	deps, stdout, _ := testDependencies()
	if err := Run(context.Background(), []string{"starport"}, deps); err != nil {
		t.Fatalf("run without arguments: %v", err)
	}
	for _, want := range []string{"NAME:", "starport", "COMMANDS:", "serve", "version"} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("help output does not contain %q:\n%s", want, stdout.String())
		}
	}
}

func TestVersionFlagUsesInjectedOutput(t *testing.T) {
	deps, stdout, _ := testDependencies()
	if err := Run(context.Background(), []string{"starport", "--version"}, deps); err != nil {
		t.Fatalf("run version flag: %v", err)
	}
	if !strings.Contains(stdout.String(), deps.Build.Version) {
		t.Errorf("version output = %q, want version %q", stdout.String(), deps.Build.Version)
	}
}

func TestServeUsesInjectedRunner(t *testing.T) {
	deps, _, _ := testDependencies()
	var got ServeOptions
	deps.RunServer = func(_ context.Context, options ServeOptions) error {
		got = options
		return nil
	}
	if err := Run(
		context.Background(),
		[]string{"starport", "serve", "--enable-ollama"},
		deps,
	); err != nil {
		t.Fatalf("run serve: %v", err)
	}
	if !got.EnableOllama {
		t.Fatal("serve did not pass the Ollama option")
	}
}

func TestUnknownCommandReturnsUsageError(t *testing.T) {
	deps, stdout, _ := testDependencies()
	err := Run(context.Background(), []string{"starport", "invalid"}, deps)
	if err == nil {
		t.Fatal("unknown command returned no error")
	}
	if ExitCode(err) != ExitCodeUsage {
		t.Errorf("exit code = %d, want %d", ExitCode(err), ExitCodeUsage)
	}
	if !strings.Contains(stdout.String(), "COMMANDS:") {
		t.Errorf("unknown command did not show help:\n%s", stdout.String())
	}
}

func TestInvalidFlagReturnsUsageError(t *testing.T) {
	deps, stdout, stderr := testDependencies()
	err := Run(context.Background(), []string{"starport", "serve", "--invalid"}, deps)
	if err == nil {
		t.Fatal("invalid flag returned no error")
	}
	if ExitCode(err) != ExitCodeUsage {
		t.Errorf("exit code = %d, want %d", ExitCode(err), ExitCodeUsage)
	}
	if stderr.Len() != 0 {
		t.Errorf("command layer wrote process error output: %q", stderr.String())
	}
	if !strings.Contains(stdout.String(), "starport serve") {
		t.Errorf("serve help output = %q", stdout.String())
	}
}

func TestMissingHelpTopicReturnsUsageError(t *testing.T) {
	deps, _, _ := testDependencies()
	err := Run(context.Background(), []string{"starport", "help", "missing"}, deps)
	if err == nil {
		t.Fatal("missing help topic returned no error")
	}
	if ExitCode(err) != ExitCodeUsage {
		t.Errorf("exit code = %d, want %d", ExitCode(err), ExitCodeUsage)
	}
}

func TestHelpInvalidFlagReturnsOneUsageError(t *testing.T) {
	deps, _, stderr := testDependencies()
	err := Run(context.Background(), []string{"starport", "help", "--invalid"}, deps)
	if err == nil {
		t.Fatal("invalid help flag returned no error")
	}
	if ExitCode(err) != ExitCodeUsage {
		t.Errorf("exit code = %d, want %d", ExitCode(err), ExitCodeUsage)
	}
	if stderr.Len() != 0 {
		t.Errorf("command layer wrote process error output: %q", stderr.String())
	}
}

func TestHelpShowsCommandHelp(t *testing.T) {
	deps, stdout, _ := testDependencies()
	if err := Run(context.Background(), []string{"starport", "help", "serve"}, deps); err != nil {
		t.Fatalf("run serve help: %v", err)
	}
	if !strings.Contains(stdout.String(), "starport serve") {
		t.Errorf("serve help output = %q", stdout.String())
	}
}

func TestRunReturnsServerFailure(t *testing.T) {
	serverErr := errors.New("server failed")
	deps, _, _ := testDependencies()
	deps.RunServer = func(context.Context, ServeOptions) error { return serverErr }
	err := Run(context.Background(), []string{"starport", "serve"}, deps)
	if !errors.Is(err, serverErr) {
		t.Fatalf("run error = %v, want %v", err, serverErr)
	}
	if ExitCode(err) != ExitCodeRuntime {
		t.Errorf("exit code = %d, want %d", ExitCode(err), ExitCodeRuntime)
	}
}

func TestServerExitCoderRemainsRuntimeFailure(t *testing.T) {
	deps, _, _ := testDependencies()
	deps.RunServer = func(context.Context, ServeOptions) error {
		return urfavecli.Exit("server dependency failed", 42)
	}
	err := Run(context.Background(), []string{"starport", "serve"}, deps)
	if ExitCode(err) != ExitCodeRuntime {
		t.Errorf("exit code = %d, want %d", ExitCode(err), ExitCodeRuntime)
	}
}

func TestNewRequiresProcessDependencies(t *testing.T) {
	valid, _, _ := testDependencies()
	tests := []struct {
		name string
		edit func(*Dependencies)
		want error
	}{
		{name: "input", edit: func(deps *Dependencies) { deps.Stdin = nil }, want: ErrStdinRequired},
		{name: "output", edit: func(deps *Dependencies) { deps.Stdout = nil }, want: ErrStdoutRequired},
		{name: "error output", edit: func(deps *Dependencies) { deps.Stderr = nil }, want: ErrStderrRequired},
		{name: "server", edit: func(deps *Dependencies) { deps.RunServer = nil }, want: ErrServerRunnerRequired},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			deps := valid
			test.edit(&deps)
			_, err := New(deps)
			if !errors.Is(err, test.want) {
				t.Fatalf("New() error = %v, want %v", err, test.want)
			}
		})
	}
}

func testDependencies() (Dependencies, *bytes.Buffer, *bytes.Buffer) {
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	return Dependencies{
		Stdin:  bytes.NewReader(nil),
		Stdout: stdout,
		Stderr: stderr,
		Build: BuildInfo{
			Version: "v1.0.0-test", BuildTime: "2026-08-09T00:00:00Z",
			GitCommit: "abc123", GitBranch: "test", GoVersion: "go1.26.5",
			OS: "testos", Arch: "testarch",
		},
		RunServer: func(context.Context, ServeOptions) error { return nil },
	}, stdout, stderr
}
