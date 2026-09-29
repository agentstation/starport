package recovery

import (
	"encoding/base64"
	"encoding/json/v2"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/files"
	"github.com/agentstation/starport/internal/limits/storedbytes"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

func TestBackupStoredByteReferences(t *testing.T) {
	for _, backend := range []string{storage.StorageTypeBadger, storage.StorageTypeValkey} {
		t.Run(backend, func(t *testing.T) {
			for _, mode := range []string{"ready", "missing-total", "wrong-total", "missing-claim", "unattached-claim", "wrong-file", "released-live", "released-deleting-live", "released-deleting-retired", "pending-unattached", "historical-release", "unfinished-replay", "future-field"} {
				t.Run(mode, func(t *testing.T) {
					source, request, destination := backupBundleFixture(t)
					kv, transfer, _ := kvTransferStores(t, backend)
					source.KV = transfer
					meter, err := storedbytes.NewStorageMeter(kv)
					require.NoError(t, err)
					repo, err := files.OpenRepository(kv)
					require.NoError(t, err)
					service, err := files.NewService(repo, source.Blobs, files.WithMeter(meter))
					require.NoError(t, err)
					file, err := service.Upload(t.Context(), files.UploadRequest{Account: "owner", Filename: "bytes.txt", Purpose: files.PurposeUserData, Size: 5}, strings.NewReader("bytes"))
					require.NoError(t, err)
					prefix := storedbytes.StoredBytesPrefix + base64.RawURLEncoding.EncodeToString([]byte("owner"))
					claimKey := prefix + ":claim:" + base64.RawURLEncoding.EncodeToString([]byte(file.ID))
					totalKey := prefix + ":total"
					raw, err := kv.Get(t.Context(), claimKey)
					require.NoError(t, err)
					var claim storedbytes.RecoveryClaim
					require.NoError(t, json.Unmarshal(raw, &claim))
					switch mode {
					case "missing-total":
						require.NoError(t, kv.Delete(t.Context(), totalKey))
					case "wrong-total":
						require.NoError(t, kv.Set(t.Context(), totalKey, []byte(`{"version":2,"bytes":0}`)))
					case "missing-claim":
						require.NoError(t, kv.Delete(t.Context(), claimKey))
					case "unattached-claim":
						claim.Attached = false
						claim.Measured = false
					case "wrong-file":
						require.NoError(t, repo.Delete(t.Context(), file.Account, file.ID))
					case "released-live", "released-deleting-live":
						require.NoError(t, meter.Release(t.Context(), file.Account, file.ID))
						claim.Released = true
						if mode == "released-deleting-live" {
							file.State = files.FileStateDeleting
							require.NoError(t, repo.Replace(t.Context(), file))
						}
					case "released-deleting-retired":
						file.State = files.FileStateDeleting
						require.NoError(t, repo.Replace(t.Context(), file))
						keys, err := kv.ScanWithPrefix(t.Context(), files.StoragePrefix, 10)
						require.NoError(t, err)
						require.Len(t, keys, 1)
						retained, err := kv.Get(t.Context(), keys[0])
						require.NoError(t, err)
						require.NoError(t, service.Delete(t.Context(), file.Account, file.ID))
						claim.Released = true
						require.NoError(t, kv.Set(t.Context(), keys[0], retained))
					case "pending-unattached":
						require.NoError(t, service.Delete(t.Context(), file.Account, file.ID))
						claim.Released = true
						require.NoError(t, meter.Reserve(t.Context(), "owner", "pending", 7, 0))
					case "historical-release":
						require.NoError(t, service.Delete(t.Context(), file.Account, file.ID))
						claim.Released = true
					case "unfinished-replay":
						require.NoError(t, kv.Set(t.Context(), storedbytes.ReplayStoragePrefix+"owner", []byte("marker")))
					case "future-field":
						raw = append(append([]byte(nil), raw[:len(raw)-1]...), []byte(`,"future":true}`)...)
						require.NoError(t, kv.Set(t.Context(), claimKey, raw))
					}
					if mode != "missing-claim" && mode != "future-field" {
						raw, err = json.Marshal(claim)
						require.NoError(t, err)
						require.NoError(t, kv.Set(t.Context(), claimKey, raw))
					}
					report, err := inspectReferenceFixture(t, source, request, destination)
					switch mode {
					case "ready", "released-deleting-retired", "pending-unattached", "historical-release":
						require.NoError(t, err)
						require.Positive(t, report.StoredByteClaims)
					default:
						require.Error(t, err)
					}
				})
			}
		})
	}
}

func TestBackupStoredByteIndexRefusesDuplicateAndOverflow(t *testing.T) {
	for _, mode := range []string{"duplicate", "overflow"} {
		t.Run(mode, func(t *testing.T) {
			path := filepath.Join(privateKVDirectory(t), "index.db")
			require.NoError(t, os.WriteFile(path, nil, 0o600))
			db, err := openKVSnapshot(path, false)
			require.NoError(t, err)
			defer db.Close()
			tx, err := db.BeginTx(t.Context(), nil)
			require.NoError(t, err)
			defer tx.Rollback()
			index, err := newBackupStoredByteIndex(t.Context(), tx)
			require.NoError(t, err)
			prefix := storedbytes.StoredBytesPrefix + base64.RawURLEncoding.EncodeToString([]byte("owner"))
			require.NoError(t, index.Add(t.Context(), storage.TransferRecord{Key: prefix + ":total", Value: []byte(`{"version":2,"bytes":9223372036854775807}`)}))
			claim := storedbytes.RecoveryClaim{Version: 2, Holder: "owner", ID: "first", Initial: math.MaxInt64, Bytes: math.MaxInt64, CreatedAt: time.Now().UTC()}
			data, err := json.Marshal(claim)
			require.NoError(t, err)
			record := storage.TransferRecord{Key: prefix + ":claim:" + base64.RawURLEncoding.EncodeToString([]byte(claim.ID)), Value: data}
			require.NoError(t, index.Add(t.Context(), record))
			if mode == "duplicate" {
				require.Error(t, index.Add(t.Context(), record))
				return
			}
			claim.ID = "second"
			claim.Initial = 1
			claim.Bytes = 1
			data, err = json.Marshal(claim)
			require.NoError(t, err)
			require.NoError(t, index.Add(t.Context(), storage.TransferRecord{Key: prefix + ":claim:" + base64.RawURLEncoding.EncodeToString([]byte(claim.ID)), Value: data}))
			_, err = index.Verify(t.Context())
			require.ErrorIs(t, err, storedbytes.ErrStorageHistoryUnknown)
		})
	}
}
