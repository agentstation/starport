package setup

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/agentstation/starmap/pkg/productfiles"
	"github.com/agentstation/starport/internal/config"
)

func (s *Service) configurationDirectory() string { return filepath.Dir(s.paths.ConfigFile) }
func (s *Service) databaseParent() string         { return filepath.Dir(s.paths.BadgerDir) }

func validateSetupPaths(paths config.Paths) error {
	for _, path := range []string{paths.ConfigDir, paths.ConfigFile, paths.DataDir, paths.BadgerDir} {
		if !filepath.IsAbs(path) || filepath.Clean(path) != path || strings.ContainsRune(path, '\x00') {
			return ErrPathsRequired
		}
	}
	for _, leaf := range []string{paths.ConfigFile, paths.BadgerDir} {
		name := filepath.Base(leaf)
		if !setupChildName(name) || name == setupMetadataDirectory || name == recordPublicationDirectory ||
			strings.HasPrefix(name, ".starport-init-") || filepath.Dir(leaf) == leaf {
			return ErrPathsRequired
		}
	}
	metadata := filepath.Join(filepath.Dir(paths.ConfigFile), setupMetadataDirectory)
	publications := filepath.Join(filepath.Dir(paths.ConfigFile), recordPublicationDirectory)
	storageMetadata := filepath.Join(filepath.Dir(paths.BadgerDir), databaseLockDirectory(paths))
	for _, selected := range []string{paths.ConfigFile, paths.ConfigDir, paths.DataDir, paths.BadgerDir} {
		if setupContains(storageMetadata, selected) {
			return fmt.Errorf("%w: setup paths overlap database ownership metadata", ErrPathsRequired)
		}
	}
	for _, selected := range []string{paths.ConfigFile, paths.ConfigDir, paths.DataDir, metadata, publications} {
		if setupContains(paths.BadgerDir, selected) {
			return fmt.Errorf("%w: setup paths overlap the database", ErrPathsRequired)
		}
	}
	for _, selected := range []string{paths.ConfigDir, paths.DataDir, paths.BadgerDir} {
		if setupContains(paths.ConfigFile, selected) || setupContains(metadata, selected) || setupContains(publications, selected) {
			return fmt.Errorf("%w: setup paths overlap configuration metadata", ErrPathsRequired)
		}
	}
	return nil
}

func setupContains(parent, path string) bool {
	relative, err := filepath.Rel(parent, path)
	return err == nil && filepath.IsLocal(relative)
}

// Inspect reads setup artifacts at their selected paths without changing them.
// Empty roots and settled setup metadata do not count as application state.
func Inspect(paths config.Paths) (State, error) {
	if err := validateSetupPaths(paths); err != nil {
		return "", err
	}
	configExists, err := pathExists(paths.ConfigFile)
	if err != nil {
		return "", err
	}
	storeExists, err := pathExists(paths.BadgerDir)
	if err != nil {
		return "", err
	}
	if configExists && storeExists {
		configuration, err := productfiles.ExistingDirectory(filepath.Dir(paths.ConfigFile))
		if err != nil {
			return "", err
		}
		if _, err := configuration.ReadFile(filepath.Base(paths.ConfigFile), setupJournalLimit); err != nil {
			return "", err
		}
		if _, err := productfiles.ExistingDirectory(paths.BadgerDir); err != nil {
			return "", err
		}
		if err := inspectSetupMetadata(configuration); err != nil {
			return StatePartial, err
		}
		return StateReady, nil
	}
	if configExists || storeExists {
		return StatePartial, nil
	}
	if err := inspectEmptySetup(paths); err != nil {
		if errors.Is(err, ErrPartialState) {
			return StatePartial, nil
		}
		return "", err
	}
	return StateAbsent, nil
}

func inspectEmptySetup(paths config.Paths) error {
	configuration := filepath.Dir(paths.ConfigFile)
	data := filepath.Dir(paths.BadgerDir)
	for _, path := range []string{configuration, data} {
		directory, err := productfiles.ExistingDirectory(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		entries, err := setupEntries(directory)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			child := filepath.Join(path, entry.Name())
			if path == data && entry.Name() == databaseLockDirectory(paths) {
				metadata, err := directory.ExistingChild(entry.Name())
				if err != nil {
					return err
				}
				if err := inspectStorageMetadata(metadata, paths.ConfigFile); err != nil {
					return err
				}
				continue
			}
			if path == configuration && (entry.Name() == setupMetadataDirectory || entry.Name() == recordPublicationDirectory) {
				continue
			}
			if child != path && (setupContains(child, configuration) || setupContains(child, data)) {
				continue
			}
			return fmt.Errorf("%w: selected setup directory contains existing state", ErrPartialState)
		}
		if path == configuration {
			if err := inspectSetupMetadata(directory); err != nil {
				return err
			}
		}
	}
	return nil
}

func inspectSetupMetadata(configuration *productfiles.Directory) error {
	for _, name := range []string{setupMetadataDirectory, recordPublicationDirectory} {
		metadata, err := configuration.ExistingChild(name)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		if err := inspectSettledMetadata(metadata, name == setupMetadataDirectory); err != nil {
			return err
		}
	}
	return nil
}

func inspectStorageMetadata(directory *productfiles.Directory, configuration string) error {
	entries, err := setupEntries(directory)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.Name() == setupBindingFile {
			// The public initialization flow holds this binding during inspection.
			retained, err := directory.ReadFile(entry.Name(), setupJournalLimit)
			if err != nil {
				return err
			}
			var binding setupBinding
			if err := json.Unmarshal(retained, &binding); err != nil || binding.Configuration != configuration {
				return errors.Join(ErrPartialState, err)
			}
			continue
		}
		if entry.Name() == setupLockFile {
			if _, err := directory.ReadFile(entry.Name(), 0); err != nil {
				return err
			}
			continue
		}
		if entry.Name() != recordPublicationDirectory {
			return ErrPartialState
		}
		child, err := directory.ExistingChild(entry.Name())
		if err != nil {
			return err
		}
		if err := inspectSettledMetadata(child, false); err != nil {
			return err
		}
	}
	return nil
}

func inspectSettledMetadata(directory *productfiles.Directory, nested bool) error {
	entries, err := setupEntries(directory)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.Name() == setupLockFile {
			if _, err := directory.ReadFile(entry.Name(), 0); err != nil {
				return err
			}
			continue
		}
		if nested && entry.Name() == recordPublicationDirectory {
			child, err := directory.ExistingChild(entry.Name())
			if err != nil {
				return err
			}
			if err := inspectSettledMetadata(child, false); err != nil {
				return err
			}
			continue
		}
		return fmt.Errorf("%w: setup metadata requires recovery", ErrPartialState)
	}
	return nil
}

func setupEntries(directory *productfiles.Directory) (_ []os.DirEntry, resultErr error) {
	root, err := directory.Open()
	if err != nil {
		return nil, err
	}
	defer func() { resultErr = errors.Join(resultErr, root.Close()) }()
	file, err := root.Open(".")
	if err != nil {
		return nil, err
	}
	entries, readErr := file.ReadDir(setupFileCount + 1)
	closeErr := file.Close()
	if readErr != nil && !errors.Is(readErr, io.EOF) {
		return nil, errors.Join(readErr, closeErr)
	}
	if len(entries) > setupFileCount {
		return nil, errors.Join(ErrPartialState, closeErr)
	}
	return entries, closeErr
}
