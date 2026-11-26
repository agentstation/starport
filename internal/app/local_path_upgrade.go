package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/agentstation/starmap/pkg/productfiles"
	"github.com/agentstation/starport/internal/catalog"
	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/setup"
	"github.com/agentstation/starport/internal/storage"
)

const (
	pathUpgradeDirectory       = ".starport-path-upgrade"
	pathUpgradeRecord          = "upgrade.json"
	pathUpgradeSource          = "original.env"
	pathUpgradeTarget          = "replacement.env"
	pathUpgradeOperations      = "configuration-operations.json"
	prepareUpgradeRuntime      = "prepare-runtime"
	publishUpgradeRuntime      = "publish-runtime"
	publishUpgradeConfig       = "publish-config"
	completeUpgradeRuntime     = "complete-runtime"
	retireUpgradeConfig        = "retire-config"
	pathUpgradeConfigLimit     = 1 << 20
	pathUpgradeOperationsLimit = 4 << 20
)

type localPathUpgrade struct {
	source, target config.Paths
	root           *productfiles.Directory
	load           func(context.Context) (*config.Config, error)
	checkpoint     func(string) error
}

type pathUpgradeRecordV1 struct {
	Version           int      `json:"version"`
	Phase             string   `json:"phase"`
	SourceConfig      string   `json:"source_config"`
	TargetConfig      string   `json:"target_config"`
	SourceRuntime     string   `json:"source_runtime,omitempty"`
	TargetRuntime     string   `json:"target_runtime,omitempty"`
	Identity          string   `json:"scheduler_identity,omitempty"`
	BadgerPath        string   `json:"badger_path"`
	OriginalDigest    [32]byte `json:"original_digest"`
	ReplacementDigest [32]byte `json:"replacement_digest"`
	OperationsDigest  [32]byte `json:"operations_digest"`
}

// UpgradeLocalPaths moves one verified local installation before the gateway opens.
// Read-only configuration commands continue to resolve paths without migration.
func UpgradeLocalPaths(ctx context.Context, cfg *config.Config, load func(context.Context) (*config.Config, error)) (*config.Config, error) {
	return upgradeLocalPathsWithCheckpoint(ctx, cfg, load, nil)
}

func upgradeLocalPathsWithCheckpoint(ctx context.Context, cfg *config.Config, load func(context.Context) (*config.Config, error), checkpoint func(string) error) (_ *config.Config, resultErr error) {
	source, target, ok := cfg.DefaultLocalPathUpgrade()
	if !ok {
		return cfg, nil
	}
	journalPath := filepath.Join(target.DataDir, pathUpgradeDirectory)
	_, err := os.Lstat(filepath.Join(journalPath, pathUpgradeRecord))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	resuming := err == nil
	if !resuming && !cfg.ReadsLegacyConfiguration() {
		return cfg, nil
	}
	if !resuming && source.RuntimeDir != target.RuntimeDir {
		if err := catalogSettings(cfg).InspectUpgradeSource(ctx); err != nil {
			return nil, err
		}
	}
	guardPaths := cfg.EffectivePaths()
	if resuming {
		// The upgrade journal owns recovery of its configuration publication.
		// Take the database lock before accessing either configuration directory.
		guardPaths.ConfigFile = ""
	}
	guard, err := setup.GuardLocalStorage(ctx, guardPaths)
	if err != nil {
		return nil, err
	}
	defer func() { resultErr = errors.Join(resultErr, guard.Close()) }()
	cfg, err = load(ctx)
	if err != nil {
		return nil, err
	}
	source, target, ok = cfg.DefaultLocalPathUpgrade()
	if !ok {
		return nil, fmt.Errorf("local paths changed while waiting for the upgrade lock")
	}
	upgrade := localPathUpgrade{source: source, target: target, load: load, checkpoint: checkpoint}
	root, err := productfiles.ExistingDirectory(journalPath)
	if err == nil {
		upgrade.root = root
		cfg, resumed, err := upgrade.resume(ctx, cfg)
		if resumed || err != nil {
			return cfg, err
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	if !cfg.ReadsLegacyConfiguration() {
		return cfg, nil
	}
	return upgrade.start(ctx, cfg, journalPath)
}

func (u localPathUpgrade) start(ctx context.Context, cfg *config.Config, journalPath string) (*config.Config, error) {
	if err := cfg.CheckLegacyPaths(ctx); err != nil {
		return nil, err
	}
	original, operations, err := cfg.PathUpgradeConfiguration(ctx)
	if err != nil {
		return nil, err
	}
	replacement := original
	record := pathUpgradeRecordV1{
		Version: 1, Phase: publishUpgradeConfig, SourceConfig: u.source.ConfigFile, TargetConfig: u.target.ConfigFile, BadgerPath: u.target.BadgerDir,
	}
	if u.source.RuntimeDir != u.target.RuntimeDir {
		identity, err := readLocalUpgradeIdentity(ctx, cfg)
		if err != nil {
			return nil, err
		}
		record.Phase, record.Identity = prepareUpgradeRuntime, identity
		record.SourceRuntime, record.TargetRuntime = u.source.RuntimeDir, u.target.RuntimeDir
		replacement, err = cfg.PathUpgradeReplacement(original, record.TargetRuntime, record.Identity)
		if err != nil {
			return nil, err
		}
	}
	if u.root == nil {
		u.root, err = createUpgradeDirectory(journalPath)
		if err != nil {
			return nil, err
		}
	}
	if err := u.stageConfiguration(ctx, &record, original, replacement, operations); err != nil {
		return nil, err
	}
	return u.run(ctx, cfg, record, nil)
}

func readLocalUpgradeIdentity(ctx context.Context, cfg *config.Config) (_ string, resultErr error) {
	settings := catalogSettings(cfg)
	store, err := storage.Open(cfg.RuntimeStorage())
	if err != nil {
		return "", err
	}
	defer func() { resultErr = errors.Join(resultErr, store.Close()) }()
	return settings.ReadUpgradeSourceIdentity(ctx, store)
}

func (u localPathUpgrade) stageConfiguration(ctx context.Context, record *pathUpgradeRecordV1, original, replacement, operations []byte) error {
	record.OriginalDigest, record.ReplacementDigest = sha256.Sum256(original), sha256.Sum256(replacement)
	type stagedFile struct {
		name  string
		data  []byte
		limit int64
	}
	files := []stagedFile{
		{pathUpgradeSource, original, pathUpgradeConfigLimit},
		{pathUpgradeTarget, replacement, pathUpgradeConfigLimit},
	}
	if operations != nil {
		record.OperationsDigest = sha256.Sum256(operations)
		files = append(files, stagedFile{pathUpgradeOperations, operations, pathUpgradeOperationsLimit})
	}
	for _, file := range files {
		existing, err := u.root.ReadFile(file.name, file.limit)
		switch {
		case err == nil && bytes.Equal(existing, file.data):
		case errors.Is(err, fs.ErrNotExist):
			if err := u.root.CompareAndPublish(ctx, file.name, nil, file.data); err != nil {
				return err
			}
		default:
			return fmt.Errorf("staged file %s differs from the selected upgrade configuration", file.name)
		}
	}
	return nil
}

func (u localPathUpgrade) resume(ctx context.Context, cfg *config.Config) (*config.Config, bool, error) {
	if err := u.root.RecoverPublications(ctx); err != nil {
		return nil, false, err
	}
	data, err := u.root.ReadFile(pathUpgradeRecord, pathUpgradeConfigLimit)
	if errors.Is(err, fs.ErrNotExist) {
		return cfg, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	var record pathUpgradeRecordV1
	if err := json.Unmarshal(data, &record, json.RejectUnknownMembers(true)); err != nil {
		return nil, false, fmt.Errorf("invalid local path upgrade record")
	}
	if err := u.verifyPaths(record); err != nil {
		return nil, false, err
	}
	cfg, err = u.run(ctx, cfg, record, data)
	return cfg, true, err
}

func (u localPathUpgrade) verifyPaths(record pathUpgradeRecordV1) error {
	if record.Version != 1 {
		return fmt.Errorf("unsupported local path upgrade record version")
	}
	if record.SourceConfig != u.source.ConfigFile || record.TargetConfig != u.target.ConfigFile || record.BadgerPath != u.target.BadgerDir {
		return fmt.Errorf("upgrade record does not select the current local configuration and Badger paths")
	}
	if record.Identity == "" && record.SourceRuntime == "" && record.TargetRuntime == "" {
		return nil
	}
	previous, err := config.ResolveStateDirectory("")
	if err != nil {
		return err
	}
	if record.Identity == "" || record.SourceRuntime != previous || record.TargetRuntime != u.target.RuntimeDir {
		return fmt.Errorf("upgrade record does not select the current catalog runtime paths")
	}
	return nil
}

func (u localPathUpgrade) run(ctx context.Context, cfg *config.Config, record pathUpgradeRecordV1, saved []byte) (*config.Config, error) {
	migration := catalog.RuntimeMigration{
		OperationID: "default-path-upgrade", JournalRoot: filepath.Join(u.target.DataDir, pathUpgradeDirectory),
		SourceDirectory: record.SourceRuntime, TargetDirectory: record.TargetRuntime, SourceIdentity: record.Identity,
	}
	for {
		if saved == nil {
			var err error
			saved, err = u.save(ctx, record, nil)
			if err != nil {
				return nil, err
			}
		}
		next, err := u.advance(ctx, cfg, record, migration)
		if err != nil {
			return nil, err
		}
		if u.checkpoint != nil {
			if err := u.checkpoint(record.Phase); err != nil {
				return nil, err
			}
		}
		if next == "" {
			if _, err := u.root.CompareAndRemove(ctx, pathUpgradeRecord, saved); err != nil {
				return nil, err
			}
			return u.load(ctx)
		}
		record.Phase = next
		saved, err = u.save(ctx, record, saved)
		if err != nil {
			return nil, err
		}
	}
}

func (u localPathUpgrade) save(ctx context.Context, record pathUpgradeRecordV1, previous []byte) ([]byte, error) {
	data, err := json.Marshal(record)
	if err != nil {
		return nil, err
	}
	return data, u.root.CompareAndPublish(ctx, pathUpgradeRecord, previous, data)
}

func (u localPathUpgrade) advance(ctx context.Context, cfg *config.Config, record pathUpgradeRecordV1, migration catalog.RuntimeMigration) (string, error) {
	if record.Phase == prepareUpgradeRuntime || record.Phase == publishUpgradeRuntime || record.Phase == publishUpgradeConfig {
		if err := verifyUpgradeConfiguration(record.SourceConfig, record.OriginalDigest); err != nil {
			return "", err
		}
	}
	switch record.Phase {
	case prepareUpgradeRuntime:
		return publishUpgradeRuntime, prepareLocalUpgradeRuntime(ctx, cfg, migration)
	case publishUpgradeRuntime:
		_, err := runRuntimeMigration(ctx, cfg, "publish", migration)
		return publishUpgradeConfig, err
	case publishUpgradeConfig:
		if record.Identity == "" {
			return retireUpgradeConfig, u.publishConfiguration(ctx, record)
		}
		return completeUpgradeRuntime, u.publishConfiguration(ctx, record)
	case completeUpgradeRuntime:
		if err := verifyUpgradeConfiguration(record.TargetConfig, record.ReplacementDigest); err != nil {
			return "", err
		}
		selected, err := u.load(ctx)
		if err != nil {
			return "", err
		}
		_, err = MigrateRuntime(ctx, selected, "complete", migration)
		return retireUpgradeConfig, err
	case retireUpgradeConfig:
		return "", u.retireConfiguration(ctx, record)
	default:
		return "", fmt.Errorf("unknown local path upgrade phase")
	}
}

func prepareLocalUpgradeRuntime(ctx context.Context, cfg *config.Config, migration catalog.RuntimeMigration) error {
	result, err := MigrateRuntime(ctx, cfg, "prepare", migration)
	if err != nil {
		return err
	}
	if result.IdentityVerified == nil || !*result.IdentityVerified {
		return fmt.Errorf("catalog runtime ownership could not be verified")
	}
	_, err = MigrateRuntime(ctx, cfg, "stage", migration)
	return err
}

func (u localPathUpgrade) publishConfiguration(ctx context.Context, record pathUpgradeRecordV1) error {
	contents, err := u.readStaged(pathUpgradeTarget, pathUpgradeConfigLimit, record.ReplacementDigest)
	if err != nil {
		return err
	}
	var operations []byte
	if record.OperationsDigest != [32]byte{} {
		operations, err = u.readStaged(pathUpgradeOperations, pathUpgradeOperationsLimit, record.OperationsDigest)
		if err != nil {
			return err
		}
	}
	directory, err := createUpgradeDirectory(filepath.Dir(record.TargetConfig))
	if err != nil {
		return err
	}
	if err := directory.RecoverPublications(ctx); err != nil {
		return err
	}
	return config.PublishPathUpgradeConfiguration(ctx, directory, filepath.Base(record.TargetConfig), contents, operations)
}

func (u localPathUpgrade) retireConfiguration(ctx context.Context, record pathUpgradeRecordV1) error {
	selected, err := u.load(ctx)
	if err != nil {
		return err
	}
	if selected.EffectivePaths().ConfigFile != record.TargetConfig || selected.EffectivePaths().BadgerDir != record.BadgerPath {
		return fmt.Errorf("startup does not select the published configuration and existing Badger database")
	}
	if err := verifyUpgradeConfiguration(record.TargetConfig, record.ReplacementDigest); err != nil {
		return err
	}
	original, err := u.readStaged(pathUpgradeSource, pathUpgradeConfigLimit, record.OriginalDigest)
	if err != nil {
		return err
	}
	directory, err := productfiles.ExistingDirectory(filepath.Dir(record.SourceConfig))
	if err != nil {
		return err
	}
	if err := directory.RecoverPublications(ctx); err != nil {
		return err
	}
	if _, err := directory.CompareAndRemove(ctx, filepath.Base(record.SourceConfig), original); err != nil {
		return err
	}
	if err := u.removeStaged(ctx, pathUpgradeOperations, pathUpgradeOperationsLimit, record.OperationsDigest); err != nil {
		return err
	}
	return u.removeStaged(ctx, pathUpgradeTarget, pathUpgradeConfigLimit, record.ReplacementDigest)
}

func verifyUpgradeConfiguration(path string, digest [32]byte) error {
	directory, err := productfiles.ExistingDirectory(filepath.Dir(path))
	if err != nil {
		return err
	}
	contents, err := directory.ReadFile(filepath.Base(path), pathUpgradeConfigLimit)
	if err != nil {
		return err
	}
	if sha256.Sum256(contents) != digest {
		return fmt.Errorf("configuration at %q changed during the path upgrade", path)
	}
	return nil
}

func (u localPathUpgrade) readStaged(name string, limit int64, digest [32]byte) ([]byte, error) {
	data, err := u.root.ReadFile(name, limit)
	if err != nil {
		return nil, err
	}
	if sha256.Sum256(data) != digest {
		return nil, fmt.Errorf("staged file %s does not match the upgrade record", name)
	}
	return data, nil
}

func (u localPathUpgrade) removeStaged(ctx context.Context, name string, limit int64, digest [32]byte) error {
	data, err := u.readStaged(name, limit, digest)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	_, err = u.root.CompareAndRemove(ctx, name, data)
	return err
}

func createUpgradeDirectory(path string) (*productfiles.Directory, error) {
	directory, err := productfiles.NewDirectory(path)
	if err != nil {
		return nil, err
	}
	parent, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	return directory, errors.Join(productfiles.SyncDirectory(parent), parent.Close())
}
