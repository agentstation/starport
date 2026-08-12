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

// WithPrefix sets the environment variable prefix.
func (l *Loader) WithPrefix(prefix string) *Loader {
	l.prefix = prefix
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

// Load resolves configuration sources, applies defaults, and validates the result.
func (l *Loader) Load(ctx context.Context) (*Config, error) {
	return l.load(ctx, nil)
}

// LoadDevelopment reads process settings, applies the guarded development
// runtime contract, and validates the result.
func (l *Loader) LoadDevelopment(ctx context.Context) (*Config, error) {
	return l.load(ctx, func(cfg *Config) { cfg.ConfigureDevelopmentRuntime() })
}

func (l *Loader) load(ctx context.Context, prepare func(*Config)) (*Config, error) {
	paths, err := l.resolvePaths()
	if err != nil {
		return nil, newLoadFailure("configuration paths could not be resolved", err)
	}

	lookuper, err := l.sourceLookuper(paths)
	if err != nil {
		return nil, newLoadFailure("configuration sources could not be read", err)
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
	if prepare != nil {
		prepare(cfg)
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
		},
		RateLimiting: RateLimitingConfig{ConfigPath: paths.RateLimitsFile},
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
	cfg.Catalog.WorkspacePath, err = resolvePath(paths.ConfigDir, cfg.Catalog.WorkspacePath)
	if err != nil {
		return fmt.Errorf("catalog workspace path: %w", err)
	}
	cfg.RateLimiting.ConfigPath, err = resolvePath(paths.ConfigDir, cfg.RateLimiting.ConfigPath)
	if err != nil {
		return fmt.Errorf("rate-limit configuration path: %w", err)
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
func LoadWithDefaults(ctx context.Context) (*Config, error) {
	return NewLoader().Load(ctx)
}

// LoadDevelopment loads process environment settings without a configuration
// file and applies the guarded development runtime contract.
func LoadDevelopment(ctx context.Context) (*Config, error) {
	return NewLoader().WithEnvFiles().LoadDevelopment(ctx)
}

// MustLoad loads configuration and panics if loading fails.
func MustLoad(ctx context.Context) *Config {
	cfg, err := LoadWithDefaults(ctx)
	if err != nil {
		panic(fmt.Sprintf("load configuration: %v", err))
	}
	return cfg
}
