package config

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"

	"github.com/joho/godotenv"
	"github.com/sethvargo/go-envconfig"

	"github.com/agentstation/starport/internal/credentials"
	"github.com/agentstation/starport/internal/credentials/cloudchain"
)

// Loader reads configuration without changing process state.
type Loader struct {
	envFiles     []string
	prefix       string
	environment  envconfig.Lookuper
	resolvePaths func() (Paths, error)
}

type loadFailure struct {
	message string
	cause   error
}

func (e *loadFailure) Error() string { return e.message }

func (e *loadFailure) Unwrap() error { return e.cause }

// OperatorError returns an error that is safe to show without configured
// values. It preserves the original error for programmatic inspection.
func OperatorError(err error) error {
	if err == nil {
		return nil
	}
	var failure *loadFailure
	if errors.As(err, &failure) {
		return &loadFailure{message: failure.message, cause: err}
	}
	return &loadFailure{message: "configuration could not be loaded", cause: err}
}

func newLoadFailure(message string, cause error) error {
	return &loadFailure{message: message, cause: cause}
}

// NewLoader creates a loader for the process environment and platform paths.
func NewLoader() *Loader {
	return &Loader{
		prefix:       "STARPORT_",
		environment:  envconfig.OsLookuper(),
		resolvePaths: PlatformPaths,
	}
}

// WithEnvFiles sets environment files in descending precedence order.
// An empty list disables file loading.
func (l *Loader) WithEnvFiles(files ...string) *Loader {
	l.envFiles = make([]string, len(files))
	copy(l.envFiles, files)
	return l
}

// WithEnvironment replaces the process environment source.
func (l *Loader) WithEnvironment(values map[string]string) *Loader {
	copyValues := make(map[string]string, len(values))
	for key, value := range values {
		copyValues[key] = value
	}
	l.environment = envconfig.MapLookuper(copyValues)
	return l
}

// WithPaths replaces platform path resolution.
func (l *Loader) WithPaths(paths Paths) *Loader {
	l.resolvePaths = func() (Paths, error) { return paths, nil }
	return l
}

// Override is one decision a caller made outside the environment, applied
// after configuration sources are read and before validation runs. A command
// line flag is the reason it exists: a flag has to meet exactly the same
// validation an environment value meets, or the checks that read it prove
// nothing about the flag.
type Override func(*Config)

// DisableAuthentication turns off the gateway API key check. It carries the
// same weight as STARPORT_SECURITY_AUTH_MODE=disabled, including the exposure
// tripwire that refuses a non-loopback bind address.
// It also records that a flag, not the environment, decided: the two write the
// same field, and a mode an operator stored from the console yields to either,
// so startup has to be able to name which one it is honoring.
func DisableAuthentication() Override {
	return func(cfg *Config) {
		cfg.Security.AuthMode = AuthModeDisabled
		cfg.authModeFromFlag = true
	}
}

// AllowRemoteWithoutAuthentication acknowledges that an unauthenticated
// gateway may bind an address the network can reach. Alone it changes nothing;
// it only lifts the tripwire that DisableAuthentication would otherwise trip.
func AllowRemoteWithoutAuthentication() Override {
	return func(cfg *Config) { cfg.Security.AllowRemoteNoAuth = true }
}

// Load resolves configuration sources, applies defaults and any overrides, and
// validates the result.
func (l *Loader) Load(ctx context.Context, overrides ...Override) (*Config, error) {
	return l.load(ctx, nil, overrides)
}

// LoadDevelopment reads process settings, applies the guarded development
// runtime contract and any overrides, and validates the result.
func (l *Loader) LoadDevelopment(ctx context.Context, overrides ...Override) (*Config, error) {
	return l.load(ctx, func(cfg *Config) { cfg.ConfigureDevelopmentRuntime() }, overrides)
}

func (l *Loader) load(ctx context.Context, prepare func(*Config), overrides []Override) (*Config, error) {
	paths, err := l.resolvePaths()
	if err != nil {
		return nil, newLoadFailure("configuration paths could not be resolved", err)
	}

	lookuper, err := l.sourceLookuper(paths)
	if err != nil {
		return nil, newLoadFailure("configuration sources could not be read", err)
	}

	// A removed setting fails startup before anything reads a value. A
	// deployment that still sets one believes it still applies, so silence
	// would hide a routing change instead of reporting it.
	if err := checkRemovedSettings(lookuper); err != nil {
		// The message names variables only, never a configured value, so
		// it stays safe for the operator-facing error.
		return nil, newLoadFailure(err.Error(), err)
	}

	cfg := defaultConfig(paths)
	if err := envconfig.ProcessWith(ctx, &envconfig.Config{
		Target:   cfg,
		Lookuper: envconfig.PrefixLookuper(l.prefix, lookuper),
	}); err != nil {
		return nil, newLoadFailure("configuration values could not be decoded", err)
	}
	if cfg.CredentialSources.RemoteRefreshInterval == 0 {
		cfg.CredentialSources.RemoteRefreshInterval = credentials.DefaultDirectSecretRefreshInterval
	}
	// The OTLP endpoint keeps its standard unprefixed names, because they are
	// the cross-vendor contract every collector documents. The specific
	// traces variable beats the general one, matching the OpenTelemetry
	// specification.
	if endpoint, ok := lookuper.Lookup("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT"); ok && endpoint != "" {
		cfg.Telemetry.TracesEndpoint = endpoint
	} else if endpoint, ok := lookuper.Lookup("OTEL_EXPORTER_OTLP_ENDPOINT"); ok && endpoint != "" {
		cfg.Telemetry.TracesEndpoint = endpoint
	}
	if prepare != nil {
		prepare(cfg)
	}
	// Overrides land last so an explicit flag beats both the environment and
	// the development contract, and first so validation still judges them.
	for _, override := range overrides {
		if override != nil {
			override(cfg)
		}
	}

	if err := resolveConfiguredPaths(cfg, paths); err != nil {
		return nil, newLoadFailure("configured paths could not be resolved", err)
	}
	if err := cfg.Validate(); err != nil {
		return nil, newLoadFailure("configuration values are invalid", err)
	}
	cfg.providerEnvironment = lookuper
	resolverOptions := []credentials.ResolverOption{
		credentials.WithEnvironmentLookup(lookuper.Lookup),
		credentials.WithDirectSecretRefreshInterval(cfg.CredentialSources.RemoteRefreshInterval),
	}
	for primitive, chain := range cloudchain.DefaultCloudChains() {
		resolverOptions = append(resolverOptions, credentials.WithCloudChain(primitive, chain))
	}
	cfg.credentialResolver = credentials.NewResolver(resolverOptions...)

	return cfg, nil
}

func defaultConfig(paths Paths) *Config {
	return &Config{
		Storage: StorageConfig{
			Badger: BadgerConfig{Path: paths.BadgerDir},
			SQL:    SQLConfig{SQLite: SQLiteConfig{Path: paths.SQLiteFile}},
		},
		Files:    FilesConfig{Path: paths.FilesDir},
		Security: SecurityConfig{LocalTokenPath: paths.LocalTokenFile},
	}
}

func (l *Loader) sourceLookuper(paths Paths) (envconfig.Lookuper, error) {
	files := l.envFiles
	if files == nil {
		files = []string{paths.ConfigFile}
	}

	lookupers := []envconfig.Lookuper{l.environment}
	for _, file := range files {
		values, err := godotenv.Read(file)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return nil, fmt.Errorf("read environment file %q: %w", file, err)
		}
		lookupers = append(lookupers, envconfig.MapLookuper(values))
	}
	return envconfig.MultiLookuper(lookupers...), nil
}

func resolveConfiguredPaths(cfg *Config, paths Paths) error {
	var err error
	if cfg.Storage.Mode == storageModeBadger {
		cfg.Storage.Badger.Path, err = resolvePath(paths.ConfigDir, cfg.Storage.Badger.Path)
		if err != nil {
			return fmt.Errorf("badger path: %w", err)
		}
	}
	if cfg.Files.SelectedBackend() == BlobBackendFilesystem {
		if cfg.Files.Path == "" {
			cfg.Files.Path = paths.FilesDir
		}
		cfg.Files.Path, err = resolvePath(paths.ConfigDir, cfg.Files.Path)
		if err != nil {
			return fmt.Errorf("files path: %w", err)
		}
	}
	cfg.Catalog.WorkspacePath, err = resolvePath(paths.ConfigDir, cfg.Catalog.WorkspacePath)
	if err != nil {
		return fmt.Errorf("catalog workspace path: %w", err)
	}
	cfg.Security.TLSCertPath, err = resolvePath(paths.ConfigDir, cfg.Security.TLSCertPath)
	if err != nil {
		return fmt.Errorf("TLS certificate path: %w", err)
	}
	cfg.Security.TLSKeyPath, err = resolvePath(paths.ConfigDir, cfg.Security.TLSKeyPath)
	if err != nil {
		return fmt.Errorf("TLS key path: %w", err)
	}
	cfg.Logging.FilePath, err = resolvePath(paths.ConfigDir, cfg.Logging.FilePath)
	if err != nil {
		return fmt.Errorf("log file path: %w", err)
	}
	return nil
}

func resolvePath(base, value string) (string, error) {
	if value == "" || filepath.IsAbs(value) {
		return value, nil
	}
	if base == "" {
		return "", fmt.Errorf("base directory is empty")
	}
	return filepath.Join(base, value), nil
}

// LoadWithDefaults loads configuration from the standard sources.
func LoadWithDefaults(ctx context.Context, overrides ...Override) (*Config, error) {
	return NewLoader().Load(ctx, overrides...)
}

// LoadDevelopment loads process environment settings without a configuration
// file and applies the guarded development runtime contract.
func LoadDevelopment(ctx context.Context, overrides ...Override) (*Config, error) {
	return NewLoader().WithEnvFiles().LoadDevelopment(ctx, overrides...)
}
