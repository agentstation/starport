package app

import (
	"context"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/agentstation/starmap/pkg/productfiles"
	"github.com/agentstation/starport/internal/account"
	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/recovery"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
	"github.com/valkey-io/valkey-go"
)

func TestUnprefixedBackupRequiresValkeyBeforeOpeningStores(t *testing.T) {
	cfg, request := backupApplicationFixture(t)
	request.UnprefixedValkey = true
	_, err := CaptureBackup(t.Context(), cfg, request)
	require.ErrorContains(t, err, "requires configured Valkey")
	require.NoDirExists(t, request.Destination)
	_, err = CaptureBackup(t.Context(), nil, request)
	require.ErrorContains(t, err, "requires configured Valkey")
}

func TestUnprefixedBackupMigratesVerifiedRecordsUnderBarriers(t *testing.T) {
	address := os.Getenv("TEST_UNPREFIXED_VALKEY_URL")
	if address == "" {
		t.Skip("UNVERIFIED: TEST_UNPREFIXED_VALKEY_URL requires a dedicated empty database")
	}
	source, capture := backupApplicationFixture(t)
	old, err := storage.Open(source.RuntimeStorage())
	require.NoError(t, err)
	accounts, err := account.Open(old)
	require.NoError(t, err)
	_, err = accounts.EnsureDefault(t.Context())
	require.NoError(t, err)
	transfer, err := storage.OpenRecordTransfer(t.Context(), old, "")
	require.NoError(t, err)
	options, err := valkey.ParseURL(address)
	require.NoError(t, err)
	options.DisableCache = true
	options.ForceSingleClient = true
	raw, err := valkey.NewClient(options)
	require.NoError(t, err)
	t.Cleanup(raw.Close)
	var records []storage.TransferRecord
	require.NoError(t, transfer.Enumerate(t.Context(), func(record storage.TransferRecord) error {
		require.Zero(t, record.ExpiresAtMillis)
		require.NoError(t, raw.Do(t.Context(), raw.B().Set().Key(record.Key).Value(string(record.Value)).Build()).Error())
		t.Cleanup(func() {
			require.NoError(t, raw.Do(context.Background(), raw.B().Del().Key(record.Key).Build()).Error())
		})
		records = append(records, record)
		return nil
	}))
	require.NoError(t, old.Close())
	source.Storage.Mode = storage.StorageTypeValkey
	source.Storage.Valkey.URL = address
	source.Storage.Valkey.AllowInsecure = true
	_, err = CloseBackupBoundary(t.Context(), source)
	require.NoError(t, err)
	capture.UnprefixedValkey = true
	result, err := CaptureBackup(t.Context(), source, capture)
	require.NoError(t, err)
	require.True(t, result.UnprefixedValkey)
	require.EqualValues(t, len(records), result.KVRecords)
	require.EqualValues(t, 1, result.References.AccountRecords)
	verified, err := VerifyBackup(t.Context(), source, recovery.VerifyRequest{Directory: result.Directory, ManifestSHA256: result.ManifestSHA256})
	require.NoError(t, err)
	require.Equal(t, result, verified)

	parent := filepath.Join(t.TempDir(), "target")
	_, err = productfiles.CreateDirectory(parent)
	require.NoError(t, err)
	target, err := config.NewLoader().WithPaths(config.PathsForConfigDir(parent)).WithEnvFiles().WithEnvironment(map[string]string{
		"STARPORT_SECURITY_MASTER_KEY": source.Security.MasterKey, "STARPORT_DEPLOYMENT_ID": source.EffectivePaths().DeploymentID,
	}).Load(t.Context())
	require.NoError(t, err)
	parsed, err := url.Parse(address)
	require.NoError(t, err)
	require.Equal(t, "/13", parsed.Path, "fixture owns source database 13 and target database 14")
	parsed.Path = "/14"
	target.Storage.Mode = storage.StorageTypeValkey
	target.Storage.Valkey.URL = parsed.String()
	target.Storage.Valkey.AllowInsecure = true
	request := recovery.PrepareRequest{VerifyRequest: recovery.VerifyRequest{Directory: result.Directory, ManifestSHA256: result.ManifestSHA256}, FilesDirectory: filepath.Join(parent, "prepared"), Operation: recovery.RestoreOperation{ID: "namespace-migration", FencingEvidence: "incident/all-writers-fenced"}}
	prepared, err := PrepareBackup(t.Context(), target, request)
	require.NoError(t, err)
	require.False(t, prepared.Prepared.Boundary.Open)
	require.EqualValues(t, len(records), prepared.Prepared.KV.ProcessedRecords)
	again, err := PrepareBackup(t.Context(), target, request)
	require.NoError(t, err)
	require.Equal(t, prepared, again)
	_, err = storage.Open(target.RuntimeStorage())
	require.ErrorIs(t, err, storage.ErrImportRestricted)
	targetStore, err := storage.OpenValkey(target.RuntimeStorage().Valkey)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, targetStore.Close()) })
	keys, err := targetStore.ScanWithPrefix(t.Context(), "", 1000)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, targetStore.BatchDelete(context.Background(), keys)) })
	for _, record := range records {
		data, err := targetStore.Get(t.Context(), record.Key)
		require.NoError(t, err)
		require.Equal(t, record.Value, data)
		unchanged, err := raw.Do(t.Context(), raw.B().Get().Key(record.Key).Build()).AsBytes()
		require.NoError(t, err)
		require.Equal(t, record.Value, unchanged, "migration must retain the original source")
	}

	// A complete byte capture must still pass the account owner's reference checks.
	invalid := account.StoragePrefix + "account:invalid"
	require.NoError(t, raw.Do(t.Context(), raw.B().Set().Key(invalid).Value("corrupt").Build()).Error())
	t.Cleanup(func() { require.NoError(t, raw.Do(context.Background(), raw.B().Del().Key(invalid).Build()).Error()) })
	capture.Destination = filepath.Join(filepath.Dir(capture.Destination), "invalid-references")
	_, err = CaptureBackup(t.Context(), source, capture)
	require.Error(t, err)
}
