package config

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"maps"

	"github.com/agentstation/starmap/pkg/productpaths"
	"github.com/agentstation/starmap/pkg/productpaths/policy"
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
		prefix:      "STARPORT_",
		environment: envconfig.OsLookuper(),
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

// Override applies a caller decision after the loader reads configuration sources.
// Validation checks overrides and environment values with the same rules.
// Command-line flags use this contract.
type Override func(*Config)

// DisableAuthentication disables the gateway API key check.
// It has the same authority as STARPORT_SECURITY_AUTH_MODE=disabled.
// The exposure rule still refuses a non-loopback bind without explicit permission.
// A flag or environment value overrides the mode saved through the console.
// This override records the flag origin so startup can report the selected authority.
func DisableAuthentication() Override {
	return func(cfg *Config) {
		cfg.Security.AuthMode = AuthModeDisabled
		cfg.authModeFromFlag = true
	}
}

// AllowRemoteWithoutAuthentication acknowledges that an unauthenticated
// gateway may bind an address the network can reach. Alone it changes nothing.
// It permits the remote address when DisableAuthentication disables authentication.
func AllowRemoteWithoutAuthentication() Override {
	return func(cfg *Config) { cfg.Security.AllowRemoteNoAuth = true }
}

// Load resolves configuration sources, applies defaults and any overrides, and
// validates the result.
func (l *Loader) Load(ctx context.Context, overrides ...Override) (*Config, error) {
	return l.load(ctx, false, overrides)
}

// LoadDevelopment reads process settings, applies the guarded development
// runtime contract and any overrides, and validates the result.
func (l *Loader) LoadDevelopment(ctx context.Context, overrides ...Override) (*Config, error) {
	return l.load(ctx, true, overrides)
}

func (l *Loader) load(ctx context.Context, development bool, overrides []Override) (*Config, error) {
	if development {
		if err := rejectDevelopmentEnvironment(l.environment); err != nil {
			return nil, newLoadFailure(err.Error(), err)
		}
		// Development reads no saved configuration, including explicitly selected files.
		selected := *l
		selected.envFiles = []string{}
		l = &selected
	}
	paths, err := l.bootstrapPaths()
	if err != nil {
		return nil, newLoadFailure("configuration paths could not be resolved", err)
	}

	lookuper, err := l.sourceLookuper(ctx, paths)
	if err != nil {
		return nil, newLoadFailure("configuration sources could not be read", err)
	}

	selected := lookuper.(catalogSettingsLookuper)
	paths, err = l.managedPaths(paths, lookuper, selected.pathLayers)
	if err != nil {
		return nil, newLoadFailure("configuration paths could not be resolved", err)
	}
	base, err := relativeBase(lookuper)
	if err != nil {
		return nil, newLoadFailure("relative path base is invalid", err)
	}
	paths.RelativePathBase, paths.RelativePathBaseOrigin = base, pathOriginDefault
	for index, source := range selected.sources {
		if _, present := source.Lookup("STARPORT_RELATIVE_PATH_BASE"); present {
			paths.RelativePathBaseOrigin = selected.pathLayers[index].Name
			break
		}
	}

	raw := envconfig.MultiLookuper(selected.sources...)
	for _, key := range []string{badgerPathEnvironment, sqlitePathEnvironment, stateDirectoryEnvironment, filesPathEnvironment} {
		if value, present := raw.Lookup(key); present && value == "" {
			return nil, newLoadFailure(key+" requires a nonempty path", fmt.Errorf("%s requires a nonempty path", key))
		}
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
	if development {
		cfg.Storage.Badger.Path, cfg.Storage.SQL.SQLite.Path, cfg.Files.Path = "", "", ""
	}
	if err := envconfig.ProcessWith(ctx, &envconfig.Config{
		Target:   cfg,
		Lookuper: envconfig.PrefixLookuper(l.prefix, lookuper),
	}); err != nil {
		return nil, newLoadFailure("configuration values could not be decoded", err)
	}
	if selected, ok := lookuper.(catalogSettingsLookuper); ok {
		cfg.Catalog.canonicalValues = maps.Clone(selected.values)
	}
	cfg.Catalog.PermissionClock, err = loadPermissionClock(lookuper)
	if err != nil {
		return nil, newLoadFailure("catalog permission clock values could not be decoded", err)
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
	if development {
		// No source selects Valkey. Remove its unused environment defaults.
		cfg.Storage.Valkey = ValkeyConfig{}
	}
	// Overrides retain precedence over loaded values and pass the same validation.
	pathOrigins := loadedPathOrigins(cfg, selected)
	for _, override := range overrides {
		if override != nil {
			override(cfg)
		}
	}

	if development {
		if err := cfg.ConfigureDevelopmentRuntime(); err != nil {
			return nil, newLoadFailure(err.Error(), err)
		}
		paths.DataDir, paths.StateDir, paths.CacheDir, paths.BaselineDir = "", "", "", ""
	}

	if err := resolveConfiguredPaths(cfg, &paths, base, pathOrigins); err != nil {
		return nil, newLoadFailure("configured paths could not be resolved", err)
	}
	if err := validateLoadedConfiguration(cfg, &paths, development); err != nil {
		return nil, err
	}
	cfg.paths = paths
	cfg.fileInputs = selected.fileInputs
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

func validateLoadedConfiguration(cfg *Config, paths *Paths, development bool) error {
	if err := cfg.Validate(); err != nil {
		return newLoadFailure("configuration values are invalid", err)
	}
	if development {
		return nil
	}
	var err error
	paths.legacyLocations, err = paths.legacyCandidates()
	if err != nil {
		return newLoadFailure("previous configuration paths could not be resolved", err)
	}
	return nil
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

func (l *Loader) sourceLookuper(ctx context.Context, paths Paths) (envconfig.Lookuper, error) {
	files := l.envFiles
	primary := files == nil
	if primary {
		files = []string{paths.ConfigFile}
	}
	access, _ := l.environment.Lookup("STARPORT_CONFIG_ACCESS")
	checkedAccess, err := policy.Configuration(access, primary && paths.configExplicit)
	if err != nil {
		return nil, err
	}
	base, err := relativeBase(l.environment)
	if err != nil {
		return nil, err
	}
	lookupers := []envconfig.Lookuper{catalogClockLookuper{l.environment}}
	layers := []productpaths.Layer{rootLayer("environment", l.environment)}
	var inputs []configurationFile
	for _, file := range files {
		selected, err := selectedLeaf(paths.ConfigDir, file, "go-option", base)
		if err != nil {
			return nil, err
		}
		input := configurationFile{location: selected, access: policy.OwnerOnly}
		if primary {
			input.primary, input.access = true, checkedAccess
			input.location = paths.Origins["configuration"]
			if input.location.Path == "" {
				input.location = productpaths.Path{Path: selected.Path, Origin: "derived:config"}
			}
		}
		inputs = append(inputs, input)
		var data []byte
		if primary {
			data, err = productpaths.ReadConfiguration(ctx, productpaths.ConfigurationInput{Path: selected.Path, AccessPolicy: access, Explicit: paths.configExplicit, MaxBytes: 1 << 20})
		} else {
			data, err = productpaths.ReadDotenv(ctx, selected.Path, 1<<20)
		}
		if err != nil {
			if primary && !paths.configExplicit && errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return nil, fmt.Errorf("read configuration file %q: %w", selected.Path, err)
		}
		values, err := godotenv.Unmarshal(string(data))
		if err != nil {
			return nil, fmt.Errorf("parse configuration file %q: %w", selected.Path, err)
		}
		lookupers = append(lookupers, catalogClockLookuper{envconfig.MapLookuper(values)})
		layers = append(layers, rootLayer("file:"+selected.Path, envconfig.MapLookuper(values)))
	}
	resolved, err := resolveCatalogLookuper(lookupers)
	if err != nil {
		return nil, err
	}
	selected := resolved.(catalogSettingsLookuper)
	selected.pathLayers, selected.sources = layers, lookupers
	selected.fileInputs = inputs
	return selected, nil
}

func resolveConfiguredPaths(cfg *Config, paths *Paths, base string, origins map[string]pathSelectionOrigin) error {
	for _, selection := range pathSelections(cfg) {
		loaded, known := origins[selection.name]
		origin := loaded.origin
		if !known || loaded.value != *selection.value {
			origin = pathOriginGoOption
		}
		if selection.name == pathRoleRuntime && *selection.value == "" && selection.required {
			*selection.value = paths.RuntimeDir
		}
		if *selection.value == "" && !selection.required {
			continue
		}
		path, err := selectedLeaf(paths.ConfigDir, *selection.value, origin, base)
		if err != nil {
			return fmt.Errorf("%s path: %w", selection.name, err)
		}
		*selection.value = path.Path
		paths.Origins[selection.name] = path
	}
	paths.BadgerDir, paths.SQLiteFile, paths.FilesDir = cfg.Storage.Badger.Path, cfg.Storage.SQL.SQLite.Path, cfg.Files.Path
	paths.RuntimeDir, paths.LocalTokenFile = cfg.Catalog.StateDirectory, cfg.Security.LocalTokenPath
	return nil
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
