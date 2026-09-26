package setup

import (
	"context"
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

const setupBindingFile = "configuration.json"

func databaseLockDirectory(paths config.Paths) string {
	return ".starport-setup-" + filepath.Base(paths.BadgerDir)
}

func lockDatabase(ctx context.Context, paths config.Paths) (*setupWriter, error) {
	if !filepath.IsAbs(paths.BadgerDir) || filepath.Clean(paths.BadgerDir) != paths.BadgerDir ||
		!setupChildName(filepath.Base(paths.BadgerDir)) || strings.ContainsRune(paths.BadgerDir, '\x00') {
		return nil, ErrPathsRequired
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	info, err := os.Lstat(paths.BadgerDir)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	if info != nil && !info.IsDir() {
		return nil, &os.PathError{Op: "open", Path: paths.BadgerDir, Err: errors.New("database path must be a directory without links")}
	}
	metadata, err := createSetupDirectory(filepath.Join(filepath.Dir(paths.BadgerDir), databaseLockDirectory(paths)))
	if err != nil {
		return nil, err
	}
	guard, err := lockSetupMetadata(ctx, metadata, metadata)
	if err != nil {
		return nil, err
	}
	if err := guard.directory.RecoverPublications(ctx); err != nil {
		return nil, errors.Join(err, guard.close())
	}
	return guard, nil
}

// GuardLocalStorage excludes setup while a persistent local gateway owns storage.
// The caller must close the guard after all gateway stores close.
// A pending setup transaction requires an explicit initialization retry.
func GuardLocalStorage(ctx context.Context, paths config.Paths) (_ io.Closer, resultErr error) {
	if paths.ConfigFile != "" && (!filepath.IsAbs(paths.ConfigFile) || filepath.Clean(paths.ConfigFile) != paths.ConfigFile) {
		return nil, ErrPathsRequired
	}
	guard, err := lockDatabase(ctx, paths)
	if err != nil {
		return nil, err
	}
	defer func() {
		if resultErr != nil {
			resultErr = errors.Join(resultErr, guard.close())
		}
	}()
	if _, err := guard.directory.ReadFile(setupBindingFile, setupJournalLimit); !os.IsNotExist(err) {
		return nil, errors.Join(fmt.Errorf("%w: retry local initialization before storage opens", ErrPartialState), err)
	}
	if err := inspectSettledMetadata(guard.directory, true); err != nil {
		return nil, err
	}
	if paths.ConfigFile == "" {
		return guard, nil
	}
	if err := inspectRuntimeSetup(paths); err != nil {
		return nil, err
	}
	return guard, nil
}

func inspectRuntimeSetup(paths config.Paths) error {
	configurationParent := filepath.Dir(paths.ConfigFile)
	for _, name := range []string{setupMetadataDirectory, recordPublicationDirectory} {
		metadata, err := productfiles.ExistingDirectory(filepath.Join(configurationParent, name))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		if err := inspectSettledMetadata(metadata, name == setupMetadataDirectory); err != nil {
			return fmt.Errorf("local setup must finish before storage opens: %w", err)
		}
	}
	return nil
}

func (w *setupWriter) bindSetup(ctx context.Context, paths config.Paths) error {
	if err := inspectStorageMetadata(w.directory, paths.ConfigFile); err != nil {
		return err
	}
	encoded, err := json.Marshal(setupBinding{paths.ConfigFile})
	if err != nil {
		return err
	}
	retained, err := w.directory.ReadFile(setupBindingFile, setupJournalLimit)
	if err == nil {
		if string(retained) != string(encoded) {
			return fmt.Errorf("%w: another configuration owns retained local setup", ErrPartialState)
		}
		return nil
	}
	if !os.IsNotExist(err) {
		return err
	}
	return w.directory.CompareAndPublish(ctx, setupBindingFile, nil, encoded)
}

type setupBinding struct {
	Configuration string `json:"configuration"`
}

func (w *setupWriter) finishSetup(paths config.Paths) error {
	// Retain the database binding until the configuration transaction settles.
	if err := inspectRuntimeSetup(paths); err != nil {
		return err
	}
	encoded, err := json.Marshal(setupBinding{paths.ConfigFile})
	if err != nil {
		return err
	}
	_, err = w.directory.CompareAndRemove(context.Background(), setupBindingFile, encoded)
	return err
}
