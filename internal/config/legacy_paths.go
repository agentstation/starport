package config

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/agentstation/starmap/pkg/productpaths"
)

// ErrLegacyPaths reports existing state outside an implicitly selected replacement.
var ErrLegacyPaths = errors.New("legacy filesystem state requires explicit path selection or verified migration")

const pathOriginLegacyDefault = "legacy-default"

// LegacyPath identifies an old location that an implicit default would abandon.
// Inspection reads metadata only and does not establish migration completion.
type LegacyPath struct {
	Role         string `json:"role"`
	PreviousPath string `json:"previous_path"`
	SelectedPath string `json:"selected_path"`
	Selector     string `json:"selector"`
	marker       string
}

func (p Paths) implicitPath(root productpaths.Root, role string) bool {
	if p.Origins[string(root)].Origin != pathOriginPlatformDefault {
		return false
	}
	origin := p.Origins[role].Origin
	return origin == "" || origin == pathOriginDefault || origin == "derived:"+string(root)
}

func (p Paths) legacyCandidates() ([]LegacyPath, error) {
	if p.legacyLocations != nil {
		return p.legacyLocations, nil
	}
	candidates := []LegacyPath{}
	add := func(role, previous, selected, selector, marker string) {
		if selected != "" && filepath.Clean(previous) != filepath.Clean(selected) {
			candidates = append(candidates, LegacyPath{role, previous, selected, selector, marker})
		}
	}
	legacyConfig := p.ConfigDir
	if p.Origins[string(productpaths.Config)].Origin == pathOriginPlatformDefault {
		previousConfig, err := previousConfigFile()
		if err != nil {
			return nil, err
		}
		legacyConfig = filepath.Dir(previousConfig)
		if !p.configExplicit {
			add(pathRoleConfiguration, previousConfig, p.ConfigFile, "STARPORT_CONFIG_FILE", "")
		}
	}
	previous := PathsForConfigDir(legacyConfig)
	for _, item := range []struct{ role, old, selected, selector string }{
		{pathRoleBadger, previous.BadgerDir, p.BadgerDir, badgerPathEnvironment},
		{pathRoleSQLite, previous.SQLiteFile, p.SQLiteFile, sqlitePathEnvironment},
		{pathRoleFiles, previous.FilesDir, p.FilesDir, filesPathEnvironment},
		{fileRoleLocalToken, previous.LocalTokenFile, p.LocalTokenFile, dataDirectoryEnvironment},
		{fileRoleWelcome, previous.WelcomeStampFile, p.WelcomeStampFile, dataDirectoryEnvironment},
	} {
		if legacyConfig != "" && item.selected != "" && p.implicitPath(productpaths.Data, item.role) {
			add(item.role, item.old, item.selected, item.selector, "")
			if item.role == pathRoleSQLite {
				for _, suffix := range []string{"-wal", "-shm", "-journal"} {
					add(item.role, item.old+suffix, item.selected+suffix, item.selector, "")
				}
			}
		}
	}
	runtime, err := p.legacyRuntimeCandidates()
	if err != nil {
		return nil, err
	}
	return append(candidates, runtime...), nil
}

func previousConfigFile() (string, error) {
	root, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("resolve previous configuration root: %w", err)
	}
	return filepath.Join(root, "starport", "config.env"), nil
}

func (p Paths) legacyRuntimeCandidates() ([]LegacyPath, error) {
	if p.RuntimeDir == "" || !p.implicitPath(productpaths.State, pathRoleRuntime) {
		return nil, nil
	}
	previous, err := ResolveStateDirectory("")
	if err != nil {
		return nil, err
	}
	if !filepath.IsAbs(previous) {
		return nil, fmt.Errorf("previous catalog runtime path must be absolute")
	}
	if previous == p.RuntimeDir {
		return nil, nil
	}
	// The new Linux runtime can be below the old root. Only old markers count.
	var candidates []LegacyPath
	for _, marker := range []string{"owner.json", "instance-seed", "catalog-runtime", "github-catalog-source"} {
		candidates = append(candidates, LegacyPath{pathRoleRuntime, previous, p.RuntimeDir, stateDirectoryEnvironment, marker})
	}
	return candidates, nil
}

// reuseLegacyConfiguration selects the previous default for a local upgrade.
// A configuration directory at the current location can hold a pending setup transaction.
func (l *Loader) reuseLegacyConfiguration(paths *Paths) error {
	if l.envFiles != nil || l.resolvePaths != nil || paths.configExplicit ||
		paths.Origins[string(productpaths.Config)].Origin != pathOriginPlatformDefault {
		return nil
	}
	previous, err := previousConfigFile()
	if err != nil || previous == paths.ConfigFile {
		return err
	}
	current, err := legacyPathInfo(paths.ConfigDir)
	if err != nil || current != nil {
		return err
	}
	config, err := legacyPathInfo(previous)
	if err != nil || config == nil || !config.Mode().IsRegular() {
		return err
	}
	data, err := productpaths.ResolveRoot(productpaths.Data, l.platformDefaults, rootLayer("environment", l.environment))
	if err != nil || data.Origin != pathOriginPlatformDefault {
		return err
	}
	manifest, err := legacyPathInfo(filepath.Join(data.Path, "badger", "MANIFEST"))
	if err != nil || manifest == nil || !manifest.Mode().IsRegular() {
		return err
	}
	paths.ConfigFile = previous
	paths.Origins[pathRoleConfiguration] = productpaths.Path{Path: previous, Origin: pathOriginLegacyDefault}
	return nil
}

func (c *Config) reuseLegacyRuntime(ctx context.Context, paths *Paths, defaults Paths) error {
	configuration := paths.Origins[pathRoleConfiguration]
	if configuration.Origin != pathOriginLegacyDefault {
		return nil
	}
	if c.ManagementMode() != ManagementLocal || strings.TrimSpace(c.Security.MasterKey) == "" ||
		paths.Origins[string(productpaths.Data)].Origin != pathOriginPlatformDefault || !c.UsesPlatformStorage(defaults) {
		return fmt.Errorf("%w: %q requires a master key and the default local stores for automatic reuse. Set STARPORT_CONFIG_FILE to select it explicitly",
			ErrLegacyPaths, configuration.Path)
	}
	if paths.InstanceID != defaultInstanceID || paths.DeploymentID != defaultDeploymentID {
		return nil
	}
	candidates, err := paths.legacyRuntimeCandidates()
	if err != nil || len(candidates) == 0 {
		return err
	}
	current, err := legacyPathInfo(paths.RuntimeDir)
	if err != nil || current != nil {
		return err
	}
	retained, err := inspectLegacyPaths(ctx, candidates)
	if err != nil || len(retained) == 0 {
		return err
	}
	previous := retained[0].PreviousPath
	directory, err := legacyPathInfo(previous)
	if err != nil || directory == nil || !directory.IsDir() {
		return err
	}
	c.Catalog.StateDirectory, paths.RuntimeDir = previous, previous
	paths.Origins[pathRoleRuntime] = productpaths.Path{Path: previous, Origin: pathOriginLegacyDefault}
	return nil
}

func legacyPathInfo(path string) (os.FileInfo, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	return info, err
}

func inspectLegacyPaths(ctx context.Context, candidates []LegacyPath) ([]LegacyPath, error) {
	conflicts := []LegacyPath{}
	seen := make(map[string]bool)
	for _, candidate := range candidates {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		path := candidate.PreviousPath
		if candidate.marker != "" {
			path = filepath.Join(path, candidate.marker)
		}
		if _, err := os.Lstat(path); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("inspect previous %s path: %w", candidate.Role, err)
		}
		key := candidate.Role + "\x00" + candidate.PreviousPath
		if !seen[key] {
			conflicts = append(conflicts, candidate)
			seen[key] = true
		}
	}
	return conflicts, ctx.Err()
}

func refuseLegacyPaths(conflicts []LegacyPath, err error) error {
	if err != nil || len(conflicts) == 0 {
		return err
	}
	first := conflicts[0]
	return fmt.Errorf("%w: %s exists at %q while the default selects %q. Set %s to the previous path. Run %q to list all path conflicts",
		ErrLegacyPaths, first.Role, first.PreviousPath, first.SelectedPath, first.Selector, "starport config paths --legacy")
}

// CheckLegacyPaths rejects defaults that abandon existing local setup paths.
// It creates no files and never opens a database or reads file contents.
func (p Paths) CheckLegacyPaths(ctx context.Context) error {
	candidates, err := p.legacyCandidates()
	if err != nil {
		return err
	}
	return refuseLegacyPaths(inspectLegacyPaths(ctx, candidates))
}

// LegacyPaths reports conflicts for selected persistent storage and primary configuration.
// Explicit locations and ephemeral development storage do not need legacy checks.
func (c *Config) LegacyPaths(ctx context.Context) ([]LegacyPath, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if c.Catalog.StateDirectoryIsScratch() {
		return []LegacyPath{}, nil
	}
	p := c.EffectivePaths()
	primary := false
	for _, input := range c.fileInputs {
		primary = primary || input.primary
	}
	candidates, err := p.legacyCandidates()
	if err != nil {
		return nil, err
	}
	selected := make([]LegacyPath, 0, len(candidates))
	for _, candidate := range candidates {
		switch candidate.Role {
		case pathRoleConfiguration:
			if !primary {
				continue
			}
		case pathRoleBadger:
			if c.Storage.Mode != storageModeBadger || c.Storage.Badger.inMemory {
				continue
			}
		case pathRoleSQLite:
			if c.Storage.SQL.Mode != sqlModeSQLite {
				continue
			}
		case pathRoleFiles:
			if c.Files.SelectedBackend() != BlobBackendFilesystem {
				continue
			}
		}
		selected = append(selected, candidate)
	}
	return inspectLegacyPaths(ctx, selected)
}

// CheckLegacyPaths refuses startup before stores can abandon known prior state.
func (c *Config) CheckLegacyPaths(ctx context.Context) error {
	return refuseLegacyPaths(c.LegacyPaths(ctx))
}
