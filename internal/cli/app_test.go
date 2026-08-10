package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/agentstation/starmap/pkg/catalogs"
	urfavecli "github.com/urfave/cli/v3"

	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/diagnosis"
)

func TestNoArgumentsShowHelp(t *testing.T) {
	deps, stdout, _ := testDependencies()
	if err := Run(context.Background(), []string{"starport"}, deps); err != nil {
		t.Fatalf("run without arguments: %v", err)
	}
	for _, want := range []string{"NAME:", "starport", "COMMANDS:", "serve", "doctor", "config", "version"} {
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

func TestInitUsesInjectedRunnerAndJSONOutput(t *testing.T) {
	deps, stdout, _ := testDependencies()
	var got InitOptions
	deps.Initialize = func(_ context.Context, options InitOptions) (InitResult, error) {
		got = options
		return InitResult{
			Provider: options.Provider, IdentityName: options.IdentityName,
			ConfigFile: "/config/config.env", DataDir: "/config/data", APIKey: "gateway-key",
		}, nil
	}
	if err := Run(context.Background(), []string{
		"starport", "init", "--provider", "ollama", "--name", "developer", "--json",
	}, deps); err != nil {
		t.Fatalf("run init: %v", err)
	}
	if got.Provider != catalogs.ProviderIDOllama || got.IdentityName != "developer" {
		t.Errorf("init options = %#v", got)
	}
	var result InitResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("decode init JSON: %v", err)
	}
	if result.APIKey != "gateway-key" || result.ConfigFile != "/config/config.env" {
		t.Errorf("init output = %#v", result)
	}
}

func TestInitRejectsUnsupportedProviderAsUsageError(t *testing.T) {
	deps, _, _ := testDependencies()
	for _, args := range [][]string{
		{"starport", "init"},
		{"starport", "init", "--provider", "unknown"},
		{"starport", "init", "--provider", "ollama", "--configured-storage"},
	} {
		err := Run(context.Background(), args, deps)
		if err == nil || ExitCode(err) != ExitCodeUsage {
			t.Fatalf("%v error = %v, exit code = %d", args, err, ExitCode(err))
		}
	}
}

func TestInitRejectsInvalidIdentityNameAsUsageError(t *testing.T) {
	deps, _, _ := testDependencies()
	called := false
	deps.Initialize = func(context.Context, InitOptions) (InitResult, error) {
		called = true
		return InitResult{}, nil
	}
	err := Run(context.Background(), []string{
		"starport", "init", "--provider", "ollama", "--name", "invalid name",
	}, deps)
	if err == nil || ExitCode(err) != ExitCodeUsage {
		t.Fatalf("invalid name error = %v, exit code = %d", err, ExitCode(err))
	}
	if called {
		t.Fatal("initializer ran for an invalid identity name")
	}
}

func TestInitRollsBackWhenCredentialOutputFails(t *testing.T) {
	deps, _, _ := testDependencies()
	outputErr := errors.New("output unavailable")
	deps.Stdout = failingWriter{err: outputErr}
	rollbackCalls := 0
	deps.Initialize = func(context.Context, InitOptions) (InitResult, error) {
		return InitResult{
			Provider: catalogs.ProviderIDOllama, IdentityName: "local-admin",
			ConfigFile: "/config/config.env", DataDir: "/config/data", APIKey: "gateway-key",
			Rollback: func(context.Context) error {
				rollbackCalls++
				return nil
			},
		}, nil
	}
	err := Run(context.Background(), []string{"starport", "init", "--provider", "ollama"}, deps)
	if !errors.Is(err, outputErr) || ExitCode(err) != ExitCodeRuntime {
		t.Fatalf("output error = %v, exit code = %d", err, ExitCode(err))
	}
	if rollbackCalls != 1 {
		t.Fatalf("rollback calls = %d, want 1", rollbackCalls)
	}
}

func TestInitReturnsCredentialBeforePostCommitFailure(t *testing.T) {
	deps, stdout, _ := testDependencies()
	closeErr := errors.New("storage close failed")
	deps.Initialize = func(context.Context, InitOptions) (InitResult, error) {
		return InitResult{
			IdentityName: "local-admin", APIKey: "gateway-key",
		}, closeErr
	}
	err := Run(context.Background(), []string{"starport", "init", "--configured-storage"}, deps)
	if !errors.Is(err, closeErr) || ExitCode(err) != ExitCodeRuntime {
		t.Fatalf("initialization error = %v, exit code = %d", err, ExitCode(err))
	}
	if !strings.Contains(stdout.String(), "gateway-key") {
		t.Errorf("initialization output = %q", stdout.String())
	}
}

func TestInitConfiguredStorageNeedsNoProvider(t *testing.T) {
	deps, stdout, _ := testDependencies()
	var got InitOptions
	deps.Initialize = func(_ context.Context, options InitOptions) (InitResult, error) {
		got = options
		return InitResult{IdentityName: options.IdentityName, APIKey: "gateway-key"}, nil
	}
	if err := Run(context.Background(), []string{
		"starport", "init", "--configured-storage",
	}, deps); err != nil {
		t.Fatalf("run configured setup: %v", err)
	}
	if !got.ConfiguredStorage || got.Provider != "" {
		t.Errorf("init options = %#v", got)
	}
	if strings.Contains(stdout.String(), "Configuration:") || !strings.Contains(stdout.String(), "gateway-key") {
		t.Errorf("configured setup output = %q", stdout.String())
	}
}

func TestInitOllamaOutputRequiresStarmapWorkspace(t *testing.T) {
	deps, stdout, _ := testDependencies()
	deps.Initialize = func(_ context.Context, options InitOptions) (InitResult, error) {
		return InitResult{
			Provider: options.Provider, IdentityName: options.IdentityName,
			ConfigFile: "/config/config.env", DataDir: "/config/data", APIKey: "gateway-key",
		}, nil
	}
	if err := Run(context.Background(), []string{
		"starport", "init", "--provider", "ollama",
	}, deps); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "reviewed Starmap workspace") {
		t.Errorf("Ollama setup output = %q", stdout.String())
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

func TestConfigShowJSONRedactsSecrets(t *testing.T) {
	deps, stdout, _ := testDependencies()
	secret := "secret-that-must-not-appear"
	deps.LoadConfig = func(context.Context) (*config.Config, error) {
		return &config.Config{
			Providers: config.ProvidersConfig{OpenAI: config.ProviderConfig{APIKey: secret}},
			Security:  config.SecurityConfig{MasterKey: secret},
		}, nil
	}
	if err := Run(context.Background(), []string{"starport", "config", "show", "--json"}, deps); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stdout.String(), secret) || !strings.Contains(stdout.String(), "<redacted>") {
		t.Errorf("configuration output = %q", stdout.String())
	}
}

func TestConfigPathsJSONUsesInjectedResolver(t *testing.T) {
	deps, stdout, _ := testDependencies()
	want := config.PathsForConfigDir("/test/config")
	deps.ResolvePaths = func() (config.Paths, error) {
		return want, nil
	}
	if err := Run(context.Background(), []string{"starport", "config", "paths", "--json"}, deps); err != nil {
		t.Fatal(err)
	}
	var paths config.Paths
	if err := json.Unmarshal(stdout.Bytes(), &paths); err != nil {
		t.Fatal(err)
	}
	if paths.ConfigFile != want.ConfigFile {
		t.Errorf("paths = %#v", paths)
	}
}

func TestConfigValidateUsesInjectedLoader(t *testing.T) {
	deps, stdout, _ := testDependencies()
	calls := 0
	deps.LoadConfig = func(context.Context) (*config.Config, error) {
		calls++
		return &config.Config{}, nil
	}
	if err := Run(context.Background(), []string{"starport", "config", "validate", "--json"}, deps); err != nil {
		t.Fatal(err)
	}
	if calls != 1 || !strings.Contains(stdout.String(), `"valid": true`) {
		t.Errorf("calls = %d, output = %q", calls, stdout.String())
	}
}

func TestConfigInspectionDoesNotExposeLoaderErrors(t *testing.T) {
	for _, command := range [][]string{
		{"starport", "config", "show"},
		{"starport", "config", "validate"},
	} {
		deps, _, _ := testDependencies()
		secret := "loader-secret-that-must-not-appear"
		deps.LoadConfig = func(context.Context) (*config.Config, error) {
			return nil, errors.New("malformed credential " + secret)
		}
		err := Run(context.Background(), command, deps)
		if err == nil {
			t.Fatalf("%v returned no error", command)
		}
		if strings.Contains(err.Error(), secret) {
			t.Errorf("%v error contains loader secret: %q", command, err)
		}
	}
}

func TestConfigValidateJSONReportsSafeFailure(t *testing.T) {
	deps, stdout, _ := testDependencies()
	secret := "loader-secret-that-must-not-appear"
	deps.LoadConfig = func(context.Context) (*config.Config, error) {
		return nil, errors.New("malformed credential " + secret)
	}

	err := Run(context.Background(), []string{"starport", "config", "validate", "--json"}, deps)
	if err == nil || ExitCode(err) != ExitCodeRuntime {
		t.Fatalf("validation error = %v, exit code = %d", err, ExitCode(err))
	}
	var result validationResult
	if decodeErr := json.Unmarshal(stdout.Bytes(), &result); decodeErr != nil {
		t.Fatal(decodeErr)
	}
	if result.Valid || result.Error != "configuration could not be loaded" {
		t.Errorf("validation result = %#v", result)
	}
	if strings.Contains(stdout.String(), secret) || strings.Contains(err.Error(), secret) {
		t.Errorf("validation exposed loader secret: output=%q error=%q", stdout.String(), err)
	}
}

func TestDoctorReportsExactFailuresAndReturnsRuntimeExit(t *testing.T) {
	deps, stdout, _ := testDependencies()
	deps.Diagnose = func(context.Context, diagnosis.Options) diagnosis.Report {
		return diagnosis.Report{
			OK: false, Failed: 1,
			Checks: []diagnosis.Check{{
				ID: "identities", Status: diagnosis.StatusFail,
				Message: "gateway identity storage is empty",
			}},
		}
	}
	err := Run(context.Background(), []string{"starport", "doctor"}, deps)
	if !errors.Is(err, ErrDiagnosisFailed) || ExitCode(err) != ExitCodeRuntime {
		t.Fatalf("doctor error = %v, exit code = %d", err, ExitCode(err))
	}
	if !strings.Contains(stdout.String(), "FAIL identities: gateway identity storage is empty") {
		t.Errorf("doctor output = %q", stdout.String())
	}
}

func TestDoctorPassesProbeOptionAndWritesJSON(t *testing.T) {
	deps, stdout, _ := testDependencies()
	var got diagnosis.Options
	deps.Diagnose = func(_ context.Context, options diagnosis.Options) diagnosis.Report {
		got = options
		return diagnosis.Report{OK: true, Probed: options.Probe, Checks: []diagnosis.Check{}}
	}
	if err := Run(context.Background(), []string{"starport", "doctor", "--probe", "--json"}, deps); err != nil {
		t.Fatal(err)
	}
	if !got.Probe || !strings.Contains(stdout.String(), `"probed": true`) {
		t.Errorf("options = %#v, output = %q", got, stdout.String())
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
		{name: "initializer", edit: func(deps *Dependencies) { deps.Initialize = nil }, want: ErrInitializerRequired},
		{name: "configuration loader", edit: func(deps *Dependencies) { deps.LoadConfig = nil }, want: ErrConfigLoaderRequired},
		{name: "path resolver", edit: func(deps *Dependencies) { deps.ResolvePaths = nil }, want: ErrPathResolverRequired},
		{name: "diagnoser", edit: func(deps *Dependencies) { deps.Diagnose = nil }, want: ErrDiagnoserRequired},
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
		RunServer:    func(context.Context, ServeOptions) error { return nil },
		Initialize:   func(context.Context, InitOptions) (InitResult, error) { return InitResult{}, nil },
		LoadConfig:   func(context.Context) (*config.Config, error) { return &config.Config{}, nil },
		ResolvePaths: func() (config.Paths, error) { return config.PathsForConfigDir("/test"), nil },
		Diagnose: func(context.Context, diagnosis.Options) diagnosis.Report {
			return diagnosis.Report{OK: true, Checks: []diagnosis.Check{}}
		},
	}, stdout, stderr
}

type failingWriter struct{ err error }

func (writer failingWriter) Write([]byte) (int, error) { return 0, writer.err }
