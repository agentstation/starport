package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agentstation/starport/internal/apikey"
	starportcli "github.com/agentstation/starport/internal/cli"
	"github.com/agentstation/starport/internal/setup"
	"github.com/agentstation/starport/internal/storage"
)

func TestAuthBootstrapClaimsTheFirstKey(t *testing.T) {
	home, badger := sharedStorageEnvironment(t)

	code, stdout, stderr := runAuthBootstrap(t, "--name", "primary-admin")

	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr = %s", code, stderr)
	}
	if count := strings.Count(stdout, gatewayKeyLine); count != 1 {
		t.Errorf("gateway key lines = %d, want 1; stdout = %s", count, stdout)
	}
	keys := storedAPIKeys(t, badger)
	if len(keys) != 1 || keys[0].APIKey.Name != "primary-admin" {
		t.Fatalf("stored API keys = %#v, want one named primary-admin", keys)
	}
	// The operator supplies the master key, so shared storage gets no
	// configuration file.
	if _, err := os.Stat(filepath.Join(home, "config", "config.env")); !os.IsNotExist(err) {
		t.Errorf("configuration file after bootstrap: %v", err)
	}
}

func TestAuthBootstrapRefusesASecondKey(t *testing.T) {
	_, badger := sharedStorageEnvironment(t)
	if code, _, stderr := runAuthBootstrap(t, "--name", "primary-admin"); code != 0 {
		t.Fatalf("first bootstrap exit code = %d; stderr = %s", code, stderr)
	}

	code, stdout, stderr := runAuthBootstrap(t, "--name", "second-admin")

	if code != 1 || !strings.Contains(stderr, setup.ErrAlreadyInitialized.Error()) {
		t.Fatalf("second bootstrap exit code = %d, stderr = %q", code, stderr)
	}
	if strings.Contains(stdout, gatewayKeyLine) {
		t.Errorf("second bootstrap printed a key: %q", stdout)
	}
	keys := storedAPIKeys(t, badger)
	if len(keys) != 1 || keys[0].APIKey.Name != "primary-admin" {
		t.Fatalf("stored API keys = %#v, want only primary-admin", keys)
	}
}

// TestAuthBootstrapNameRefusesAFirstRunRoot keeps --name off a root that
// dev or serve initializes. A key there without the setup transaction would leave a
// partial root.
func TestAuthBootstrapNameRefusesAFirstRunRoot(t *testing.T) {
	home, _ := firstRunEnvironment(t)

	code, stdout, stderr := runAuthBootstrap(t, "--name", "primary-admin")

	if code != 1 || !strings.Contains(stderr, `"starport dev" or "starport serve"`) {
		t.Fatalf("exit code = %d, stderr = %q", code, stderr)
	}
	if strings.Contains(stdout, gatewayKeyLine) {
		t.Errorf("refused bootstrap printed a key: %q", stdout)
	}
	requireNoLocalRoot(t, home)
}

func TestAuthBootstrapRecoverRefusesCleanRoot(t *testing.T) {
	home, _ := firstRunEnvironment(t)

	code, stdout, stderr := runAuthBootstrap(t, "--recover")

	if code != 1 || !strings.Contains(stderr, setup.ErrNoPendingSetup.Error()) || !strings.Contains(stderr, `"starport dev" or "starport serve"`) {
		t.Fatalf("exit code = %d, stderr = %q", code, stderr)
	}
	if stdout != "" {
		t.Errorf("refused recovery wrote output: %q", stdout)
	}
	requireNoLocalRoot(t, home)
}

// requireNoLocalRoot proves that a refused command created no configuration
// or data root under home.
func requireNoLocalRoot(t *testing.T, home string) {
	t.Helper()
	for _, root := range []string{"config", "data"} {
		if _, err := os.Stat(filepath.Join(home, root)); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s root after a refused command: %v", root, err)
		}
	}
}

// sharedStorageEnvironment gives an otherwise empty home the shape of a
// shared deployment: an operator-supplied master key and a configured Badger
// path. It returns the home and the Badger path.
func sharedStorageEnvironment(t *testing.T) (string, string) {
	t.Helper()
	home, _ := firstRunEnvironment(t)
	badger := filepath.Join(home, "shared", "badger")
	t.Setenv("STARPORT_SECURITY_MASTER_KEY", strings.Repeat("m", 32))
	t.Setenv("STARPORT_STORAGE_BADGER_PATH", badger)
	return home, badger
}

// runAuthBootstrap runs the composed auth bootstrap command and returns the
// exit code, the standard output, and the standard error.
func runAuthBootstrap(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	code := runContext(t.Context(), append([]string{"starport", "auth", "bootstrap"}, args...),
		bytes.NewReader(nil), stdout, stderr,
		func(context.Context, starportcli.GatewayOptions, starportcli.ServerOutput) error { return nil },
		noopDevelopmentStarter, runInitializer)
	return code, stdout.String(), stderr.String()
}

func storedAPIKeys(t *testing.T, badger string) []apikey.Record {
	t.Helper()
	store, err := storage.OpenBadger(storage.BadgerConfig{
		Path: badger, SyncWrites: true, NumVersions: 1, NumLevelZero: 5, MemTableSize: 64 << 20,
	})
	if err != nil {
		t.Fatalf("open the Badger store: %v", err)
	}
	defer func() {
		if err := store.Close(); err != nil {
			t.Errorf("close the Badger store: %v", err)
		}
	}()
	repository, err := apikey.Open(store)
	if err != nil {
		t.Fatalf("open the API key repository: %v", err)
	}
	records, err := repository.List(t.Context(), 10, 0)
	if err != nil {
		t.Fatalf("list API keys: %v", err)
	}
	return records
}
