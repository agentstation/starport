package config

import (
	"fmt"
	"maps"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/agentstation/starmap/pkg/productpaths"
	"github.com/sethvargo/go-envconfig"
)

const pathOriginGoOption = "go-option"
const pathRoleRuntime = "runtime"
const configDirectoryEnvironment = "STARPORT_CONFIG_DIR"

// Paths contains resolved files, roots, and their selection origins.
type Paths struct {
	ConfigDir        string                       `json:"config_dir"`
	ConfigFile       string                       `json:"config_file"`
	DataDir          string                       `json:"data_dir"`
	StateDir         string                       `json:"state_dir"`
	CacheDir         string                       `json:"cache_dir"`
	RuntimeDir       string                       `json:"runtime_dir"`
	BaselineDir      string                       `json:"baseline_dir"`
	BadgerDir        string                       `json:"badger_dir"`
	SQLiteFile       string                       `json:"sqlite_file"`
	FilesDir         string                       `json:"files_dir"`
	LocalTokenFile   string                       `json:"local_token_file"`
	WelcomeStampFile string                       `json:"welcome_stamp_file"`
	InstanceID       string                       `json:"instance_id"`
	DeploymentID     string                       `json:"deployment_id"`
	Origins          map[string]productpaths.Path `json:"origins"`
	configExplicit   bool
}

// PlatformPaths resolves product settings from the environment without reading files.
func PlatformPaths() (Paths, error) {
	loader := NewLoader()
	paths, err := loader.bootstrapPaths()
	if err != nil {
		return Paths{}, err
	}
	return loader.managedPaths(paths, loader.environment, []productpaths.Layer{rootLayer("environment", loader.environment)})
}

// PathsForConfigDir selects a caller-owned layout below one absolute directory.
// Platform defaults use separate native roots instead.
func PathsForConfigDir(configDir string) Paths {
	configDir = filepath.Clean(configDir)
	roots := productpaths.Roots{}
	for root, path := range map[productpaths.Root]string{
		productpaths.Config: configDir,
		productpaths.Data:   filepath.Join(configDir, "data"),
		productpaths.State:  filepath.Join(configDir, "state"),
		productpaths.Cache:  filepath.Join(configDir, "cache"),
	} {
		roots[root] = productpaths.Path{Path: path, Origin: pathOriginGoOption}
	}
	return pathsFromRoots(roots)
}

func pathsFromRoots(roots productpaths.Roots) Paths {
	configDir, dataDir := roots[productpaths.Config].Path, roots[productpaths.Data].Path
	stateDir := roots[productpaths.State].Path
	origins := make(map[string]productpaths.Path, len(roots))
	for root, path := range roots {
		origins[string(root)] = path
	}
	return Paths{
		ConfigDir: configDir, ConfigFile: filepath.Join(configDir, "config.env"),
		DataDir: dataDir, StateDir: stateDir, CacheDir: roots[productpaths.Cache].Path,
		RuntimeDir:  filepath.Join(stateDir, "catalog", "runtime", "default"),
		BaselineDir: filepath.Join(dataDir, "catalog", "baseline"),
		BadgerDir:   filepath.Join(dataDir, "badger"), SQLiteFile: filepath.Join(dataDir, "sqlite", "starport.db"),
		FilesDir: filepath.Join(dataDir, "files"), LocalTokenFile: filepath.Join(dataDir, "local-admin-token.json"),
		WelcomeStampFile: filepath.Join(dataDir, "welcomed"), InstanceID: "default", DeploymentID: "local", Origins: origins,
	}
}

func rootLayer(name string, source envconfig.Lookuper) productpaths.Layer {
	values := make(map[productpaths.Root]string)
	for root, key := range map[productpaths.Root]string{
		productpaths.Home: "STARPORT_HOME", productpaths.Config: configDirectoryEnvironment,
		productpaths.Data: "STARPORT_DATA_DIR", productpaths.State: "STARPORT_STATE_ROOT", productpaths.Cache: "STARPORT_CACHE_DIR",
	} {
		if value, present := source.Lookup(key); present {
			values[root] = value
		}
	}
	return productpaths.Layer{Name: name, Values: values}
}

func (l *Loader) bootstrapPaths() (Paths, error) {
	var paths Paths
	if l.resolvePaths != nil {
		selected, err := l.resolvePaths()
		if err != nil {
			return Paths{}, err
		}
		paths = selected
		paths.Origins = maps.Clone(selected.Origins)
		if paths.Origins == nil {
			paths.Origins = make(map[string]productpaths.Path)
		}
	} else {
		root, err := productpaths.ResolveRoot(productpaths.Config, productpaths.UserDefaults(productpaths.Starport), rootLayer("environment", l.environment))
		if err != nil {
			return Paths{}, err
		}
		paths.ConfigDir, paths.ConfigFile = root.Path, filepath.Join(root.Path, "config.env")
		paths.Origins = map[string]productpaths.Path{string(productpaths.Config): root}
	}
	if !filepath.IsAbs(paths.ConfigDir) {
		return Paths{}, fmt.Errorf("configuration root must be absolute")
	}
	if file, present := l.environment.Lookup("STARPORT_CONFIG_FILE"); present {
		base, err := relativeBase(l.environment)
		if err != nil {
			return Paths{}, err
		}
		path, err := selectedLeaf(paths.ConfigDir, file, "environment", base)
		if err != nil {
			return Paths{}, fmt.Errorf("STARPORT_CONFIG_FILE: %w", err)
		}
		paths.ConfigFile, paths.configExplicit = path.Path, true
		paths.Origins["configuration"] = path
	}
	return paths, nil
}

func (l *Loader) managedPaths(bootstrap Paths, source envconfig.Lookuper, layers []productpaths.Layer) (Paths, error) {
	if l.resolvePaths != nil {
		layers = append([]productpaths.Layer{{Name: pathOriginGoOption, Values: map[productpaths.Root]string{
			productpaths.Data: bootstrap.DataDir, productpaths.State: bootstrap.StateDir, productpaths.Cache: bootstrap.CacheDir,
		}}}, layers...)
	}
	// The selected file cannot move the configuration root that selected it.
	layers = append([]productpaths.Layer{{Name: "bootstrap", Values: map[productpaths.Root]string{productpaths.Config: bootstrap.ConfigDir}}}, layers...)
	roots, err := productpaths.Resolve(productpaths.UserDefaults(productpaths.Starport), layers...)
	if err != nil {
		return Paths{}, err
	}
	roots[productpaths.Config] = bootstrap.Origins[string(productpaths.Config)]
	if roots[productpaths.Config].Path == "" {
		roots[productpaths.Config] = productpaths.Path{Path: bootstrap.ConfigDir, Origin: pathOriginGoOption}
	}
	paths := pathsFromRoots(roots)
	paths.ConfigFile, paths.configExplicit = bootstrap.ConfigFile, bootstrap.configExplicit
	if origin, ok := bootstrap.Origins["configuration"]; ok {
		paths.Origins["configuration"] = origin
	}
	if l.resolvePaths != nil {
		paths.BadgerDir, paths.SQLiteFile, paths.FilesDir = bootstrap.BadgerDir, bootstrap.SQLiteFile, bootstrap.FilesDir
		paths.LocalTokenFile, paths.WelcomeStampFile = bootstrap.LocalTokenFile, bootstrap.WelcomeStampFile
	}
	if value, present := source.Lookup("STARPORT_INSTANCE_ID"); present {
		paths.InstanceID = value
	}
	if err := productpaths.ValidateInstanceID(paths.InstanceID); err != nil {
		return Paths{}, err
	}
	if value, present := source.Lookup("STARPORT_DEPLOYMENT_ID"); present {
		paths.DeploymentID = value
	}
	deployment := paths.DeploymentID
	if deployment == "" || strings.TrimSpace(deployment) != deployment || len(deployment) > 256 || !utf8.ValidString(deployment) || strings.ContainsFunc(deployment, unicode.IsControl) {
		return Paths{}, fmt.Errorf("STARPORT_DEPLOYMENT_ID requires at most 256 UTF-8 bytes without surrounding whitespace or control characters")
	}
	paths.RuntimeDir = filepath.Join(paths.StateDir, "catalog", "runtime", paths.InstanceID)
	return paths, nil
}

func relativeBase(source envconfig.Lookuper) (string, error) {
	value, _ := source.Lookup("STARPORT_RELATIVE_PATH_BASE")
	if value != "" && value != "config" {
		return "", fmt.Errorf("STARPORT_RELATIVE_PATH_BASE must be empty or config")
	}
	return value, nil
}

func selectedLeaf(configDir, value, origin, base string) (productpaths.Path, error) {
	if !filepath.IsAbs(value) && base != "config" {
		return productpaths.Path{}, fmt.Errorf("use an absolute path or set STARPORT_RELATIVE_PATH_BASE=config after resolving the previous location")
	}
	return productpaths.Leaf(productpaths.Path{Path: configDir}, value, origin)
}

// EffectivePaths returns a copy of the resolved product layout.
func (c *Config) EffectivePaths() Paths {
	if c == nil {
		return Paths{}
	}
	paths := c.paths
	paths.Origins = maps.Clone(paths.Origins)
	return paths
}
