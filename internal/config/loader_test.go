package config

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/agentstation/starmap/pkg/catalogs"
)

func TestLoaderDefersProviderEnvironmentUntilCatalogResolution(t *testing.T) {
	environment := map[string]string{
		"GOOGLE_API_KEY":          "studio-key",
		"AZURE_OPENAI_ENDPOINT":   "https://azure.example",
		"AZURE_OPENAI_API_KEY":    "azure-key",
		"FIREWORKS_API_KEY":       "fireworks-key",
		"STARPORT_OPENAI_API_KEY": "openai-product-key",
	}
	cfg := loadTestConfig(t, environment)
	if len(cfg.Providers) != 0 {
		t.Fatalf("provider values were read before catalog resolution: %#v", cfg.Providers)
	}
	resolveTestProviders(t, cfg)

	assertProviderMaterialValue(t, cfg, catalogs.ProviderIDGoogleAIStudio, "api-key", "studio-key")
	assertProviderMaterialValue(t, cfg, catalogs.ProviderIDAzureOpenAI, "api-key", "azure-key")
	assertProviderMaterialValue(t, cfg, "fireworks-ai", "api-key", "fireworks-key")
	assertProviderMaterialValue(t, cfg, catalogs.ProviderIDOpenAI, "api-key", "openai-product-key")
}

func TestLoaderIgnoresRemovedProviderEnvironmentNamespaces(t *testing.T) {
	environment := map[string]string{
		"STARPORT_PROVIDERS_GOOGLE_AISTUDIO_API_KEY": "old-studio-key",
		"STARPORT_PROVIDERS_GOOGLE_VERTEXAI_API_KEY": "old-vertex-token",
		"STARPORT_PROVIDERS_AZURE_API_KEY":           "old-azure-key",
	}
	cfg := loadTestConfig(t, environment)
	resolveTestProviders(t, cfg)

	if len(cfg.Providers) != 0 {
		t.Fatalf("removed provider namespaces were accepted: %#v", cfg.Providers)
	}
}

func TestLoaderSecurePlatformDefaults(t *testing.T) {
	paths := PathsForConfigDir(t.TempDir())
	cfg, err := NewLoader().
		WithPaths(paths).
		WithEnvironment(nil).
		WithEnvFiles().
		Load(context.Background())
	if err != nil {
		t.Fatalf("load defaults: %v", err)
	}

	if cfg.Server.Port != 8080 {
		t.Errorf("default port = %d, want 8080", cfg.Server.Port)
	}
	if cfg.Server.Host != "127.0.0.1" {
		t.Errorf("default host = %q, want 127.0.0.1", cfg.Server.Host)
	}
	if cfg.Security.EnableCORS {
		t.Error("CORS is enabled by default")
	}
	if cfg.Security.AllowedOrigins != "" {
		t.Errorf("default allowed origins = %q, want empty", cfg.Security.AllowedOrigins)
	}
	if cfg.Storage.Badger.Path != paths.BadgerDir {
		t.Errorf("default badger path = %q, want %q", cfg.Storage.Badger.Path, paths.BadgerDir)
	}
	if cfg.RateLimiting.ConfigPath != paths.RateLimitsFile {
		t.Errorf("default rate-limit path = %q, want %q", cfg.RateLimiting.ConfigPath, paths.RateLimitsFile)
	}
	if cfg.RateLimiting.EnableHotReload {
		t.Error("rate-limit hot reload is enabled without an explicit configuration")
	}
}

func TestLoaderEnvironmentOverridesFiles(t *testing.T) {
	dir := t.TempDir()
	highPriorityFile := writeEnvFile(t, dir, "local.env", `STARPORT_SERVER_PORT=7777`)
	lowPriorityFile := writeEnvFile(t, dir, "base.env", `STARPORT_SERVER_PORT=8888
STARPORT_LOGGING_LEVEL=warn`)

	cfg, err := NewLoader().
		WithPaths(PathsForConfigDir(dir)).
		WithEnvironment(map[string]string{"STARPORT_SERVER_PORT": "9999"}).
		WithEnvFiles(highPriorityFile, lowPriorityFile).
		Load(context.Background())
	if err != nil {
		t.Fatalf("load ordered sources: %v", err)
	}
	if cfg.Server.Port != 9999 {
		t.Errorf("server port = %d, want environment value 9999", cfg.Server.Port)
	}
	if cfg.Logging.Level != "warn" {
		t.Errorf("logging level = %q, want low-priority file value warn", cfg.Logging.Level)
	}
}

func TestLoaderUsesFirstEnvironmentFileValue(t *testing.T) {
	dir := t.TempDir()
	first := writeEnvFile(t, dir, "first.env", `STARPORT_SERVER_PORT=7777`)
	second := writeEnvFile(t, dir, "second.env", `STARPORT_SERVER_PORT=8888`)

	cfg, err := NewLoader().
		WithPaths(PathsForConfigDir(dir)).
		WithEnvironment(nil).
		WithEnvFiles(first, second).
		Load(context.Background())
	if err != nil {
		t.Fatalf("load ordered files: %v", err)
	}
	if cfg.Server.Port != 7777 {
		t.Errorf("server port = %d, want first file value 7777", cfg.Server.Port)
	}
}

func TestLoaderUsesPlatformConfigFile(t *testing.T) {
	paths := PathsForConfigDir(t.TempDir())
	if err := os.WriteFile(paths.ConfigFile, []byte("STARPORT_SERVER_PORT=7070\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := NewLoader().WithPaths(paths).WithEnvironment(nil).Load(context.Background())
	if err != nil {
		t.Fatalf("load platform file: %v", err)
	}
	if cfg.Server.Port != 7070 {
		t.Errorf("server port = %d, want platform file value 7070", cfg.Server.Port)
	}
}

func TestLoaderRejectsInvalidEnvironmentValue(t *testing.T) {
	_, err := NewLoader().
		WithPaths(PathsForConfigDir(t.TempDir())).
		WithEnvironment(map[string]string{"STARPORT_SERVER_PORT": "invalid"}).
		WithEnvFiles().
		Load(context.Background())
	if err == nil {
		t.Fatal("invalid server port was accepted")
	}
}

func TestLoaderErrorsDoNotExposeConfigurationValues(t *testing.T) {
	dir := t.TempDir()
	secret := "loader-secret-that-must-not-appear"
	file := writeEnvFile(
		t,
		dir,
		"invalid.env",
		"OPENAI_API_KEY='"+secret,
	)
	_, err := NewLoader().
		WithPaths(PathsForConfigDir(dir)).
		WithEnvironment(nil).
		WithEnvFiles(file).
		Load(context.Background())
	if err == nil {
		t.Fatal("malformed environment file was accepted")
	}
	if strings.Contains(err.Error(), secret) {
		t.Errorf("loader error contains configured secret: %q", err)
	}
	if err.Error() != "configuration sources could not be read" {
		t.Errorf("loader error = %q", err)
	}
}

func TestOperatorErrorDoesNotTrustExternalErrors(t *testing.T) {
	secret := "external-secret-that-must-not-appear"
	err := OperatorError(fmt.Errorf("dependency included %s", secret))
	if strings.Contains(err.Error(), secret) {
		t.Errorf("operator error contains dependency secret: %q", err)
	}
	if err.Error() != "configuration could not be loaded" {
		t.Errorf("operator error = %q", err)
	}
}

func TestLoaderResolvesRelativePathsFromConfigDirectory(t *testing.T) {
	paths := PathsForConfigDir(t.TempDir())
	environment := map[string]string{
		"STARPORT_STORAGE_BADGER_PATH":             "state",
		"STARPORT_CATALOG_WORKSPACE_PATH":          "catalog",
		"STARPORT_RATE_LIMITING_CONFIG_PATH":       "rate-limits.yaml",
		"STARPORT_SECURITY_TLS_CERT_PATH":          "tls/cert.pem",
		"STARPORT_SECURITY_TLS_KEY_PATH":           "tls/key.pem",
		"STARPORT_LOGGING_OUTPUT":                  "file",
		"STARPORT_LOGGING_FILE_PATH":               "logs/starport.log",
		"STARPORT_SECURITY_MASTER_KEY":             strings.Repeat("m", 32),
		"OPENAI_API_KEY":                           "provider-key",
		"STARPORT_RATE_LIMITING_ENABLE_HOT_RELOAD": "false",
	}
	cfg, err := NewLoader().
		WithPaths(paths).
		WithEnvironment(environment).
		WithEnvFiles().
		Load(context.Background())
	if err != nil {
		t.Fatalf("load relative paths: %v", err)
	}

	wants := map[string]string{
		"badger":  filepath.Join(paths.ConfigDir, "state"),
		"catalog": filepath.Join(paths.ConfigDir, "catalog"),
		"rates":   filepath.Join(paths.ConfigDir, "rate-limits.yaml"),
		"cert":    filepath.Join(paths.ConfigDir, "tls", "cert.pem"),
		"key":     filepath.Join(paths.ConfigDir, "tls", "key.pem"),
		"log":     filepath.Join(paths.ConfigDir, "logs", "starport.log"),
	}
	got := map[string]string{
		"badger":  cfg.Storage.Badger.Path,
		"catalog": cfg.Catalog.WorkspacePath,
		"rates":   cfg.RateLimiting.ConfigPath,
		"cert":    cfg.Security.TLSCertPath,
		"key":     cfg.Security.TLSKeyPath,
		"log":     cfg.Logging.FilePath,
	}
	for name, want := range wants {
		if got[name] != want {
			t.Errorf("%s path = %q, want %q", name, got[name], want)
		}
	}
}

func resolveTestProviders(t *testing.T, cfg *Config) {
	t.Helper()
	builder, err := catalogs.NewEmbedded()
	if err != nil {
		t.Fatalf("open embedded catalog: %v", err)
	}
	catalog, err := builder.Build()
	if err != nil {
		t.Fatalf("build embedded catalog: %v", err)
	}
	if err := cfg.ResolveProviders(context.Background(), catalog.Providers()); err != nil {
		t.Fatalf("resolve providers: %v", err)
	}
}

func assertProviderMaterialValue(
	t *testing.T,
	cfg *Config,
	providerID catalogs.ProviderID,
	fieldID catalogs.ProviderCredentialFieldID,
	want string,
) {
	t.Helper()
	provider, found := cfg.Providers[providerID]
	if !found {
		t.Fatalf("provider %s was not resolved", providerID)
	}
	got, found := provider.Material.Value(fieldID)
	if !found || got != want {
		t.Fatalf("provider %s field %s = %q, %t", providerID, fieldID, got, found)
	}
}

func TestLoaderDoesNotMutateEnvironment(t *testing.T) {
	const sentinel = "STARPORT_DX2_ENVIRONMENT_MUTATION_SENTINEL"
	envFile := writeEnvFile(t, t.TempDir(), "config.env", sentinel+"=unexpected")

	before := append([]string(nil), os.Environ()...)
	slices.Sort(before)
	if _, err := NewLoader().
		WithPaths(PathsForConfigDir(t.TempDir())).
		WithEnvironment(nil).
		WithEnvFiles(envFile).
		Load(context.Background()); err != nil {
		t.Fatalf("load config: %v", err)
	}
	after := append([]string(nil), os.Environ()...)
	slices.Sort(after)
	if !slices.Equal(before, after) {
		t.Fatal("configuration loading changed the process environment")
	}
}

func loadTestConfig(t *testing.T, environment map[string]string) *Config {
	t.Helper()
	cfg, err := NewLoader().
		WithPaths(PathsForConfigDir(t.TempDir())).
		WithEnvironment(environment).
		WithEnvFiles().
		Load(context.Background())
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	return cfg
}

func writeEnvFile(t *testing.T, dir, name, contents string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(contents+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
