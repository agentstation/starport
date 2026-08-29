package setup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/joho/godotenv"

	"github.com/agentstation/starport/internal/apikey"
	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/storage"
)

func TestInitializeCreatesNamedIdentity(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "provider-secret")
	paths := config.PathsForConfigDir(filepath.Join(t.TempDir(), "starport"))
	service := New(paths)

	result, err := service.Initialize(context.Background(), Request{
		IdentityName: "local-admin",
	})
	if err != nil {
		t.Fatalf("initialize: %v", err)
	}
	if result.APIKey == "" || result.IdentityName != "local-admin" {
		t.Fatalf("result = %#v", result)
	}
	state, err := Inspect(paths)
	if err != nil || state != StateReady {
		t.Fatalf("setup state = %q, error = %v", state, err)
	}

	info, err := os.Stat(paths.ConfigFile)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Errorf("configuration mode = %04o, want 0600", info.Mode().Perm())
	}
	values, err := godotenv.Read(paths.ConfigFile)
	if err != nil {
		t.Fatalf("read configuration: %v", err)
	}
	if _, exists := values["OPENAI_API_KEY"]; exists {
		t.Error("provider credential was persisted")
	}
	if len(values["STARPORT_SECURITY_MASTER_KEY"]) < 32 {
		t.Errorf("master key length = %d, want at least 32", len(values["STARPORT_SECURITY_MASTER_KEY"]))
	}
	for key := range values {
		if strings.Contains(key, "BOOTSTRAP") {
			t.Errorf("configuration contains obsolete setting %q", key)
		}
	}
	if strings.Contains(string(mustReadFile(t, paths.ConfigFile)), result.APIKey) {
		t.Fatal("configuration contains the plaintext gateway key")
	}

	store, err := openLocalStore(paths.BadgerDir)
	if err != nil {
		t.Fatalf("reopen identity store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	repository, err := apikey.Open(store)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte(result.APIKey))
	record, err := repository.GetByHash(context.Background(), hex.EncodeToString(digest[:]))
	if err != nil {
		t.Fatalf("get initialized identity: %v", err)
	}
	if record.APIKey.Name != "local-admin" || record.APIKey.Metadata["source"] != "setup" {
		t.Errorf("identity = %#v", record.APIKey)
	}
}

func TestLocalInitPersistsNoProviderCredential(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "provider-secret")
	t.Setenv("FIREWORKS_API_KEY", "other-provider-secret")
	t.Setenv("STARPORT_OPENAI_API_KEY", "starport-provider-secret")
	paths := config.PathsForConfigDir(filepath.Join(t.TempDir(), "starport"))
	_, err := New(paths).Initialize(context.Background(), Request{
		IdentityName: "local-admin",
	})
	if err != nil {
		t.Fatalf("initialize: %v", err)
	}
	values, err := godotenv.Read(paths.ConfigFile)
	if err != nil {
		t.Fatal(err)
	}
	if len(values) != 1 || values["STARPORT_SECURITY_MASTER_KEY"] == "" {
		t.Fatalf("local configuration keys = %#v", values)
	}
	contents := string(mustReadFile(t, paths.ConfigFile))
	for _, secret := range []string{
		"provider-secret", "other-provider-secret", "starport-provider-secret",
	} {
		if strings.Contains(contents, secret) {
			t.Fatal("local initialization persisted ambient provider material")
		}
	}
}

func TestInitializeRefusesExistingState(t *testing.T) {
	tests := []struct {
		name    string
		prepare func(t *testing.T, paths config.Paths)
		check   func(t *testing.T, paths config.Paths)
		want    error
	}{
		{
			name: "configuration", want: ErrPartialState,
			prepare: func(t *testing.T, paths config.Paths) {
				t.Helper()
				if err := os.MkdirAll(paths.ConfigDir, 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(paths.ConfigFile, []byte("sentinel"), 0o600); err != nil {
					t.Fatal(err)
				}
			},
			check: func(t *testing.T, paths config.Paths) {
				t.Helper()
				if got := string(mustReadFile(t, paths.ConfigFile)); got != "sentinel" {
					t.Errorf("configuration = %q, want sentinel", got)
				}
			},
		},
		{
			name: "identity store", want: ErrPartialState,
			prepare: func(t *testing.T, paths config.Paths) {
				t.Helper()
				if err := os.MkdirAll(paths.BadgerDir, 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(paths.BadgerDir, "sentinel"), []byte("keep"), 0o600); err != nil {
					t.Fatal(err)
				}
			},
			check: func(t *testing.T, paths config.Paths) {
				t.Helper()
				if got := string(mustReadFile(t, filepath.Join(paths.BadgerDir, "sentinel"))); got != "keep" {
					t.Errorf("identity sentinel = %q, want keep", got)
				}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			paths := config.PathsForConfigDir(filepath.Join(t.TempDir(), "starport"))
			test.prepare(t, paths)
			_, err := New(paths).Initialize(context.Background(), Request{
				IdentityName: "local-admin",
			})
			if !errors.Is(err, test.want) {
				t.Fatalf("initialize error = %v, want %v", err, test.want)
			}
			test.check(t, paths)
		})
	}
}

func TestInitializeRefusesReadyState(t *testing.T) {
	paths := config.PathsForConfigDir(filepath.Join(t.TempDir(), "starport"))
	service := New(paths)
	request := Request{IdentityName: "local-admin"}
	if _, err := service.Initialize(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	_, err := service.Initialize(context.Background(), request)
	if !errors.Is(err, ErrAlreadyInitialized) {
		t.Fatalf("second initialization error = %v, want %v", err, ErrAlreadyInitialized)
	}
}

func TestRollbackRemovesUnpublishedLocalState(t *testing.T) {
	paths := config.PathsForConfigDir(filepath.Join(t.TempDir(), "starport"))
	service := New(paths)
	request := Request{IdentityName: "local-admin"}
	result, err := service.Initialize(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Rollback(context.Background(), result); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	state, err := Inspect(paths)
	if err != nil || state != StateAbsent {
		t.Fatalf("state after rollback = %q, error = %v", state, err)
	}
	if _, err := service.Initialize(context.Background(), request); err != nil {
		t.Fatalf("retry after rollback: %v", err)
	}
}

func TestRollbackRefusesChangedConfiguration(t *testing.T) {
	paths := config.PathsForConfigDir(filepath.Join(t.TempDir(), "starport"))
	service := New(paths)
	result, err := service.Initialize(context.Background(), Request{
		IdentityName: "local-admin",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.ConfigFile, []byte("changed"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := service.Rollback(context.Background(), result); !errors.Is(err, ErrRollbackRefused) {
		t.Fatalf("rollback error = %v, want %v", err, ErrRollbackRefused)
	}
	if _, err := os.Stat(paths.ConfigDir); err != nil {
		t.Fatalf("configuration directory after refused rollback: %v", err)
	}
}

func TestRollbackRefusesChangedIdentityStorage(t *testing.T) {
	paths := config.PathsForConfigDir(filepath.Join(t.TempDir(), "starport"))
	service := New(paths)
	result, err := service.Initialize(context.Background(), Request{
		IdentityName: "local-admin",
	})
	if err != nil {
		t.Fatal(err)
	}
	store, err := openLocalStore(paths.BadgerDir)
	if err != nil {
		t.Fatal(err)
	}
	repository, err := apikey.Open(store)
	if err != nil {
		t.Fatal(err)
	}
	issuer, err := apikey.NewIssuer(repository)
	if err != nil {
		t.Fatal(err)
	}
	_, err = issuer.Issue(context.Background(), apikey.IssueRequest{
		Name: "second-admin", Scopes: []string{"*"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	if err := service.Rollback(context.Background(), result); !errors.Is(err, ErrRollbackRefused) {
		t.Fatalf("rollback error = %v, want %v", err, ErrRollbackRefused)
	}
	if _, err := os.Stat(paths.ConfigDir); err != nil {
		t.Fatalf("configuration directory after refused rollback: %v", err)
	}
}

func TestRollbackRefusesOtherApplicationRecords(t *testing.T) {
	paths := config.PathsForConfigDir(filepath.Join(t.TempDir(), "starport"))
	service := New(paths)
	result, err := service.Initialize(context.Background(), Request{
		IdentityName: "local-admin",
	})
	if err != nil {
		t.Fatal(err)
	}
	store, err := openLocalStore(paths.BadgerDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Set(context.Background(), "catalog:v1:current", []byte("state")); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	if err := service.Rollback(context.Background(), result); !errors.Is(err, ErrRollbackRefused) {
		t.Fatalf("rollback error = %v, want %v", err, ErrRollbackRefused)
	}
	if _, err := os.Stat(paths.ConfigDir); err != nil {
		t.Fatalf("configuration directory after refused rollback: %v", err)
	}
}

func TestRollbackRefusesOtherManagedFiles(t *testing.T) {
	paths := config.PathsForConfigDir(filepath.Join(t.TempDir(), "starport"))
	service := New(paths)
	result, err := service.Initialize(context.Background(), Request{
		IdentityName: "local-admin",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(paths.DataDir, "application-state"), []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := service.Rollback(context.Background(), result); !errors.Is(err, ErrRollbackRefused) {
		t.Fatalf("rollback error = %v, want %v", err, ErrRollbackRefused)
	}
	if got := string(mustReadFile(t, filepath.Join(paths.DataDir, "application-state"))); got != "keep" {
		t.Fatalf("application state = %q, want keep", got)
	}
}

func TestInitializeConcurrentSingleWinner(t *testing.T) {
	paths := config.PathsForConfigDir(filepath.Join(t.TempDir(), "starport"))
	request := Request{IdentityName: "local-admin"}
	errorsByCall := make([]error, 2)
	var wait sync.WaitGroup
	wait.Add(2)
	for index := range errorsByCall {
		go func() {
			defer wait.Done()
			_, errorsByCall[index] = New(paths).Initialize(context.Background(), request)
		}()
	}
	wait.Wait()

	successes := 0
	refusals := 0
	for _, err := range errorsByCall {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, ErrAlreadyInitialized), errors.Is(err, ErrPartialState):
			refusals++
		default:
			t.Fatalf("concurrent initialization error = %v", err)
		}
	}
	if successes != 1 || refusals != 1 {
		t.Fatalf("successes = %d, refusals = %d", successes, refusals)
	}
}

func TestInitializeValidatesBeforeWriting(t *testing.T) {
	paths := config.PathsForConfigDir(filepath.Join(t.TempDir(), "starport"))
	_, err := New(paths).Initialize(context.Background(), Request{IdentityName: "invalid name"})
	if !errors.Is(err, apikey.ErrInvalidName) {
		t.Fatalf("initialize error = %v, want %v", err, apikey.ErrInvalidName)
	}
	if _, statErr := os.Stat(paths.ConfigDir); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("configuration directory exists after validation failure: %v", statErr)
	}
}

func TestInitializeIdentityRefusesExistingIdentity(t *testing.T) {
	store := storage.NewMockStore()
	first, err := InitializeIdentity(context.Background(), store, "primary-admin")
	if err != nil {
		t.Fatalf("initialize identity: %v", err)
	}
	if first.Secret == "" || first.APIKey.Name != "primary-admin" {
		t.Errorf("issued identity = %#v", first)
	}
	_, err = InitializeIdentity(context.Background(), store, "replacement-admin")
	if !errors.Is(err, ErrAlreadyInitialized) {
		t.Fatalf("second initialization error = %v, want %v", err, ErrAlreadyInitialized)
	}
}

func TestReleaseIdentityAllowsConfiguredStorageRetry(t *testing.T) {
	store := storage.NewMockStore()
	first, err := InitializeIdentity(context.Background(), store, "primary-admin")
	if err != nil {
		t.Fatal(err)
	}
	if err := ReleaseIdentity(context.Background(), store, first.APIKey.ID); err != nil {
		t.Fatalf("release identity: %v", err)
	}
	if _, err := InitializeIdentity(context.Background(), store, "retry-admin"); err != nil {
		t.Fatalf("retry configured initialization: %v", err)
	}
}

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return contents
}
