package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agentstation/starport/internal/apikey"
	starportcli "github.com/agentstation/starport/internal/cli"
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
