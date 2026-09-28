package recovery

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

func privateKVDirectory(t *testing.T) string {
	t.Helper()
	path, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	require.NoError(t, os.Chmod(path, 0o700))
	return path
}

func kvTransferStores(t *testing.T, kind string) (storage.KVStore, storage.RecordTransfer, storage.Config) {
	t.Helper()
	config := storage.Config{Type: kind}
	var store storage.KVStore
	var err error
	if kind == storage.StorageTypeBadger {
		config.Badger = storage.BadgerConfig{Path: privateKVDirectory(t), SyncWrites: true, NumVersions: 1, MemTableSize: 8 << 20}
		store, err = storage.OpenBadger(config.Badger)
	} else {
		address := os.Getenv("TEST_VALKEY_URL")
		if address == "" {
			t.Skip("UNVERIFIED: TEST_VALKEY_URL is not set")
		}
		config.Valkey = storage.ValkeyConfig{URL: address, DeploymentID: "kv-snapshot-" + rand.Text(), AllowInsecure: true}
		store, err = storage.OpenValkey(config.Valkey)
	}
	require.NoError(t, err)
	t.Cleanup(func() {
		if kind == storage.StorageTypeValkey {
			keys, err := store.ScanWithPrefix(context.Background(), "", 10000)
			require.NoError(t, err)
			if len(keys) > 0 {
				require.NoError(t, store.BatchDelete(context.Background(), keys))
			}
		}
		require.NoError(t, store.Close())
	})
	identity := ""
	if provider, ok := store.(storage.IncarnationProvider); ok {
		identity, err = provider.ObserveIncarnation(t.Context())
		require.NoError(t, err)
	}
	transfer, err := storage.OpenRecordTransfer(t.Context(), store, identity)
	require.NoError(t, err)
	return store, transfer, config
}

func kvSnapshotFixture(t *testing.T) (string, KVSnapshot) {
	t.Helper()
	store, source, _ := kvTransferStores(t, storage.StorageTypeBadger)
	require.NoError(t, store.Set(t.Context(), "account:one", []byte("encrypted-credentials")))
	require.NoError(t, store.Set(t.Context(), "budget:uncertain", []byte("9223372036854775807")))
	destination := filepath.Join(privateKVDirectory(t), "snapshot")
	receipt, err := SnapshotKV(t.Context(), source, destination)
	require.NoError(t, err)
	return KVSnapshotPath(destination), receipt
}

func TestKVSnapshotTransfersAllBackendPairs(t *testing.T) {
	for _, sourceKind := range []string{storage.StorageTypeBadger, storage.StorageTypeValkey} {
		for _, targetKind := range []string{storage.StorageTypeBadger, storage.StorageTypeValkey} {
			t.Run(sourceKind+"_to_"+targetKind, func(t *testing.T) {
				source, reader, _ := kvTransferStores(t, sourceKind)
				target, writer, cfg := kvTransferStores(t, targetKind)
				values := map[string][]byte{"authorization:withdrawn": []byte("denied"), "budget:uncertain": []byte("9223372036854775807"), "catalog:head": []byte{0, 255, 1}, "empty": {}, "utf8:模型\x00": []byte("provider/account-reference")}
				for key, value := range values {
					require.NoError(t, source.Set(t.Context(), key, value))
				}
				require.NoError(t, source.SetWithTTL(t.Context(), "expires", []byte("unchanged deadline"), time.Hour+321*time.Millisecond))
				var expiry int64
				require.NoError(t, reader.Enumerate(t.Context(), func(r storage.TransferRecord) error {
					if r.Key == "expires" {
						expiry = r.ExpiresAtMillis
					}
					return nil
				}))
				destination := filepath.Join(privateKVDirectory(t), "snapshot")
				receipt, err := SnapshotKV(t.Context(), reader, destination)
				require.NoError(t, err)
				require.Equal(t, int64(len(values)+1), receipt.Records)
				result, err := ImportKV(t.Context(), writer, "move", KVSnapshotPath(destination), privateKVDirectory(t), receipt)
				require.NoError(t, err)
				require.Equal(t, receipt.Records, result.ProcessedRecords)
				wantRounded := int64(0)
				if expiry%writer.ExpiryResolution().Milliseconds() != 0 {
					wantRounded = 1
				}
				require.Equal(t, wantRounded, result.RoundedExpirations)
				for key, value := range values {
					actual, err := target.Get(t.Context(), key)
					require.NoError(t, err)
					require.Equal(t, value, actual)
				}
				lifetime, err := target.GetTTL(t.Context(), "expires")
				require.NoError(t, err)
				require.LessOrEqual(t, lifetime, time.Until(time.UnixMilli(expiry))+time.Second)
				require.Greater(t, lifetime, 59*time.Minute)
				require.ErrorIs(t, storage.CheckImportBarrier(t.Context(), target), storage.ErrImportRestricted)
				if targetKind == storage.StorageTypeValkey {
					_, err := storage.Open(cfg)
					require.ErrorIs(t, err, storage.ErrImportRestricted)
				}
				again, err := ImportKV(t.Context(), writer, "move", KVSnapshotPath(destination), privateKVDirectory(t), receipt)
				require.NoError(t, err)
				require.Equal(t, result, again)
				_, err = ImportKV(t.Context(), writer, "different", KVSnapshotPath(destination), privateKVDirectory(t), receipt)
				require.ErrorIs(t, err, storage.ErrConflict)
				// Source bytes and permission state are unchanged by collection and import.
				require.NoError(t, storage.CheckImportBarrier(t.Context(), source))
				_, err = SnapshotKV(t.Context(), reader, destination)
				require.Error(t, err)
			})
		}
	}
}

type replayRecordSource struct {
	storage.RecordTransfer
	change bool
}

func (r replayRecordSource) Enumerate(ctx context.Context, yield func(storage.TransferRecord) error) error {
	return r.RecordTransfer.Enumerate(ctx, func(record storage.TransferRecord) error {
		if err := yield(record); err != nil {
			return err
		}
		if r.change {
			record.ExpiresAtMillis++
		}
		return yield(record)
	})
}

func TestKVSnapshotDeduplicatesOnlyIdenticalScanReplay(t *testing.T) {
	store, source, _ := kvTransferStores(t, storage.StorageTypeBadger)
	require.NoError(t, store.Set(t.Context(), "key", []byte("data")))
	for _, changed := range []bool{false, true} {
		t.Run(map[bool]string{false: "identical", true: "changed"}[changed], func(t *testing.T) {
			destination := filepath.Join(privateKVDirectory(t), "snapshot")
			receipt, err := SnapshotKV(t.Context(), replayRecordSource{RecordTransfer: source, change: changed}, destination)
			if changed {
				require.ErrorContains(t, err, "source changed")
				require.Empty(t, receipt)
				_, err = os.Stat(filepath.Join(destination, "snapshot.json"))
				require.ErrorIs(t, err, os.ErrNotExist)
			} else {
				require.NoError(t, err)
				require.Equal(t, int64(1), receipt.Records)
			}
		})
	}
}

func hashKVFixture(t *testing.T, path string, receipt KVSnapshot) KVSnapshot {
	t.Helper()
	file, err := os.Open(path)
	require.NoError(t, err)
	defer file.Close()
	hash := sha256.New()
	size, err := io.Copy(hash, file)
	require.NoError(t, err)
	receipt.Size = size
	receipt.SHA256 = hex.EncodeToString(hash.Sum(nil))
	return receipt
}

func TestKVSnapshotRejectsInvalidImageBeforeClaim(t *testing.T) {
	for _, damage := range []string{"digest", "size", "count", "schema", "oversize", "reserved-key", "expiry", "corruption"} {
		t.Run(damage, func(t *testing.T) {
			path, receipt := kvSnapshotFixture(t)
			switch damage {
			case "digest":
				receipt.SHA256 = strings.Repeat("0", 64)
			case "size":
				receipt.Size++
			case "count":
				receipt.Records++
			case "corruption":
				require.NoError(t, os.WriteFile(path, []byte("not SQLite"), 0o600))
				receipt = hashKVFixture(t, path, receipt)
			default:
				db, err := openKVSnapshot(path, false)
				require.NoError(t, err)
				query := map[string]string{"schema": "CREATE TABLE extra(value TEXT)", "oversize": "UPDATE records SET value=zeroblob(67108865)", "reserved-key": "UPDATE records SET key=cast('!badger!private' AS BLOB) WHERE key=cast('account:one' AS BLOB)", "expiry": "UPDATE records SET expires=-1"}[damage]
				_, err = db.ExecContext(t.Context(), query)
				require.NoError(t, err)
				require.NoError(t, db.Close())
				receipt = hashKVFixture(t, path, receipt)
			}
			target, writer, _ := kvTransferStores(t, storage.StorageTypeBadger)
			_, err := ImportKV(t.Context(), writer, "invalid", path, privateKVDirectory(t), receipt)
			require.Error(t, err)
			require.NoError(t, storage.CheckImportBarrier(t.Context(), target))
			keys, err := target.ScanWithPrefix(t.Context(), "", 10)
			require.NoError(t, err)
			require.Empty(t, keys)
		})
	}
}

type interruptedKVImport struct {
	storage.RecordTransfer
	remaining int
	lostAck   bool
}

func (i *interruptedKVImport) Import(ctx context.Context, claim []byte, record storage.TransferRecord) error {
	if i.remaining == 0 {
		return context.Canceled
	}
	i.remaining--
	if err := i.RecordTransfer.Import(ctx, claim, record); err != nil {
		return err
	}
	if i.lostAck {
		return context.DeadlineExceeded
	}
	return nil
}

func TestKVSnapshotInterruptedImportRetainsBarrierAndExactRetry(t *testing.T) {
	for _, kind := range []string{storage.StorageTypeBadger, storage.StorageTypeValkey} {
		for _, lost := range []bool{false, true} {
			t.Run(kind+map[bool]string{false: "_interrupted", true: "_lost_ack"}[lost], func(t *testing.T) {
				path, receipt := kvSnapshotFixture(t)
				target, writer, _ := kvTransferStores(t, kind)
				fault := &interruptedKVImport{RecordTransfer: writer, remaining: 1, lostAck: lost}
				_, err := ImportKV(t.Context(), fault, "same", path, privateKVDirectory(t), receipt)
				require.Error(t, err)
				require.ErrorIs(t, storage.CheckImportBarrier(t.Context(), target), storage.ErrImportRestricted)
				value, err := target.Get(t.Context(), "account:one")
				require.NoError(t, err)
				require.Equal(t, []byte("encrypted-credentials"), value)
				_, err = ImportKV(t.Context(), writer, "other", path, privateKVDirectory(t), receipt)
				require.ErrorIs(t, err, storage.ErrConflict)
				result, err := ImportKV(t.Context(), writer, "same", path, privateKVDirectory(t), receipt)
				require.NoError(t, err)
				require.Equal(t, receipt.Records, result.ProcessedRecords)
			})
		}
	}
}

func TestKVSnapshotConcurrentImportsChooseOneOperation(t *testing.T) {
	for _, kind := range []string{storage.StorageTypeBadger, storage.StorageTypeValkey} {
		t.Run(kind, func(t *testing.T) {
			path, receipt := kvSnapshotFixture(t)
			_, writer, _ := kvTransferStores(t, kind)
			results := make(chan error, 2)
			var wg sync.WaitGroup
			for _, operation := range []string{"one", "two"} {
				scratch := privateKVDirectory(t)
				wg.Go(func() { _, err := ImportKV(t.Context(), writer, operation, path, scratch, receipt); results <- err })
			}
			wg.Wait()
			close(results)
			winners := 0
			for err := range results {
				if err == nil {
					winners++
				} else {
					require.True(t, errors.Is(err, storage.ErrConflict) || errors.Is(err, storage.ErrImportRestricted))
				}
			}
			require.Equal(t, 1, winners)
		})
	}
}

func TestKVSnapshotProcessLoss(t *testing.T) {
	if path := os.Getenv("STARPORT_KV_IMPORT_CHILD"); path != "" {
		store, err := storage.OpenBadger(storage.BadgerConfig{Path: path, SyncWrites: true, MemTableSize: 8 << 20})
		require.NoError(t, err)
		writer, err := storage.OpenRecordTransfer(t.Context(), store, "")
		require.NoError(t, err)
		require.NoError(t, writer.Claim(t.Context(), []byte("process-loss")))
		require.NoError(t, writer.Import(t.Context(), []byte("process-loss"), storage.TransferRecord{Key: "reservation", Value: []byte("uncertain")}))
		os.Exit(77)
	}
	path := privateKVDirectory(t)
	cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestKVSnapshotProcessLoss$")
	cmd.Env = append(os.Environ(), "STARPORT_KV_IMPORT_CHILD="+path)
	output, err := cmd.CombinedOutput()
	var exit *exec.ExitError
	require.ErrorAs(t, err, &exit, string(output))
	require.Equal(t, 77, exit.ExitCode())
	config := storage.Config{Type: storage.StorageTypeBadger, Badger: storage.BadgerConfig{Path: path, SyncWrites: true, NumVersions: 1, MemTableSize: 8 << 20}}
	_, err = storage.Open(config)
	require.ErrorIs(t, err, storage.ErrImportRestricted)
	store, err := storage.OpenBadger(config.Badger)
	require.NoError(t, err)
	defer store.Close()
	value, err := store.Get(t.Context(), "reservation")
	require.NoError(t, err)
	require.Equal(t, []byte("uncertain"), value)
}

func TestKVSnapshotEmptyImageAndCanceledImport(t *testing.T) {
	_, source, _ := kvTransferStores(t, storage.StorageTypeBadger)
	destination := filepath.Join(privateKVDirectory(t), "empty")
	receipt, err := SnapshotKV(t.Context(), source, destination)
	require.NoError(t, err)
	require.Zero(t, receipt.Records)
	target, writer, _ := kvTransferStores(t, storage.StorageTypeBadger)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = ImportKV(ctx, writer, "empty", KVSnapshotPath(destination), privateKVDirectory(t), receipt)
	require.ErrorIs(t, err, context.Canceled)
	require.NoError(t, storage.CheckImportBarrier(t.Context(), target))
	result, err := ImportKV(t.Context(), writer, "empty", KVSnapshotPath(destination), privateKVDirectory(t), receipt)
	require.NoError(t, err)
	require.Zero(t, result.ProcessedRecords)
	require.ErrorIs(t, storage.CheckImportBarrier(t.Context(), target), storage.ErrImportRestricted)
}

func TestKVSnapshotReadsEveryNativeScanPage(t *testing.T) {
	store, source, _ := kvTransferStores(t, storage.StorageTypeValkey)
	values := make(map[string][]byte, 1500)
	for i := range 1500 {
		values[fmt.Sprintf("record:%04d", i)] = []byte{byte(i % 251)}
	}
	require.NoError(t, store.BatchSet(t.Context(), values))
	destination := filepath.Join(privateKVDirectory(t), "pages")
	receipt, err := SnapshotKV(t.Context(), source, destination)
	require.NoError(t, err)
	require.Equal(t, int64(len(values)), receipt.Records)
	seen := 0
	require.NoError(t, visitKVSnapshot(t.Context(), KVSnapshotPath(destination), func(record storage.TransferRecord) error {
		require.Equal(t, values[record.Key], record.Value)
		seen++
		return nil
	}))
	require.Equal(t, len(values), seen)
}
