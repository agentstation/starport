package catalog

import (
	"context"
	"fmt"
	"maps"
	"path/filepath"
	"strings"

	catalogconfig "github.com/agentstation/starmap/pkg/catalogs/config"
	"github.com/agentstation/starmap/runtime"
	"github.com/agentstation/starport/internal/storage"
)

// RuntimeMigration selects a stopped runtime directory and its replacement.
// It does not move the gateway databases, catalog store, or authoring workspace.
type RuntimeMigration struct {
	OperationID     string
	SourceDirectory string
	TargetDirectory string
	JournalRoot     string
	SourceIdentity  string
	StoreSelection  string
}

// RuntimeMigrationResult reports the durable phase of a runtime directory move.
type RuntimeMigrationResult struct {
	Phase                string `json:"phase"`
	HostJournalDirectory string `json:"host_journal_directory"`
	JournalDirectory     string `json:"journal_directory"`
	TargetDirectory      string `json:"target_directory"`
	SchedulerIdentity    string `json:"scheduler_identity"`
	FileCount            int    `json:"file_count,omitempty"`
	IdentityVerified     *bool  `json:"identity_verified,omitempty"`
}

func (m RuntimeMigration) request(settings Settings) runtime.DirectoryMigrationRequest {
	return runtime.DirectoryMigrationRequest{OperationID: m.OperationID, SourceDirectory: m.SourceDirectory, TargetDirectory: m.TargetDirectory, JournalRoot: m.JournalRoot, SourceIdentity: m.SourceIdentity, Owner: settings.directoryOwner()}
}

// Prepare inventories source files without creating the target directory.
func (m RuntimeMigration) Prepare(ctx context.Context, store storage.KVStore, settings Settings) (RuntimeMigrationResult, error) {
	result, err := runtime.PrepareDirectoryMigration(ctx, m.request(settings))
	if err == nil {
		err = m.bindStore(ctx, store, settings)
	}
	return RuntimeMigrationResult{HostJournalDirectory: m.hostJournalDirectory(settings), Phase: result.Phase, JournalDirectory: result.JournalDirectory, TargetDirectory: m.TargetDirectory, SchedulerIdentity: m.SourceIdentity, FileCount: result.FileCount, IdentityVerified: new(result.IdentityVerified)}, err
}

// Stage copies and verifies private staging files without publishing the target.
func (m RuntimeMigration) Stage(ctx context.Context, store storage.KVStore, settings Settings) (RuntimeMigrationResult, error) {
	if err := m.verifyStore(ctx, store, settings); err != nil {
		return RuntimeMigrationResult{}, err
	}
	result, err := runtime.StageDirectoryMigration(ctx, m.request(settings))
	return RuntimeMigrationResult{HostJournalDirectory: m.hostJournalDirectory(settings), Phase: result.Phase, JournalDirectory: result.JournalDirectory, TargetDirectory: m.TargetDirectory, SchedulerIdentity: m.SourceIdentity, FileCount: result.FileCount, IdentityVerified: new(result.IdentityVerified)}, err
}

// Publish installs the verified target and retires the source runtime.
func (m RuntimeMigration) Publish(ctx context.Context, store storage.KVStore, settings Settings) (RuntimeMigrationResult, error) {
	if err := m.verifyStore(ctx, store, settings); err != nil {
		return RuntimeMigrationResult{}, err
	}
	result, err := runtime.PublishDirectoryMigration(ctx, m.request(settings))
	return m.migrationPublication(settings, result), err
}

// OpenReplacement verifies migration evidence before opening the selected runtime.
// The caller must retain the original catalog KV store and persist the target settings.
// Completion remains explicit after the host verifies that saved configuration.
func (m RuntimeMigration) OpenReplacement(ctx context.Context, store storage.KVStore, settings Settings) (*Runtime, error) {
	parsed, err := catalogconfig.Parse(settings.catalogValues())
	if err != nil {
		return nil, err
	}
	if parsed.StateDirectory != m.TargetDirectory || parsed.SchedulerIdentity != m.SourceIdentity {
		return nil, fmt.Errorf("runtime migration requires the target directory and retained scheduler identity")
	}
	request := m.request(settings)
	if err := runtime.VerifyDirectoryMigrationPublication(ctx, request); err != nil {
		return nil, err
	}
	if err := m.verifyStore(ctx, store, settings); err != nil {
		return nil, err
	}
	settings.Values = maps.Clone(settings.Values)
	if settings.Values == nil {
		settings.Values = make(map[string]string)
	}
	settings.Values[catalogconfig.NetworkMode] = "offline"
	settings.AcquisitionEnabled = false
	settings.SourcePollInterval = 0
	return openRuntimeWithMigration(ctx, store, settings, runtimeCollectors{}, &request)
}

// Complete records completion for an active replacement after saved selection checks.
func (m RuntimeMigration) Complete(ctx context.Context, connected *Runtime, settings Settings) (RuntimeMigrationResult, error) {
	if connected == nil || connected.runtime == nil {
		return RuntimeMigrationResult{}, fmt.Errorf("runtime migration requires an active replacement")
	}
	if err := m.verifyStore(ctx, connected.accepted.store, settings); err != nil {
		return RuntimeMigrationResult{}, err
	}
	result, err := connected.runtime.CompleteDirectoryMigration(ctx, m.request(settings))
	return m.migrationPublication(settings, result), err
}

func (m RuntimeMigration) migrationPublication(settings Settings, result runtime.DirectoryMigrationPublication) RuntimeMigrationResult {
	return RuntimeMigrationResult{HostJournalDirectory: m.hostJournalDirectory(settings), Phase: result.Phase, JournalDirectory: result.JournalDirectory, TargetDirectory: result.TargetDirectory, SchedulerIdentity: result.SchedulerIdentity}
}

func (m RuntimeMigration) hostJournalDirectory(settings Settings) string {
	return filepath.Join(m.JournalRoot, "starport-runtime", strings.TrimPrefix(m.storeReceiptKey(settings), "catalog_migration:v1:"))
}
