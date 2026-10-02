package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/agentstation/starport/internal/audit"
	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/configrevision"
	"github.com/agentstation/starport/internal/credentials"
	"github.com/agentstation/starport/internal/sqlstore"
)

const (
	// configurationReadTimeout bounds one read of the shared configuration.
	configurationReadTimeout = 30 * time.Second
	// configurationObservationInterval is the wait between reads of the
	// shared configuration head after startup.
	configurationObservationInterval = 30 * time.Second
)

// openConfigurationStore binds the relational store to the shared
// configuration of this deployment. It reads and writes nothing.
func openConfigurationStore(cfg *config.Config, db *sqlstore.DB) (*configrevision.Store, error) {
	trail, err := audit.Open(db, cfg.Audit.RetentionWindow())
	if err != nil {
		return nil, fmt.Errorf("open audit repository: %w", err)
	}
	var sealer configrevision.Sealer
	if strings.TrimSpace(cfg.Security.MasterKey) != "" {
		key := []byte(cfg.Security.MasterKey)
		if len(key) < 32 {
			key = credentials.DeriveKeyFromPassword(cfg.Security.MasterKey)
		}
		encryption, err := credentials.NewEncryptionService(key)
		if err != nil {
			return nil, err
		}
		sealer = encryption
	}
	return configrevision.New(db, trail, sealer, cfg.EffectivePaths().DeploymentID, cfg.ConfigNamespace())
}

// openConfigurationAuthority selects the deployment configuration authority
// before any adapter reads catalog settings. Shared management applies the
// stored head. An absent head, an unavailable store, a different namespace,
// and a sealed credential that the master key cannot open each refuse
// startup. No path falls back to local deployment values.
func (b *runtimeBuilder) openConfigurationAuthority() error {
	cfg := b.config
	if !cfg.SharedManagement() {
		log.Info().Str("management", cfg.ManagementMode()).Msg("deployment configuration authority selected")
		return nil
	}
	store, err := openConfigurationStore(cfg, b.sqlDB)
	if err != nil {
		return fmt.Errorf("open shared configuration: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), configurationReadTimeout)
	defer cancel()
	revision, err := store.Current(ctx)
	if err != nil {
		return fmt.Errorf("read shared configuration: %w", err)
	}
	if revision.Authority != configrevision.AuthorityShared {
		return fmt.Errorf("shared configuration revision %d released the shared authority. Set STARPORT_CONFIG_MANAGEMENT=local, or run starport config migrate --to shared", revision.Sequence)
	}
	if err := cfg.ApplySharedRevision(revision.Shared()); err != nil {
		return fmt.Errorf("apply shared configuration: %w", err)
	}
	if err := b.validateCatalogStorage(); err != nil {
		return err
	}
	b.application.configuration = store
	applied := cfg.AppliedRevision()
	log.Info().Str("management", applied.Authority).Str("namespace", applied.Namespace).
		Int64("desired_revision", applied.Desired).Int64("applied_revision", applied.Applied).
		Str("checksum", applied.Checksum).Msg("shared configuration revision applied")
	return nil
}

// observeConfigurationRevision records the newest stored head as the desired
// revision. It never applies it: a restart applies a new revision. A failed
// read keeps the applied revision and marks it retained.
func (a *App) observeConfigurationRevision(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, configurationReadTimeout)
	defer cancel()
	head, err := a.configuration.Head(ctx)
	if err != nil {
		a.config.RetainAppliedRevision()
		return err
	}
	a.config.ObserveDesiredRevision(head.Sequence)
	return nil
}

// configurationObservationLoop reports the drift between the desired and the
// applied revision. Requests never wait for it.
func (a *App) configurationObservationLoop(ctx context.Context) {
	failing := false
	reported := a.config.AppliedRevision().Applied
	for ctx.Err() == nil {
		timer := time.NewTimer(configurationObservationInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		err := a.observeConfigurationRevision(ctx)
		if ctx.Err() != nil {
			return
		}
		// Storage errors can contain connection details. Log only transitions.
		if err != nil && !failing {
			log.Warn().Msg("shared configuration observation failed; this process keeps its applied revision")
		} else if err == nil && failing {
			log.Info().Msg("shared configuration observation recovered")
		}
		failing = err != nil
		if revision := a.config.AppliedRevision(); revision.Desired > reported {
			reported = revision.Desired
			log.Warn().Int64("desired_revision", revision.Desired).Int64("applied_revision", revision.Applied).
				Msg("a newer shared configuration revision is committed; restart this process to apply it")
		}
	}
}
