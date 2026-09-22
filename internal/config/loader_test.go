package config

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	starmap "github.com/agentstation/starmap"
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
	if cfg.CredentialSources.RemoteRefreshInterval != 5*time.Minute {
		t.Errorf(
			"credential source refresh interval = %s, want 5m",
			cfg.CredentialSources.RemoteRefreshInterval,
		)
	}
	if cfg.CredentialSources.ReconcileInterval != time.Minute {
		t.Errorf(
			"credential reconcile interval = %s, want 1m",
			cfg.CredentialSources.ReconcileInterval,
		)
	}
	if cfg.CredentialSources.ReconcileTimeout != 10*time.Second {
		t.Errorf(
			"credential reconcile timeout = %s, want 10s",
			cfg.CredentialSources.ReconcileTimeout,
		)
	}
}

// TestBodyLimitDefaultMatchesItsConstant closes the gap a struct tag opens. A
// tag holds a literal, so the default the loader applies and the constant the
// rest of the gateway reads are two statements of one number, and a change to
// either alone leaves a deployment reading a limit no other code agrees with.
func TestBodyLimitDefaultMatchesItsConstant(t *testing.T) {
	cfg, err := NewLoader().
		WithPaths(PathsForConfigDir(t.TempDir())).
		WithEnvironment(nil).
		WithEnvFiles().
		Load(context.Background())
	if err != nil {
		t.Fatalf("load defaults: %v", err)
	}
	if cfg.Server.MaxRequestSize != DefaultMaxRequestSize {
		t.Fatalf(
			"default max request size = %d, want %d",
			cfg.Server.MaxRequestSize, DefaultMaxRequestSize,
		)
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

func TestLoaderRejectsNegativeDirectSecretRefreshInterval(t *testing.T) {
	_, err := NewLoader().
		WithPaths(PathsForConfigDir(t.TempDir())).
		WithEnvironment(map[string]string{
			"STARPORT_CREDENTIAL_SOURCES_REMOTE_REFRESH_INTERVAL": "-1s",
		}).
		WithEnvFiles().
		Load(t.Context())
	if err == nil {
		t.Fatal("negative direct secret refresh interval was accepted")
	}
}

func TestLoaderAppliesDirectSecretRefreshInterval(t *testing.T) {
	cfg, err := NewLoader().
		WithPaths(PathsForConfigDir(t.TempDir())).
		WithEnvironment(map[string]string{
			"STARPORT_CREDENTIAL_SOURCES_REMOTE_REFRESH_INTERVAL": "9m",
		}).
		WithEnvFiles().
		Load(t.Context())
	if err != nil {
		t.Fatalf("load direct secret refresh interval: %v", err)
	}
	if cfg.CredentialSources.RemoteRefreshInterval != 9*time.Minute {
		t.Fatalf(
			"direct secret refresh interval = %s, want 9m",
			cfg.CredentialSources.RemoteRefreshInterval,
		)
	}
}

func TestLoaderAppliesProviderReconcileLifecycle(t *testing.T) {
	cfg, err := NewLoader().
		WithPaths(PathsForConfigDir(t.TempDir())).
		WithEnvironment(map[string]string{
			"STARPORT_CREDENTIAL_SOURCES_RECONCILE_INTERVAL": "2m",
			"STARPORT_CREDENTIAL_SOURCES_RECONCILE_TIMEOUT":  "15s",
		}).
		WithEnvFiles().
		Load(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if cfg.CredentialSources.ReconcileInterval != 2*time.Minute ||
		cfg.CredentialSources.ReconcileTimeout != 15*time.Second {
		t.Fatalf("provider reconcile lifecycle = %#v", cfg.CredentialSources)
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
		"cert":    filepath.Join(paths.ConfigDir, "tls", "cert.pem"),
		"key":     filepath.Join(paths.ConfigDir, "tls", "key.pem"),
		"log":     filepath.Join(paths.ConfigDir, "logs", "starport.log"),
	}
	got := map[string]string{
		"badger":  cfg.Storage.Badger.Path,
		"catalog": cfg.Catalog.WorkspacePath,
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
	builder, err := starmap.EmbeddedBuilder()
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

func TestDevelopmentLoaderUsesProcessSettingsAndGuardedRuntime(t *testing.T) {
	paths := PathsForConfigDir(t.TempDir())
	writeEnvFile(
		t,
		paths.ConfigDir,
		filepath.Base(paths.ConfigFile),
		"STARPORT_SERVER_PORT=19001\nOPENAI_API_KEY=file-secret",
	)
	loader := NewLoader().
		WithPaths(paths).
		WithEnvironment(map[string]string{
			"OPENAI_API_KEY":                           "process-secret",
			"STARPORT_SERVER_HOST":                     "0.0.0.0",
			"STARPORT_SERVER_PORT":                     "18994",
			"STARPORT_STORAGE_MODE":                    "valkey",
			"STARPORT_SECURITY_MASTER_KEY":             "short",
			"STARPORT_SECURITY_ENABLE_TLS":             "true",
			"STARPORT_LOGGING_OUTPUT":                  "file",
			"STARPORT_RATE_LIMITING_ENABLE_HOT_RELOAD": "true",
		}).
		WithEnvFiles()

	cfg, err := loader.LoadDevelopment(t.Context())
	if err != nil {
		t.Fatalf("load development config: %v", err)
	}
	if cfg.Server.Host != "127.0.0.1" || cfg.Server.Port != 18994 {
		t.Fatalf("development server = %s:%d", cfg.Server.Host, cfg.Server.Port)
	}
	storageConfig := cfg.Storage.RuntimeStorage()
	if storageConfig.Type != "badger" || !storageConfig.Badger.InMemory || storageConfig.Badger.Path != "" {
		t.Fatalf("development storage = %#v", storageConfig)
	}
	if cfg.Security.MasterKey != "" || cfg.Security.EnableTLS || cfg.Security.EnableCORS {
		t.Fatalf("development security = %#v", cfg.Security)
	}
	if cfg.Logging.Output != "stdout" || cfg.Logging.FilePath != "" {
		t.Fatalf("development local settings were not guarded")
	}
	credential, found := cfg.providerEnvironment.Lookup("OPENAI_API_KEY")
	if !found || credential != "process-secret" {
		t.Fatalf("development provider environment = %q, %t", credential, found)
	}
	if cfg.Catalog.StateDirectory != "" || !cfg.Catalog.StateDirectoryIsScratch() {
		t.Fatalf("development catalog state directory = %q, want scratch", cfg.Catalog.StateDirectory)
	}
}

// TestDevelopmentLoaderNeedsNoHomeDirectory proves that a development gateway
// loads with no home directory and no state root, because its catalog state
// is session scratch and never resolves against the user state root. A
// serving gateway in the same process refuses to load and names the settings.
func TestDevelopmentLoaderNeedsNoHomeDirectory(t *testing.T) {
	t.Setenv(stateHomeEnvironment, "")
	setHomeDirectory(t, "")
	loader := NewLoader().
		WithPaths(PathsForConfigDir(t.TempDir())).
		WithEnvironment(map[string]string{}).
		WithEnvFiles()

	cfg, err := loader.LoadDevelopment(t.Context())
	if err != nil {
		t.Fatalf("load development config without a home directory: %v", err)
	}
	if cfg.Catalog.StateDirectory != "" || !cfg.Catalog.StateDirectoryIsScratch() {
		t.Fatalf("development catalog state directory = %q, want scratch", cfg.Catalog.StateDirectory)
	}

	_, err = loader.Load(t.Context())
	if err == nil {
		t.Fatal("a serving gateway loaded without a home directory or a state root")
	}
	// The operator-facing message carries no value, so the cause names the
	// settings.
	cause := errors.Unwrap(err)
	if cause == nil || !strings.Contains(cause.Error(), stateDirectoryEnvironment) {
		t.Fatalf("serving load cause %v does not name %s", cause, stateDirectoryEnvironment)
	}
}

// TestDevelopmentLoaderKeepsAnOperatorStateDirectory proves that an operator
// value survives the development contract, so a session that names a
// directory retains its catalog state there.
func TestDevelopmentLoaderKeepsAnOperatorStateDirectory(t *testing.T) {
	stateDirectory := t.TempDir()
	loader := NewLoader().
		WithPaths(PathsForConfigDir(t.TempDir())).
		WithEnvironment(map[string]string{
			"STARPORT_CATALOG_STATE_DIR": stateDirectory,
		}).
		WithEnvFiles()

	cfg, err := loader.LoadDevelopment(t.Context())
	if err != nil {
		t.Fatalf("load development config: %v", err)
	}
	if cfg.Catalog.StateDirectory != stateDirectory || cfg.Catalog.StateDirectoryIsScratch() {
		t.Fatalf("development catalog state directory = %q, scratch %t", cfg.Catalog.StateDirectory, cfg.Catalog.StateDirectoryIsScratch())
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

func TestLoaderReadsStandardOTLPEndpoint(t *testing.T) {
	cfg := loadTestConfig(t, map[string]string{
		"OTEL_EXPORTER_OTLP_ENDPOINT": "http://collector.example:4318",
	})
	if cfg.Telemetry.TracesEndpoint != "http://collector.example:4318" {
		t.Errorf("TracesEndpoint = %q, want the general OTLP endpoint", cfg.Telemetry.TracesEndpoint)
	}

	cfg = loadTestConfig(t, map[string]string{
		"OTEL_EXPORTER_OTLP_ENDPOINT":        "http://general.example:4318",
		"OTEL_EXPORTER_OTLP_TRACES_ENDPOINT": "http://traces.example:4318/v1/traces",
	})
	if cfg.Telemetry.TracesEndpoint != "http://traces.example:4318/v1/traces" {
		t.Errorf("TracesEndpoint = %q, want the specific traces endpoint to win", cfg.Telemetry.TracesEndpoint)
	}

	cfg = loadTestConfig(t, nil)
	if cfg.Telemetry.TracesEndpoint != "" {
		t.Errorf("TracesEndpoint = %q, want empty without OTLP environment", cfg.Telemetry.TracesEndpoint)
	}
}
