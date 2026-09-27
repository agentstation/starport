package app

import (
	"context"
	"errors"
	"time"

	"github.com/agentstation/starport/internal/catalog/recovery"
	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/sqlstore"
	"github.com/agentstation/starport/internal/storage"
)

// InitializeFleet approves fresh shared storage without starting a gateway or source refresh.
func InitializeFleet(ctx context.Context, cfg *config.Config, request recovery.FreshRequest) (record recovery.Record, err error) {
	if err := request.Validate(); err != nil {
		return record, err
	}
	if cfg == nil || cfg.Storage.Mode != storage.StorageTypeValkey || cfg.Storage.SQL.Mode != sqlstore.TypePostgres || cfg.Storage.Valkey.ClusterMode {
		return record, errors.New("fresh fleet initialization requires standalone Valkey and PostgreSQL")
	}
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	store, err := storage.Open(cfg.RuntimeStorage())
	if err != nil {
		return record, err
	}
	defer func() { err = errors.Join(err, store.Close()) }()
	backend, ok := store.(storage.FreshDatabase)
	if !ok {
		return record, errors.New("configured storage cannot prove fresh initialization")
	}
	db, err := sqlstore.Open(cfg.Storage.RuntimeSQL())
	if err != nil {
		return record, err
	}
	defer func() { err = errors.Join(err, db.Close()) }()
	if err := db.CheckFresh(ctx); err != nil {
		return record, err
	}
	if err := db.Migrate(ctx); err != nil {
		return record, err
	}
	witness, err := recovery.New(db)
	if err != nil {
		return record, err
	}
	return witness.InitializeFresh(ctx, backend, cfg.EffectivePaths().DeploymentID, request)
}
