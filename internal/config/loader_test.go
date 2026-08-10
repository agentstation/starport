package config

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestLoaderUsesExactProviderEnvironmentNamespaces(t *testing.T) {
	environment := map[string]string{
		"STARPORT_PROVIDERS_GOOGLE_AI_STUDIO_API_KEY": "studio-key",
		"STARPORT_PROVIDERS_GOOGLE_VERTEX_API_KEY":    "vertex-token",
		"STARPORT_PROVIDERS_GOOGLE_VERTEX_AUTH_MODE":  "static",
		"STARPORT_PROVIDERS_GOOGLE_VERTEX_PROJECT_ID": "vertex-project",
		"STARPORT_PROVIDERS_AZURE_OPENAI_API_KEY":     "azure-key",
		"STARPORT_PROVIDERS_AZURE_OPENAI_AUTH_MODE":   "static",
	}
	cfg := loadTestConfig(t, environment)

	if cfg.Providers.GoogleAIStudio.APIKey != "studio-key" {
		t.Fatalf("Google AI Studio API key = %q", cfg.Providers.GoogleAIStudio.APIKey)
	}
	if cfg.Providers.GoogleVertexAI.APIKey != "vertex-token" {
		t.Fatalf("Google Vertex token = %q", cfg.Providers.GoogleVertexAI.APIKey)
	}
	if cfg.Providers.GoogleVertexAI.ProjectID != "vertex-project" {
		t.Fatalf("Google Vertex project = %q", cfg.Providers.GoogleVertexAI.ProjectID)
	}
	if cfg.Providers.Azure.APIKey != "azure-key" {
		t.Fatalf("Azure OpenAI API key = %q", cfg.Providers.Azure.APIKey)
	}
}

func TestLoaderIgnoresOldProviderEnvironmentNamespaces(t *testing.T) {
	environment := map[string]string{
		"STARPORT_PROVIDERS_GOOGLE_AISTUDIO_API_KEY": "old-studio-key",
		"STARPORT_PROVIDERS_GOOGLE_VERTEXAI_API_KEY": "old-vertex-token",
		"STARPORT_PROVIDERS_AZURE_API_KEY":           "old-azure-key",
	}
	cfg := loadTestConfig(t, environment)

	if cfg.Providers.GoogleAIStudio.APIKey != "" {
		t.Fatal("old Google AI Studio namespace was accepted")
	}
	if cfg.Providers.GoogleVertexAI.APIKey != "" {
		t.Fatal("old Google Vertex namespace was accepted")
	}
	if cfg.Providers.Azure.APIKey != "" {
		t.Fatal("old Azure OpenAI namespace was accepted")
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
		"STARPORT_PROVIDERS_OPENAI_API_KEY='"+secret,
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
		"STARPORT_PROVIDERS_OPENAI_API_KEY":        "provider-key",
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
