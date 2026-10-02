package config

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

// loadLocalConfig loads one configuration file from a private directory, as
// setup creates it.
func loadLocalConfig(t *testing.T, environment map[string]string, file string) (*Config, string) {
	t.Helper()
	paths := PathsForConfigDir(filepath.Join(t.TempDir(), "starport"))
	require.NoError(t, os.MkdirAll(paths.ConfigDir, 0o700))
	selected := filepath.Join(paths.ConfigDir, "selected.env")
	require.NoError(t, os.WriteFile(selected, []byte(file), 0o600))
	cfg, err := NewLoader().WithPaths(paths).WithEnvironment(environment).WithEnvFiles(selected).Load(t.Context())
	require.NoError(t, err)
	return cfg, selected
}

type recordedAudit struct {
	mu      sync.Mutex
	subject []string
	fail    error
}

func (a *recordedAudit) record(_ context.Context, actor, action, subject string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.fail != nil {
		return a.fail
	}
	a.subject = append(a.subject, actor+" "+action+" "+subject)
	return nil
}

func (a *recordedAudit) records() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string(nil), a.subject...)
}

func localSave(cfg *Config, operationID, expected string, edits map[string]*string) FieldSave {
	return FieldSave{OperationID: operationID, DeploymentID: cfg.EffectivePaths().DeploymentID, ExpectedRevision: expected, Edits: edits}
}

func value(text string) *string { return &text }

func requireRefusal(t *testing.T, err error, reason string) *Refusal {
	t.Helper()
	var refusal *Refusal
	require.ErrorAs(t, err, &refusal)
	require.Equal(t, reason, refusal.Reason, refusal.Message)
	return refusal
}

func TestLocalConfigurationSaveRefusesStaleRevision(t *testing.T) {
	original := "# operator settings\nSTARPORT_CATALOG_ACQUISITION_INTERVAL=7m\n"
	cfg, file := loadLocalConfig(t, nil, original)
	loaded := cfg.AppliedRevision().FileChecksum
	require.Equal(t, fileRevision([]byte(original)), loaded)
	writer := NewLocalWriter(cfg, nil)
	edits := map[string]*string{"catalog_acquisition_interval": value("9m")}

	// A revision that never existed refuses with both revisions and no write.
	_, err := writer.Save(t.Context(), localSave(cfg, "op-unknown", fileRevision([]byte("other")), edits), "operator:test")
	refusal := requireRefusal(t, err, RefusalStaleRevision)
	require.Equal(t, fileRevision([]byte("other")), refusal.Expected)
	require.Equal(t, loaded, refusal.Current)
	requireFile(t, file, original)

	// The first save at the loaded revision publishes. The process still serves the loaded file.
	receipt, err := writer.Save(t.Context(), localSave(cfg, "op-first", loaded, edits), "operator:test")
	require.NoError(t, err)
	saved := "# operator settings\nSTARPORT_CATALOG_ACQUISITION_INTERVAL='9m'\n"
	requireFile(t, file, saved)
	require.Equal(t, OperationSaved, receipt.Status)
	require.Equal(t, ReceiptRevision{Revision: fileRevision([]byte(saved)), Checksum: fileRevision([]byte(saved))}, receipt.Saved)
	require.Equal(t, ReceiptRevision{Revision: loaded, Checksum: loaded}, receipt.Applied)
	require.Equal(t, []string{"catalog_acquisition_interval"}, receipt.Settings)

	// A second writer that read the loaded revision is now stale.
	_, err = writer.Save(t.Context(), localSave(cfg, "op-second", loaded, map[string]*string{"catalog_acquisition_interval": value("11m")}), "operator:test")
	refusal = requireRefusal(t, err, RefusalStaleRevision)
	require.Equal(t, loaded, refusal.Expected)
	require.Equal(t, fileRevision([]byte(saved)), refusal.Current)
	requireFile(t, file, saved)

	// An edit outside the writer also makes the expected revision stale.
	edited := saved + "STARPORT_CATALOG_SOURCE_MAX_HOPS=4\n"
	require.NoError(t, os.WriteFile(file, []byte(edited), 0o600))
	_, err = writer.Save(t.Context(), localSave(cfg, "op-third", fileRevision([]byte(saved)), edits), "operator:test")
	refusal = requireRefusal(t, err, RefusalStaleRevision)
	require.Equal(t, fileRevision([]byte(edited)), refusal.Current)
	requireFile(t, file, edited)

	// Validation reports the same refusal and writes nothing.
	validation, err := writer.Validate(t.Context(), localSave(cfg, "", fileRevision([]byte(saved)), edits))
	require.NoError(t, err)
	require.False(t, validation.Valid)
	require.Equal(t, RefusalStaleRevision, validation.Refusal.Reason)
	requireFile(t, file, edited)
}

func TestLocalConfigurationSaveRetryCompletesFromPublishedChecksum(t *testing.T) {
	original := "STARPORT_CATALOG_ACQUISITION_INTERVAL=7m\nSTARMAP_CATALOG_SOURCE_POLL_INTERVAL=3m\n"
	cfg, file := loadLocalConfig(t, nil, original)
	loaded := cfg.AppliedRevision().FileChecksum
	audit := &recordedAudit{}
	writer := NewLocalWriter(cfg, audit.record)
	stopped := errors.New("process stopped after publish")
	writer.afterPublish = func() error { return stopped }
	edits := map[string]*string{
		"catalog_acquisition_interval": value("9m"),
		"catalog_source_poll_interval": nil,
	}
	request := localSave(cfg, "op-retry", loaded, edits)

	_, err := writer.Save(t.Context(), request, "operator:test")
	require.ErrorIs(t, err, stopped)
	published := "STARPORT_CATALOG_ACQUISITION_INTERVAL='9m'\n"
	requireFile(t, file, published)
	require.Empty(t, audit.records(), "the audit record follows the completed publish")

	// The receipt of a published but pending operation reads from the file checksum.
	restarted := NewLocalWriter(cfg, audit.record)
	pending, found, err := restarted.Receipt(t.Context(), "op-retry")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, OperationSaved, pending.Status)
	require.Nil(t, pending.Refusal)

	// The retry completes from the published checksum without a second write.
	receipt, err := restarted.Save(t.Context(), request, "operator:test")
	require.NoError(t, err)
	requireFile(t, file, published)
	require.Equal(t, OperationSaved, receipt.Status)
	require.Equal(t, fileRevision([]byte(published)), receipt.Saved.Revision)
	require.Equal(t, loaded, receipt.Applied.Revision)
	require.Equal(t, []string{"catalog_acquisition_interval", "catalog_source_poll_interval"}, receipt.Settings)
	require.Empty(t, receipt.AuditError)
	records := audit.records()
	require.Equal(t, []string{"operator:test config.save local:" + receipt.Saved.Revision + " settings=catalog_acquisition_interval,catalog_source_poll_interval"}, records)
	require.NotContains(t, records[0], "9m")

	// A second retry returns the original receipt and records nothing new.
	again, err := restarted.Save(t.Context(), request, "operator:other")
	require.NoError(t, err)
	require.Equal(t, receipt, again)
	require.Len(t, audit.records(), 1)
	stored, found, err := restarted.Receipt(t.Context(), "op-retry")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, receipt, stored)

	// The same operation ID with other edits refuses.
	_, err = restarted.Save(t.Context(), localSave(cfg, "op-retry", loaded, map[string]*string{"catalog_acquisition_interval": value("10m")}), "operator:test")
	requireRefusal(t, err, RefusalOperationConflict)

	// A failed audit record stays in the receipt. The file keeps the published revision.
	audit.fail = errors.New("audit store closed")
	failed, err := restarted.Save(t.Context(), localSave(cfg, "op-audit", fileRevision([]byte(published)), map[string]*string{"catalog_source_max_hops": value("4")}), "operator:test")
	require.NoError(t, err)
	require.NotEmpty(t, failed.AuditError)
	final := published + "STARPORT_CATALOG_SOURCE_MAX_HOPS='4'\n"
	requireFile(t, file, final)
	require.Equal(t, fileRevision([]byte(final)), failed.Saved.Revision)

	// A journal operation that never published reports an incomplete receipt.
	unpublished := NewLocalWriter(cfg, nil)
	unpublished.afterPublish = func() error { return stopped }
	_, err = unpublished.Save(t.Context(), localSave(cfg, "op-lost", fileRevision([]byte(final)), map[string]*string{"catalog_source_max_hops": value("5")}), "operator:test")
	require.ErrorIs(t, err, stopped)
	require.NoError(t, os.WriteFile(file, []byte(final), 0o600))
	lost, found, err := unpublished.Receipt(t.Context(), "op-lost")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, OperationRefused, lost.Status)
	require.Equal(t, RefusalIncomplete, lost.Refusal.Reason)
}

func TestLocalConfigurationSaveRefusals(t *testing.T) {
	cfg, file := loadLocalConfig(t, map[string]string{"STARPORT_CATALOG_SOURCE_MAX_HOPS": "3"}, "STARPORT_CATALOG_ACQUISITION_INTERVAL=7m\n")
	loaded := cfg.AppliedRevision().FileChecksum
	writer := NewLocalWriter(cfg, nil)
	for name, test := range map[string]struct {
		request FieldSave
		reason  string
	}{
		"management":   {localSave(cfg, "op", loaded, map[string]*string{"config_management": value("shared")}), RefusalMigrationBoundary},
		"environment":  {localSave(cfg, "op", loaded, map[string]*string{"catalog_source_max_hops": value("4")}), RefusalInvalidEdit},
		"node scope":   {localSave(cfg, "op", loaded, map[string]*string{"catalog_workspace_path": value("/tmp/x")}), RefusalInvalidEdit},
		"unknown":      {localSave(cfg, "op", loaded, map[string]*string{"catalog_unknown": value("x")}), RefusalInvalidEdit},
		"invalid":      {localSave(cfg, "op", loaded, map[string]*string{"catalog_acquisition_interval": value("soon")}), RefusalInvalidEdit},
		"quote":        {localSave(cfg, "op", loaded, map[string]*string{"catalog_source_api_key": value("se'cret")}), RefusalInvalidEdit},
		"foreign":      {FieldSave{OperationID: "op", DeploymentID: "other", ExpectedRevision: loaded, Edits: map[string]*string{"catalog_acquisition_interval": value("9m")}}, RefusalForeignDeployment},
		"operation id": {localSave(cfg, " op", loaded, map[string]*string{"catalog_acquisition_interval": value("9m")}), RefusalInvalidEdit},
		"empty":        {localSave(cfg, "op", loaded, nil), RefusalInvalidEdit},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := writer.Save(t.Context(), test.request, "operator:test")
			refusal := requireRefusal(t, err, test.reason)
			require.NotContains(t, refusal.Error(), "se'cret")
			requireFile(t, file, "STARPORT_CATALOG_ACQUISITION_INTERVAL=7m\n")
		})
	}

	external, externalFile := loadLocalConfig(t, map[string]string{managementEnvironment: ManagementExternal}, "STARPORT_CATALOG_ACQUISITION_INTERVAL=7m\n")
	_, err := NewLocalWriter(external, nil).Save(t.Context(), localSave(external, "op", external.AppliedRevision().FileChecksum, map[string]*string{"catalog_acquisition_interval": value("9m")}), "operator:test")
	requireRefusal(t, err, RefusalExternalManagement)
	requireFile(t, externalFile, "STARPORT_CATALOG_ACQUISITION_INTERVAL=7m\n")
}

func TestEditDotenvKeepsUnrelatedLines(t *testing.T) {
	current := "# comment\nexport STARPORT_CATALOG_ACQUISITION_INTERVAL=7m\nSTARPORT_PORT=8080\nSTARPORT_CATALOG_ACQUISITION_INTERVAL=8m\nSTARMAP_CATALOG_ACQUISITION_INTERVAL=6m"
	edited, err := editDotenv([]byte(current), []FieldEdit{{Name: "STARMAP_CATALOG_ACQUISITION_INTERVAL", Key: "catalog_acquisition_interval", Value: value("9m")}})
	require.NoError(t, err)
	require.Equal(t, "# comment\nSTARPORT_CATALOG_ACQUISITION_INTERVAL='9m'\nSTARPORT_PORT=8080\n", string(edited))

	removed, err := editDotenv(edited, []FieldEdit{{Name: "STARMAP_CATALOG_ACQUISITION_INTERVAL", Key: "catalog_acquisition_interval"}})
	require.NoError(t, err)
	require.Equal(t, "# comment\nSTARPORT_PORT=8080\n", string(removed))

	appended, err := editDotenv([]byte("STARPORT_PORT=8080"), []FieldEdit{{Name: "STARMAP_CATALOG_SOURCE_TOKEN", Key: "catalog_source_token", Value: value(`$HOME\x`)}})
	require.NoError(t, err)
	require.Equal(t, "STARPORT_PORT=8080\nSTARPORT_CATALOG_SOURCE_TOKEN='$HOME\\x'\n", string(appended))

	// A multi-line value that hides an assignment refuses instead of corrupting the file.
	_, err = editDotenv([]byte("STARPORT_NOTE=\"line\nSTARPORT_CATALOG_ACQUISITION_INTERVAL=1m\"\n"), []FieldEdit{{Name: "STARMAP_CATALOG_ACQUISITION_INTERVAL", Key: "catalog_acquisition_interval", Value: value("9m")}})
	requireRefusal(t, err, RefusalInvalidEdit)
}

func requireFile(t *testing.T, path, want string) {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, want, string(data))
	require.False(t, strings.Contains(string(data), "\r"))
}
