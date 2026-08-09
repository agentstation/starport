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

	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/joho/godotenv"

	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/identity"
	"github.com/agentstation/starport/internal/storage"
)

func TestInitializeCreatesNamedIdentity(t *testing.T) {
	paths := config.PathsForConfigDir(filepath.Join(t.TempDir(), "starport"))
	service := New(paths)

	result, err := service.Initialize(context.Background(), Request{
		Provider: catalogs.ProviderIDOpenAI, ProviderCredential: "provider-secret",
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
	if values["STARPORT_PROVIDERS_OPENAI_API_KEY"] != "provider-secret" {
		t.Errorf("provider credential was not preserved")
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
	repository, err := identity.Open(store)
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

func TestInitializeOllamaProfile(t *testing.T) {
	paths := config.PathsForConfigDir(filepath.Join(t.TempDir(), "starport"))
	result, err := New(paths).Initialize(context.Background(), Request{
		Provider: catalogs.ProviderIDOllama, IdentityName: "ollama-admin",
	})
	if err != nil {
		t.Fatalf("initialize Ollama: %v", err)
	}
	values, err := godotenv.Read(paths.ConfigFile)
	if err != nil {
		t.Fatal(err)
	}
	if values["STARPORT_PROVIDERS_OLLAMA_ENABLED"] != "true" {
		t.Errorf("Ollama setting = %q", values["STARPORT_PROVIDERS_OLLAMA_ENABLED"])
	}
	if _, ok := values["STARPORT_PROVIDERS_OPENAI_API_KEY"]; ok {
		t.Fatal("Ollama profile wrote an OpenAI credential")
	}
	if result.Provider != catalogs.ProviderIDOllama {
		t.Errorf("provider = %q", result.Provider)
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
				Provider: catalogs.ProviderIDOllama, IdentityName: "local-admin",
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
	request := Request{Provider: catalogs.ProviderIDOllama, IdentityName: "local-admin"}
	if _, err := service.Initialize(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	_, err := service.Initialize(context.Background(), request)
	if !errors.Is(err, ErrAlreadyInitialized) {
		t.Fatalf("second initialization error = %v, want %v", err, ErrAlreadyInitialized)
	}
}

func TestInitializeConcurrentSingleWinner(t *testing.T) {
	paths := config.PathsForConfigDir(filepath.Join(t.TempDir(), "starport"))
	request := Request{Provider: catalogs.ProviderIDOllama, IdentityName: "local-admin"}
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
	tests := []struct {
		name    string
		request Request
		want    error
	}{
		{name: "provider", request: Request{IdentityName: "local-admin"}, want: ErrProviderRequired},
		{
			name:    "unsupported provider",
			request: Request{Provider: catalogs.ProviderID("unknown"), IdentityName: "local-admin"},
			want:    ErrUnsupportedProvider,
		},
		{
			name:    "OpenAI credential",
			request: Request{Provider: catalogs.ProviderIDOpenAI, IdentityName: "local-admin"},
			want:    ErrProviderCredentialRequired,
		},
		{
			name:    "identity name",
			request: Request{Provider: catalogs.ProviderIDOllama, IdentityName: "invalid name"},
			want:    identity.ErrInvalidName,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			paths := config.PathsForConfigDir(filepath.Join(t.TempDir(), "starport"))
			_, err := New(paths).Initialize(context.Background(), test.request)
			if !errors.Is(err, test.want) {
				t.Fatalf("initialize error = %v, want %v", err, test.want)
			}
			if _, statErr := os.Stat(paths.ConfigDir); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("configuration directory exists after validation failure: %v", statErr)
			}
		})
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

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return contents
}
