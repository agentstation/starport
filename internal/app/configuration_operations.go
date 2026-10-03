package app

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"os/user"
	"time"

	"github.com/agentstation/starport/internal/audit"
	runtimecatalog "github.com/agentstation/starport/internal/catalog"
	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/configrevision"
	"github.com/agentstation/starport/internal/sqlstore"
	"github.com/agentstation/starport/internal/storage"
)

// configurationOperationTimeout bounds the SQL phase and the fleet phase of
// one operator change.
const configurationOperationTimeout = time.Minute

// InitializeSharedConfiguration writes revision 1 of the shared configuration
// from the validated local deployment values. A preview validates the seed and
// reads the store without a write or a schema migration.
func InitializeSharedConfiguration(ctx context.Context, cfg *config.Config, request configrevision.Request) (result configrevision.Result, err error) {
	request.OperationID = configurationOperationID(request.OperationID)
	seed := cfg.SharedSeed()
	if request.Preview {
		store, closeStore, err := openConfigurationForRead(cfg)
		if err != nil {
			return result, err
		}
		defer func() { err = errors.Join(err, closeStore()) }()
		ctx, cancel := context.WithTimeout(ctx, configurationOperationTimeout)
		defer cancel()
		head, err := store.Preview(ctx, seed)
		if err != nil {
			return result, err
		}
		result.Revision = configrevision.Revision{Head: head, OperationID: request.OperationID}
		return result, nil
	}
	if err := refuseExternalManagement(cfg); err != nil {
		return result, err
	}
	store, closeStore, err := openConfigurationForWrite(cfg)
	if err != nil {
		return result, err
	}
	defer func() { err = errors.Join(err, closeStore()) }()
	ctx, cancel := context.WithTimeout(ctx, configurationOperationTimeout)
	defer cancel()
	revision, err := store.Initialize(ctx, seed, operatorActor(), request.OperationID)
	if err != nil {
		return result, err
	}
	return configrevision.Result{Revision: revision, Written: true}, nil
}

// MigrateConfiguration records an authority switch as a revision. To shared,
// it seeds from the validated local deployment values. A shared revision after
// a local one also writes the fleet policy record, because the record of an
// earlier shared revision would refuse every replica. To local, it records the
// final applied values and releases the shared authority.
func MigrateConfiguration(ctx context.Context, cfg *config.Config, target string, request configrevision.Request) (result configrevision.Result, err error) {
	if target != configrevision.AuthorityShared && target != configrevision.AuthorityLocal {
		return result, fmt.Errorf("migration target %q is not shared or local", target)
	}
	if request.Preview {
		return result, errors.New("configuration migration has no preview")
	}
	if err := refuseExternalManagement(cfg); err != nil {
		return result, err
	}
	request.OperationID = configurationOperationID(request.OperationID)
	store, db, closeStore, err := openConfigurationStoreForWrite(cfg)
	if err != nil {
		return result, err
	}
	defer func() { err = errors.Join(err, closeStore()) }()
	sqlCtx, cancel := context.WithTimeout(ctx, configurationOperationTimeout)
	defer cancel()
	revision, err := store.Migrate(sqlCtx, target, cfg.SharedSeed(), operatorActor(), request.OperationID)
	if err != nil {
		return result, err
	}
	result = configrevision.Result{Revision: revision, Written: true}
	if target == configrevision.AuthorityShared && revision.Sequence > 1 {
		return applyConfigurationFence(ctx, cfg, db, result)
	}
	return result, nil
}

// ApplyConfiguration commits the validated local deployment values as the next
// shared revision and fences the fleet: it writes the applied policy record,
// and the next lease acquisition or renewal compares it. A rerun with the same
// operation ID continues an apply that committed. Resume repeats only the
// fleet phase. A rollback is a new apply with the previous values.
func ApplyConfiguration(ctx context.Context, cfg *config.Config, request configrevision.ApplyRequest) (result configrevision.Result, err error) {
	if !cfg.SharedManagement() {
		return result, errors.New("configuration apply requires STARPORT_CONFIG_MANAGEMENT=shared")
	}
	if request.Resume && request.OperationID == "" {
		return result, errors.New("configuration apply resume requires the operation ID of the committed revision")
	}
	request.OperationID = configurationOperationID(request.OperationID)
	store, db, closeStore, err := openConfigurationStoreForWrite(cfg)
	if err != nil {
		return result, err
	}
	defer func() { err = errors.Join(err, closeStore()) }()
	sqlCtx, cancel := context.WithTimeout(ctx, configurationOperationTimeout)
	defer cancel()
	revision, err := commitAppliedConfiguration(sqlCtx, cfg, store, request)
	if err != nil {
		return result, err
	}
	return applyConfigurationFence(ctx, cfg, db, configrevision.Result{Revision: revision, Written: !request.Resume})
}

// commitAppliedConfiguration returns the shared revision of an apply. It
// commits a new one only when the operation ID committed nothing.
func commitAppliedConfiguration(ctx context.Context, cfg *config.Config, store *configrevision.Store, request configrevision.ApplyRequest) (configrevision.Revision, error) {
	seed := cfg.SharedSeed()
	committed, found, err := store.Operation(ctx, request.OperationID)
	if err != nil {
		return configrevision.Revision{}, err
	}
	switch {
	case found && committed.Authority != configrevision.AuthorityShared:
		return configrevision.Revision{}, configrevision.ErrOperationConflict
	case found && !request.Resume && committed.Checksum != config.SharedValuesChecksum(seed):
		return configrevision.Revision{}, configrevision.ErrOperationConflict
	case !found && request.Resume:
		return configrevision.Revision{}, fmt.Errorf("operation %q committed no shared configuration revision", request.OperationID)
	}
	head, err := store.Head(ctx)
	if err != nil {
		return configrevision.Revision{}, err
	}
	if found {
		// The fleet record of an older revision would refuse the replicas of
		// the head.
		if committed.Sequence != head.Sequence {
			return configrevision.Revision{}, fmt.Errorf("operation %q committed revision %d, and the head is revision %d. Apply or resume the head", request.OperationID, committed.Sequence, head.Sequence)
		}
		return committed, nil
	}
	if head.Authority != configrevision.AuthorityShared {
		return configrevision.Revision{}, fmt.Errorf("shared configuration revision %d released the shared authority. Run starport config migrate --to shared", head.Sequence)
	}
	return store.Commit(ctx, head.Sequence, seed, operatorActor(), request.OperationID)
}

// applyConfigurationFence writes the fleet policy record of a committed
// revision. Storage without a shared catalog lease has no fleet to fence: each
// process applies the revision when it restarts.
func applyConfigurationFence(ctx context.Context, cfg *config.Config, db *sqlstore.DB, result configrevision.Result) (configrevision.Result, error) {
	selected := cfg.RuntimeStorage()
	if selected.Type != storage.StorageTypeValkey {
		result.Fence = configrevision.FenceNotShared
		return result, nil
	}
	err := fenceConfiguration(ctx, cfg, db, selected, result.Revision)
	if errors.Is(err, runtimecatalog.ErrPolicyFenceUnavailable) {
		result.Fence = configrevision.FenceNotShared
		return result, nil
	}
	if err != nil {
		return result, fmt.Errorf("shared configuration revision %d is committed, and the fleet policy is not applied: %w. Run starport config apply --resume %s", result.Revision.Sequence, err, result.Revision.OperationID)
	}
	result.Fence = configrevision.FenceApplied
	return result, nil
}

func fenceConfiguration(ctx context.Context, cfg *config.Config, db *sqlstore.DB, selected storage.Config, revision configrevision.Revision) (err error) {
	ctx, cancel := context.WithTimeout(ctx, configurationOperationTimeout)
	defer cancel()
	kv, err := storage.Open(selected)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, kv.Close()) }()
	return runtimecatalog.ApplyPolicy(ctx, kv, db, cfg.EffectivePaths().DeploymentID, runtimecatalog.AppliedPolicy{
		Sequence: revision.Sequence, Checksum: revision.Checksum, OperationID: revision.OperationID,
	})
}

// EffectiveConfiguration reports each catalog setting and its authority.
// Under shared management it reads the stored head without a write or a schema
// migration. It never falls back to local deployment values.
func EffectiveConfiguration(ctx context.Context, cfg *config.Config) (report config.EffectiveReport, err error) {
	if !cfg.SharedManagement() {
		return cfg.EffectiveReport(), nil
	}
	store, closeStore, err := openConfigurationForRead(cfg)
	if err != nil {
		return report, err
	}
	defer func() { err = errors.Join(err, closeStore()) }()
	ctx, cancel := context.WithTimeout(ctx, configurationOperationTimeout)
	defer cancel()
	revision, err := store.Current(ctx)
	if err != nil {
		return report, err
	}
	if revision.Authority != configrevision.AuthorityShared {
		return report, fmt.Errorf("shared configuration revision %d released the shared authority. Set STARPORT_CONFIG_MANAGEMENT=local, or run starport config migrate --to shared", revision.Sequence)
	}
	if err := cfg.ApplySharedRevision(revision.Shared()); err != nil {
		return report, err
	}
	return cfg.EffectiveReport(), nil
}

// openConfigurationForRead opens the shared configuration of an existing
// relational store. A missing SQLite file is an unavailable store, never an
// empty one.
func openConfigurationForRead(cfg *config.Config) (*configrevision.Store, func() error, error) {
	if err := requirePersistentConfiguration(cfg); err != nil {
		return nil, nil, err
	}
	selected := cfg.Storage.RuntimeSQL()
	if selected.Type == sqlstore.TypeSQLite {
		info, err := os.Lstat(selected.SQLite.Path)
		if err != nil {
			return nil, nil, &configrevision.UnavailableError{Err: err}
		}
		if !info.Mode().IsRegular() {
			return nil, nil, &configrevision.UnavailableError{Err: errors.New("the relational database is not a regular file")}
		}
	}
	db, err := sqlstore.Open(selected)
	if err != nil {
		return nil, nil, &configrevision.UnavailableError{Err: err}
	}
	store, err := openConfigurationStore(cfg, db)
	if err != nil {
		return nil, nil, errors.Join(err, db.Close())
	}
	return store, db.Close, nil
}

func openConfigurationForWrite(cfg *config.Config) (*configrevision.Store, func() error, error) {
	store, _, closeStore, err := openConfigurationStoreForWrite(cfg)
	return store, closeStore, err
}

// openConfigurationStoreForWrite opens and migrates the relational store as
// startup does, so a write never lands behind an open import barrier.
func openConfigurationStoreForWrite(cfg *config.Config) (*configrevision.Store, *sqlstore.DB, func() error, error) {
	if err := requirePersistentConfiguration(cfg); err != nil {
		return nil, nil, nil, err
	}
	db, err := sqlstore.Open(cfg.Storage.RuntimeSQL())
	if err != nil {
		return nil, nil, nil, &configrevision.UnavailableError{Err: err}
	}
	ctx, cancel := context.WithTimeout(context.Background(), configurationOperationTimeout)
	defer cancel()
	if err := db.Migrate(ctx); err != nil {
		return nil, nil, nil, errors.Join(fmt.Errorf("migrate relational storage: %w", err), db.Close())
	}
	if err := db.CheckImportBarrier(ctx); err != nil {
		return nil, nil, nil, errors.Join(fmt.Errorf("relational recovery required: %w", err), db.Close())
	}
	store, err := openConfigurationStore(cfg, db)
	if err != nil {
		return nil, nil, nil, errors.Join(err, db.Close())
	}
	return store, db, db.Close, nil
}

// refuseExternalManagement refuses a configuration write when an external
// controller owns the configuration. Reads continue.
func refuseExternalManagement(cfg *config.Config) error {
	if cfg.ManagementMode() == config.ManagementExternal {
		return &config.Refusal{Reason: config.RefusalExternalManagement, Message: "an external controller manages this configuration. Change it through that controller"}
	}
	return nil
}

func requirePersistentConfiguration(cfg *config.Config) error {
	if cfg == nil || cfg.RuntimeStorage().Badger.InMemory {
		return errors.New("shared configuration requires persistent deployment configuration")
	}
	return nil
}

// configurationOperationID returns the operator's ID, or a new one. The
// output names it so a partial apply can resume.
func configurationOperationID(operationID string) string {
	if operationID != "" {
		return operationID
	}
	return "config-" + rand.Text()
}

// operatorActor names the operating system user that ran a command.
func operatorActor() string {
	current, err := user.Current()
	if err != nil || current.Username == "" {
		return audit.ActorOperatorPrefix + "unknown"
	}
	return audit.ActorOperatorPrefix + current.Username
}
