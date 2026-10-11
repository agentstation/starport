package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/joho/godotenv"

	starportcli "github.com/agentstation/starport/internal/cli"
	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/localauth"
	"github.com/agentstation/starport/internal/setup"
)

func TestServeReloadsConfigurationAfterInitialization(t *testing.T) {
	home, _ := firstRunEnvironment(t)
	gateway := &recordingGateway{}

	code, stderr := serveWith(t, &bytes.Buffer{}, gateway.open)

	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr = %s", code, stderr)
	}
	values, err := godotenv.Read(filepath.Join(home, "config", "config.env"))
	if err != nil {
		t.Fatalf("read the generated configuration: %v", err)
	}
	written := values["STARPORT_SECURITY_MASTER_KEY"]
	if written == "" || gateway.cfg == nil || gateway.cfg.Security.MasterKey != written {
		t.Fatal("the application did not receive the generated master key")
	}
	if _, present := os.LookupEnv("STARPORT_SECURITY_MASTER_KEY"); present {
		t.Error("initialization changed the process environment")
	}
}

func TestServeProvisionsMachineToken(t *testing.T) {
	home, _ := firstRunEnvironment(t)
	gateway := &recordingGateway{}

	code, stderr := serveWith(t, &bytes.Buffer{}, gateway.open)

	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr = %s", code, stderr)
	}
	want := filepath.Join(home, "data", "local-admin-token.json")
	if gateway.cfg == nil || gateway.cfg.Security.LocalTokenPath != want {
		t.Fatalf("local token path = %v, want %q", gateway.cfg, want)
	}
	if gateway.tokenErr != nil {
		t.Errorf("the token file was not on disk when the application opened: %v", gateway.tokenErr)
	}
}

func TestServeRollsBackWhenKeyOutputFails(t *testing.T) {
	home, _ := firstRunEnvironment(t)
	gateway := &recordingGateway{}
	outputErr := errors.New("output unavailable")

	code, stderr := serveWith(t, failingOutput{err: outputErr}, gateway.open)

	if code != starportcli.ExitCodeRuntime || !strings.Contains(stderr, outputErr.Error()) {
		t.Fatalf("exit code = %d, stderr = %s", code, stderr)
	}
	if gateway.cfg != nil {
		t.Error("the application opened after the key output failed")
	}
	paths, err := config.PlatformPaths()
	if err != nil {
		t.Fatal(err)
	}
	if state, err := setup.Inspect(paths); err != nil || state != setup.StateAbsent {
		t.Fatalf("setup state after rollback = %q, %v; want absent", state, err)
	}
	for _, name := range []string{"config/config.env", "data/badger", "data/welcomed", "data/local-admin-token.json"} {
		if _, err := os.Stat(filepath.Join(home, name)); !os.IsNotExist(err) {
			t.Errorf("%s after rollback: %v", name, err)
		}
	}

	stdout := &bytes.Buffer{}
	if code, stderr := serveWith(t, stdout, gateway.open); code != 0 {
		t.Fatalf("retry exit code = %d, stderr = %s", code, stderr)
	}
	if !strings.Contains(stdout.String(), gatewayKeyLine) {
		t.Errorf("retry output = %q", stdout.String())
	}
}

func TestServeKeepsPartialStateError(t *testing.T) {
	home, _ := firstRunEnvironment(t)
	if err := os.MkdirAll(filepath.Join(home, "data"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "data", "stray"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	gateway := &recordingGateway{}
	stdout := &bytes.Buffer{}

	code, stderr := serveWith(t, stdout, gateway.open)

	if code != starportcli.ExitCodeRuntime {
		t.Fatalf("exit code = %d, want %d", code, starportcli.ExitCodeRuntime)
	}
	if !strings.Contains(stderr, "starport setup state is incomplete") || !strings.Contains(stderr, setup.RecoverCommand) {
		t.Errorf("partial state error = %q", stderr)
	}
	if gateway.cfg != nil {
		t.Error("the application opened over a partial root")
	}
	if stdout.Len() != 0 {
		t.Errorf("serve printed %q over a partial root", stdout.String())
	}
	for _, name := range []string{"config/config.env", "data/welcomed"} {
		if _, err := os.Stat(filepath.Join(home, name)); !os.IsNotExist(err) {
			t.Errorf("%s after refusal: %v", name, err)
		}
	}
}

func TestServeSkipsInitializationOnConfiguredStorage(t *testing.T) {
	for _, test := range []struct {
		name string
		set  func(t *testing.T, home string) map[string]string
	}{
		{name: "configured Badger path", set: func(t *testing.T, _ string) map[string]string {
			return map[string]string{"STARPORT_STORAGE_BADGER_PATH": filepath.Join(t.TempDir(), "badger")}
		}},
		{name: "configured SQLite path", set: func(t *testing.T, _ string) map[string]string {
			return map[string]string{"STARPORT_STORAGE_SQL_SQLITE_PATH": filepath.Join(t.TempDir(), "starport.db")}
		}},
		{name: "external management", set: func(*testing.T, string) map[string]string {
			return map[string]string{"STARPORT_CONFIG_MANAGEMENT": config.ManagementExternal}
		}},
		{name: "supplied master key", set: func(*testing.T, string) map[string]string {
			return map[string]string{"STARPORT_SECURITY_MASTER_KEY": strings.Repeat("m", 32)}
		}},
		{name: "network address", set: func(*testing.T, string) map[string]string {
			return map[string]string{"STARPORT_SERVER_HOST": "0.0.0.0"}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			home, _ := firstRunEnvironment(t)
			for name, value := range test.set(t, home) {
				t.Setenv(name, value)
			}
			gateway := &recordingGateway{}
			stdout := &bytes.Buffer{}

			code, stderr := serveWith(t, stdout, gateway.open)

			if code != 0 {
				t.Fatalf("exit code = %d, want 0; stderr = %s", code, stderr)
			}
			if gateway.cfg == nil {
				t.Fatal("serve did not reach the application")
			}
			if strings.Contains(stdout.String(), gatewayKeyLine) {
				t.Error("serve minted a key outside the local platform root")
			}
			for _, name := range []string{"config/config.env", "data/badger", "data/local-admin-token.json"} {
				if _, err := os.Stat(filepath.Join(home, name)); !os.IsNotExist(err) {
					t.Errorf("%s after a skipped initialization: %v", name, err)
				}
			}
		})
	}
}

// serveWith runs the serve command through the real command output and
// first-run composition, with the application replaced by open.
func serveWith(t *testing.T, stdout io.Writer, open gatewayOpener) (int, string) {
	t.Helper()
	stderr := &bytes.Buffer{}
	code := runContext(t.Context(), []string{"starport", "serve"}, bytes.NewReader(nil), stdout, stderr,
		func(ctx context.Context, options starportcli.GatewayOptions, output starportcli.ServerOutput) error {
			return serveGateway(ctx, options, output, open)
		},
		noopDevelopmentStarter, runInitializer)
	return code, stderr.String()
}

// recordingGateway stands in for the application. It records the
// configuration it opens with and whether the machine token was on disk then.
type recordingGateway struct {
	cfg      *config.Config
	tokenErr error
}

func (g *recordingGateway) open(cfg *config.Config) (gatewayRuntime, error) {
	g.cfg = cfg
	g.tokenErr = localauth.ErrPathRequired
	if store, err := localauth.NewStore(cfg.Security.LocalTokenPath); err == nil {
		_, g.tokenErr = store.Peek(context.Background())
	}
	return g, nil
}

func (g *recordingGateway) Run(context.Context) error { return nil }

type failingOutput struct{ err error }

func (o failingOutput) Write([]byte) (int, error) { return 0, o.err }
