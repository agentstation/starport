package config

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"path/filepath"

	catalogconfig "github.com/agentstation/starmap/pkg/catalogs/config"
	"github.com/agentstation/starmap/pkg/productfiles"
	"github.com/agentstation/starmap/pkg/productpaths"
	"github.com/joho/godotenv"
)

// DefaultLocalPathUpgrade returns the source and destination of a native local upgrade.
// The destination is also available after configuration publication so startup can resume.
func (c *Config) DefaultLocalPathUpgrade() (source, target Paths, ok bool) {
	paths := c.EffectivePaths()
	for _, root := range []productpaths.Root{productpaths.Config, productpaths.Data, productpaths.State} {
		if paths.Origins[string(root)].Origin != pathOriginPlatformDefault {
			return source, target, false
		}
	}
	if c.ManagementMode() != ManagementLocal || paths.configExplicit ||
		paths.InstanceID != defaultInstanceID || paths.DeploymentID != defaultDeploymentID {
		return source, target, false
	}
	previous, err := previousConfigFile()
	target = pathsFromRoots(productpaths.Roots{
		productpaths.Config: paths.Origins[string(productpaths.Config)],
		productpaths.Data:   paths.Origins[string(productpaths.Data)],
		productpaths.State:  paths.Origins[string(productpaths.State)],
	})
	if err != nil || previous == target.ConfigFile || !c.UsesPlatformStorage(target) {
		return source, target, false
	}
	source = target
	source.ConfigFile = previous
	if paths.Origins[pathRoleRuntime].Origin == pathOriginLegacyDefault {
		source.RuntimeDir = paths.RuntimeDir
	}
	return source, target, true
}

// ReadsLegacyConfiguration reports whether the loader selected the earlier default.
func (c *Config) ReadsLegacyConfiguration() bool {
	return c.paths.Origins[pathRoleConfiguration].Origin == pathOriginLegacyDefault
}

// PathUpgradeConfiguration reads the same private file the loader validated.
// A local configuration save must settle before the path can change.
func (c *Config) PathUpgradeConfiguration(ctx context.Context) (original, operations []byte, err error) {
	path := c.paths.ConfigFile
	directory, err := productfiles.ExistingDirectory(filepath.Dir(path))
	if err != nil {
		return nil, nil, err
	}
	original, err = directory.ReadFile(filepath.Base(path), maxConfigurationBytes)
	if err != nil {
		return nil, nil, err
	}
	if fileRevision(original) != c.loadedFileChecksum() {
		return nil, nil, fmt.Errorf("configuration changed after it was loaded")
	}
	values, err := godotenv.Unmarshal(string(original))
	if err != nil || values["STARPORT_SECURITY_MASTER_KEY"] == "" ||
		values["STARPORT_SECURITY_MASTER_KEY"] != c.Security.MasterKey {
		return nil, nil, fmt.Errorf("the legacy configuration must save the selected master key for automatic migration")
	}
	journal, operations, err := (localTarget{directory: directory}).readJournal()
	if err != nil {
		return nil, nil, err
	}
	for _, operation := range journal.Operations {
		if operation.Status != localComplete {
			return nil, nil, fmt.Errorf("local configuration save must finish before the path upgrade")
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	return original, operations, nil
}

// PathUpgradeReplacement records the verified runtime and database selections.
func (c *Config) PathUpgradeReplacement(original []byte, target, identity string) ([]byte, error) {
	replacement, err := editDotenv(original, []FieldEdit{
		{Name: catalogconfig.StateDirectory, Value: &target},
		{Name: catalogconfig.SchedulerIdentity, Value: &identity},
	})
	if err != nil {
		return nil, err
	}
	replacement, err = recordUpgradeBadgerPath(replacement, c.paths.BadgerDir)
	if len(replacement) > maxConfigurationBytes {
		return nil, fmt.Errorf("the upgrade configuration exceeds the file size limit")
	}
	return replacement, err
}

// PublishPathUpgradeConfiguration installs the saved configuration and its completed receipts.
// A restart may encounter either file from an interrupted publication.
func PublishPathUpgradeConfiguration(ctx context.Context, directory *productfiles.Directory, name string, contents, operations []byte) error {
	if operations != nil {
		if err := publishUpgradeFile(ctx, directory, localJournalName, operations, maxJournalBytes); err != nil {
			return err
		}
	}
	return publishUpgradeFile(ctx, directory, name, contents, maxConfigurationBytes)
}

func publishUpgradeFile(ctx context.Context, directory *productfiles.Directory, name string, expected []byte, limit int64) error {
	current, err := directory.ReadFile(name, limit)
	if err == nil {
		if !bytes.Equal(current, expected) {
			return fmt.Errorf("%s changed during the path upgrade", name)
		}
		return nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return directory.CompareAndPublish(ctx, name, nil, expected)
}

func recordUpgradeBadgerPath(contents []byte, path string) ([]byte, error) {
	values, err := godotenv.Unmarshal(string(contents))
	if err != nil {
		return nil, fmt.Errorf("cannot parse the upgrade configuration")
	}
	if values[badgerPathEnvironment] == path {
		return contents, nil
	}
	if _, present := values[badgerPathEnvironment]; present || !dotenvSafeValue(path) {
		return nil, fmt.Errorf("cannot record the existing Badger path in the upgrade configuration")
	}
	values[badgerPathEnvironment] = path
	contents = fmt.Appendf(contents, "\n%s='%s'\n", badgerPathEnvironment, path)
	parsed, err := godotenv.Unmarshal(string(contents))
	if err != nil || !maps.Equal(parsed, values) {
		return nil, fmt.Errorf("cannot verify the upgrade configuration")
	}
	return contents, nil
}
