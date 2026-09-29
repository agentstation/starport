package blob

import (
	"bytes"
	"context"
	"errors"
	"github.com/agentstation/starmap/pkg/productfiles"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

type activationTarget interface {
	ActivateImport(context.Context, string, Snapshot, string) error
}

func activationFixture(t *testing.T, kind string) (RestoreTarget, Store, string, Snapshot) {
	t.Helper()
	source, err := NewFilesystem(filepath.Join(snapshotDirectory(t), "source"))
	require.NoError(t, err)
	_, err = source.Put(t.Context(), "data", strings.NewReader("original"))
	require.NoError(t, err)
	archive := filepath.Join(snapshotDirectory(t), "blobs.tar")
	snapshot, err := Backup(t.Context(), source, archive)
	require.NoError(t, err)
	var target RestoreTarget
	var store Store
	if kind == "filesystem" {
		destination := filepath.Join(snapshotDirectory(t), "target")
		target, err = FilesystemRestoreTarget(destination)
		store = &Filesystem{root: destination}
	} else {
		objects := snapshotObjects(t)
		target, err = ObjectRestoreTarget(objects)
		store = objects
	}
	require.NoError(t, err)
	require.NoError(t, target.Restore(t.Context(), archive, snapshotDirectory(t), "operation", snapshot))
	return target, store, archive, snapshot
}

func TestBlobImportActivation(t *testing.T) {
	for _, kind := range []string{"filesystem", "objectstore"} {
		t.Run(kind, func(t *testing.T) {
			target, store, archive, snapshot := activationFixture(t, kind)
			require.Implements(t, (*activationTarget)(nil), target)
			activate := target.(activationTarget)
			decision := strings.Repeat("a", 64)
			require.NoError(t, activate.ActivateImport(t.Context(), "operation", snapshot, decision))
			if f, ok := store.(*Filesystem); ok {
				var err error
				store, err = NewFilesystem(f.root)
				require.NoError(t, err)
			}
			_, err := store.Put(t.Context(), "data", strings.NewReader("later"))
			require.NoError(t, err)
			require.NoError(t, activate.ActivateImport(t.Context(), "operation", snapshot, decision))
			body, err := store.Get(t.Context(), "data")
			require.NoError(t, err)
			data, err := io.ReadAll(body)
			require.NoError(t, err)
			require.NoError(t, body.Close())
			require.Equal(t, "later", string(data))
			require.Error(t, activate.ActivateImport(t.Context(), "operation", snapshot, strings.Repeat("b", 64)))
			require.Error(t, activate.ActivateImport(t.Context(), "other", snapshot, decision))
			require.Error(t, target.Restore(t.Context(), archive, snapshotDirectory(t), "operation", snapshot))
		})
	}
}

func activationRecordsFor(t *testing.T, target RestoreTarget) activationRecords {
	t.Helper()
	switch target := target.(type) {
	case filesystemRestoreTarget:
		control, err := productfiles.ExistingDirectory(filepath.Join(target.destination, ".starport"))
		require.NoError(t, err)
		return filesystemActivation{directory: control}
	case objectRestoreTarget:
		return objectActivation{store: target.store}
	default:
		t.Fatal("unexpected target")
		return nil
	}
}

func replaceActivationRecord(t *testing.T, target RestoreTarget, key string, value []byte) {
	t.Helper()
	switch target := target.(type) {
	case filesystemRestoreTarget:
		path := filepath.Join(target.destination, filepath.FromSlash(key))
		if value == nil {
			require.NoError(t, os.Remove(path))
		} else {
			require.NoError(t, os.WriteFile(path, value, 0o600))
		}
	case objectRestoreTarget:
		if value == nil {
			_, err := target.store.client.DeleteObject(t.Context(), &s3.DeleteObjectInput{Bucket: aws.String(target.store.bucket), Key: aws.String(target.store.objectKey(key))})
			require.NoError(t, err)
		} else {
			_, err := target.store.client.PutObject(t.Context(), &s3.PutObjectInput{Bucket: aws.String(target.store.bucket), Key: aws.String(target.store.objectKey(key)), Body: bytes.NewReader(value)})
			require.NoError(t, err)
		}
	}
}

func TestBlobActivationRetainsRestrictions(t *testing.T) {
	for _, kind := range []string{"filesystem", "objectstore"} {
		t.Run(kind, func(t *testing.T) {
			for _, invalid := range []string{"different-operation", "different-snapshot", "bad-decision", "canceled", "missing-barrier", "corrupt-barrier", "history-without-current"} {
				t.Run(invalid, func(t *testing.T) {
					target, _, _, snapshot := activationFixture(t, kind)
					records := activationRecordsFor(t, target)
					operation, decision, ctx := "operation", strings.Repeat("a", 64), t.Context()
					claim, err := makeBlobClaim(operation, snapshot)
					require.NoError(t, err)
					switch invalid {
					case "different-operation":
						operation = "other"
					case "different-snapshot":
						snapshot.SHA256 = strings.Repeat("b", 64)
					case "bad-decision":
						decision = "not-a-digest"
					case "canceled":
						var cancel context.CancelFunc
						ctx, cancel = context.WithCancel(ctx)
						cancel()
					case "missing-barrier":
						replaceActivationRecord(t, target, blobImportKey, nil)
					case "corrupt-barrier":
						replaceActivationRecord(t, target, blobImportKey, []byte("corrupt"))
					case "history-without-current":
						key, history, _, _, err := makeActivation(claim, decision)
						require.NoError(t, err)
						replaceActivationRecord(t, target, key, history)
					}
					require.Error(t, target.(ImportActivator).ActivateImport(ctx, operation, snapshot, decision))
					if invalid != "missing-barrier" {
						require.ErrorIs(t, checkActivationBarrier(t.Context(), records), ErrImportRestricted)
					}
				})
			}
		})
	}
}

func TestBlobActivationRefusesDamagedCompletion(t *testing.T) {
	for _, kind := range []string{"filesystem", "objectstore"} {
		t.Run(kind, func(t *testing.T) {
			for _, damage := range []string{"missing-current", "missing-history", "missing-barrier", "corrupt-current", "corrupt-history", "revived-barrier"} {
				t.Run(damage, func(t *testing.T) {
					target, _, _, snapshot := activationFixture(t, kind)
					decision := strings.Repeat("a", 64)
					activate := target.(ImportActivator)
					require.NoError(t, activate.ActivateImport(t.Context(), "operation", snapshot, decision))
					claim, err := makeBlobClaim("operation", snapshot)
					require.NoError(t, err)
					key, _, _, _, err := makeActivation(claim, decision)
					require.NoError(t, err)
					switch damage {
					case "missing-current":
						replaceActivationRecord(t, target, blobActivationCurrent, nil)
					case "missing-history":
						replaceActivationRecord(t, target, key, nil)
					case "missing-barrier":
						replaceActivationRecord(t, target, blobImportKey, nil)
					case "corrupt-current":
						replaceActivationRecord(t, target, blobActivationCurrent, []byte("corrupt"))
					case "corrupt-history":
						replaceActivationRecord(t, target, key, []byte("corrupt"))
					case "revived-barrier":
						replaceActivationRecord(t, target, blobImportKey, claim)
					}
					require.Error(t, activate.ActivateImport(t.Context(), "operation", snapshot, decision))
					require.ErrorIs(t, checkActivationBarrier(t.Context(), activationRecordsFor(t, target)), ErrImportRestricted)
				})
			}
		})
	}
}

type interruptedActivation struct {
	activationRecords
	after, writes int
}

var errActivationReplyLost = errors.New("activation reply lost")

func (f *interruptedActivation) compare(ctx context.Context, name string, previous, value []byte) error {
	if err := f.activationRecords.compare(ctx, name, previous, value); err != nil {
		return err
	}
	f.writes++
	if f.writes == f.after {
		return errActivationReplyLost
	}
	return nil
}

func TestBlobActivationResumesEveryDurableBoundary(t *testing.T) {
	for _, kind := range []string{"filesystem", "objectstore"} {
		t.Run(kind, func(t *testing.T) {
			for after := 1; after <= 4; after++ {
				t.Run(strconv.Itoa(after), func(t *testing.T) {
					target, _, _, snapshot := activationFixture(t, kind)
					records := activationRecordsFor(t, target)
					claim, err := makeBlobClaim("operation", snapshot)
					require.NoError(t, err)
					interrupted := &interruptedActivation{activationRecords: records, after: after}
					require.ErrorIs(t, activateBlobImport(t.Context(), interrupted, claim, strings.Repeat("a", 64)), errActivationReplyLost)
					if after < 4 {
						require.ErrorIs(t, checkActivationBarrier(t.Context(), records), ErrImportRestricted)
					}
					require.Error(t, target.(ImportActivator).ActivateImport(t.Context(), "operation", snapshot, strings.Repeat("b", 64)))
					require.NoError(t, target.(ImportActivator).ActivateImport(t.Context(), "operation", snapshot, strings.Repeat("a", 64)))
					require.NoError(t, checkActivationBarrier(t.Context(), records))
				})
			}
		})
	}
}

func TestBlobActivationHistorySurvivesTransfer(t *testing.T) {
	for _, sourceKind := range []string{"filesystem", "objectstore"} {
		t.Run(sourceKind, func(t *testing.T) {
			sourceTarget, source, _, original := activationFixture(t, sourceKind)
			require.NoError(t, sourceTarget.(ImportActivator).ActivateImport(t.Context(), "operation", original, strings.Repeat("a", 64)))
			archive := filepath.Join(snapshotDirectory(t), "activated.tar")
			snapshot, err := Backup(t.Context(), source, archive)
			require.NoError(t, err)
			require.EqualValues(t, 2, snapshot.Objects, "payload and historical receipt")
			for _, targetKind := range []string{"filesystem", "objectstore"} {
				t.Run(targetKind, func(t *testing.T) {
					var target RestoreTarget
					var store Store
					if targetKind == "filesystem" {
						path := filepath.Join(snapshotDirectory(t), "target")
						target, err = FilesystemRestoreTarget(path)
						store = &Filesystem{root: path}
					} else {
						objects := snapshotObjects(t)
						target, err = ObjectRestoreTarget(objects)
						store = objects
					}
					require.NoError(t, err)
					require.NoError(t, target.Restore(t.Context(), archive, snapshotDirectory(t), "next-operation", snapshot))
					require.NoError(t, target.Restore(t.Context(), archive, snapshotDirectory(t), "next-operation", snapshot))
					require.ErrorIs(t, checkActivationBarrier(t.Context(), activationRecordsFor(t, target)), ErrImportRestricted)
					require.Error(t, target.(ImportActivator).ActivateImport(t.Context(), "operation", original, strings.Repeat("a", 64)))
					require.NoError(t, target.(ImportActivator).ActivateImport(t.Context(), "next-operation", snapshot, strings.Repeat("b", 64)))
					next, err := Backup(t.Context(), store, filepath.Join(snapshotDirectory(t), "next.tar"))
					require.NoError(t, err)
					require.EqualValues(t, 3, next.Objects, "payload and both historical receipts")
				})
			}
		})
	}
}

func TestBlobActivationConcurrentDecisions(t *testing.T) {
	for _, kind := range []string{"filesystem", "objectstore"} {
		t.Run(kind, func(t *testing.T) {
			target, _, _, snapshot := activationFixture(t, kind)
			outcomes := make(chan error, 2)
			var wg sync.WaitGroup
			for _, decision := range []string{strings.Repeat("a", 64), strings.Repeat("b", 64)} {
				wg.Go(func() {
					outcomes <- target.(ImportActivator).ActivateImport(t.Context(), "operation", snapshot, decision)
				})
			}
			wg.Wait()
			close(outcomes)
			winners := 0
			for err := range outcomes {
				if err == nil {
					winners++
				}
			}
			require.Equal(t, 1, winners)
			require.NoError(t, checkActivationBarrier(t.Context(), activationRecordsFor(t, target)))
		})
	}
}

func TestBlobActivationRejectsUnsupportedConditionalWrites(t *testing.T) {
	for _, header := range []string{"If-Match", "If-None-Match"} {
		t.Run(header, func(t *testing.T) {
			target, _, _, snapshot := activationFixture(t, "objectstore")
			native := target.(objectRestoreTarget).store
			endpoint, err := url.Parse(os.Getenv("TEST_BLOB_S3_ENDPOINT"))
			require.NoError(t, err)
			proxy := httputil.NewSingleHostReverseProxy(endpoint)

			var conditional atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodPut && r.Header.Get(header) != "" {
					count := conditional.Add(1)
					if header == "If-Match" || count > 1 {
						// A service that acknowledges a refused condition cannot
						// qualify activation, even if it leaves the bytes intact.
						w.Header().Set("ETag", "\"unqualified\"")
						w.WriteHeader(http.StatusOK)
						return
					}
				}
				proxy.ServeHTTP(w, r)
			}))
			defer server.Close()
			unsupported, err := NewObjectStore(t.Context(), ObjectStoreOptions{Endpoint: server.URL, Region: "us-east-1", Bucket: native.bucket, Prefix: native.prefix, AccessKeyID: "starport-test", SecretAccessKey: "starport-local-test-only"})
			require.NoError(t, err)
			err = (objectRestoreTarget{store: unsupported}).ActivateImport(t.Context(), "operation", snapshot, strings.Repeat("a", 64))
			require.Error(t, err)
			require.ErrorIs(t, checkActivationBarrier(t.Context(), objectActivation{store: native}), ErrImportRestricted)
			_, err = (objectActivation{store: native}).read(t.Context(), blobActivationCurrent)
			require.ErrorIs(t, err, ErrNotFound)
		})
	}
}

func TestBlobActivationHistoryEncoding(t *testing.T) {
	key, history, _, active, err := makeActivation([]byte("claim"), strings.Repeat("a", 64))
	require.NoError(t, err)
	for _, body := range [][]byte{[]byte("bad"), append([]byte(" "), history...), active, bytes.ReplaceAll(history, []byte(`"version":1`), []byte(`"version":2`)), append(history, 0)} {
		_, _, err := inspectActivationHistory(key, int64(len(body)), bytes.NewReader(body))
		require.Error(t, err)
	}
	require.False(t, validBlobAddress(blobActivationCurrent))
	require.False(t, validBlobAddress(blobImportKey))
	require.False(t, validBlobAddress(blobActivationPrefix+strings.Repeat("A", 64)))
}

func TestBlobActivationOrphanProbeRemainsOrdinaryData(t *testing.T) {
	target, store, _, snapshot := activationFixture(t, "objectstore")
	native := store.(*ObjectStore)
	// These bytes represent every write boundary at which a probe process can exit.
	for _, body := range []string{"activation-condition-probe", "incorrect", "accepted"} {
		key := blobAddress(objectsDir, "activation-probe-"+body)
		_, err := native.client.PutObject(t.Context(), &s3.PutObjectInput{Bucket: aws.String(native.bucket), Key: aws.String(native.objectKey(key)), Body: strings.NewReader(body)})
		require.NoError(t, err)
	}
	require.ErrorIs(t, checkActivationBarrier(t.Context(), objectActivation{store: native}), ErrImportRestricted)
	require.NoError(t, target.(ImportActivator).ActivateImport(t.Context(), "operation", snapshot, strings.Repeat("a", 64)))
	archive := filepath.Join(snapshotDirectory(t), "with-probes.tar")
	captured, err := Backup(t.Context(), store, archive)
	require.NoError(t, err)
	require.EqualValues(t, 5, captured.Objects, "payload, history, and three inert probe objects")
	view, err := OpenSnapshot(t.Context(), archive, snapshotDirectory(t), captured)
	require.NoError(t, err)
	defer view.Close()
	for _, body := range []string{"activation-condition-probe", "incorrect", "accepted"} {
		reader, err := view.Get(t.Context(), "activation-probe-"+body)
		require.NoError(t, err)
		data, err := io.ReadAll(reader)
		require.NoError(t, err)
		require.NoError(t, reader.Close())
		require.Equal(t, body, string(data))
	}
}

func TestBlobActivationRetryPreservesUnownedPublicationJournal(t *testing.T) {
	target, store, _, snapshot := activationFixture(t, "filesystem")
	activate := target.(ImportActivator)
	require.NoError(t, activate.ActivateImport(t.Context(), "operation", snapshot, strings.Repeat("a", 64)))
	path := filepath.Join(store.(*Filesystem).root, ".starport", productfiles.PublicationDirectoryName, strings.Repeat("A", 26)+".jsonl")
	require.NoError(t, os.WriteFile(path, []byte("unowned-journal"), 0o600))
	require.Error(t, activate.ActivateImport(t.Context(), "operation", snapshot, strings.Repeat("a", 64)))
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "unowned-journal", string(data))
	_, err = Backup(t.Context(), store, filepath.Join(snapshotDirectory(t), "refused.tar"))
	require.Error(t, err)
}

func TestBlobActivationPendingHistoryCannotBeOverwritten(t *testing.T) {
	for _, kind := range []string{"filesystem", "objectstore"} {
		t.Run(kind, func(t *testing.T) {
			target, _, _, snapshot := activationFixture(t, kind)
			records := activationRecordsFor(t, target)
			claim, err := makeBlobClaim("operation", snapshot)
			require.NoError(t, err)
			decision := strings.Repeat("a", 64)
			interrupted := &interruptedActivation{activationRecords: records, after: 1}
			require.ErrorIs(t, activateBlobImport(t.Context(), interrupted, claim, decision), errActivationReplyLost)
			key, _, _, _, err := makeActivation(claim, decision)
			require.NoError(t, err)
			replaceActivationRecord(t, target, key, []byte{})
			require.Error(t, target.(ImportActivator).ActivateImport(t.Context(), "operation", snapshot, decision))
			unchanged, err := records.read(t.Context(), key)
			require.NoError(t, err)
			require.Empty(t, unchanged)
			require.ErrorIs(t, checkActivationBarrier(t.Context(), records), ErrImportRestricted)
		})
	}
}
