package files

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/agentstation/starport/internal/blob"
	"github.com/agentstation/starport/internal/storage"
)

func newService(t *testing.T, options ...Option) (*Service, Repository, blob.Store) {
	t.Helper()
	records, err := OpenRepository(storage.NewMockStore())
	require.NoError(t, err)
	bytes, err := blob.NewFilesystem(t.TempDir())
	require.NoError(t, err)
	service, err := NewService(records, bytes, options...)
	require.NoError(t, err)
	return service, records, bytes
}

func upload(t *testing.T, service *Service, account, name, payload string) File {
	t.Helper()
	file, err := service.Upload(context.Background(), UploadRequest{
		Account: account, Filename: name, Purpose: PurposeUserData,
	}, strings.NewReader(payload))
	require.NoError(t, err)
	return file
}

func TestUploadCommitsTheRecordAfterTheBytes(t *testing.T) {
	t.Parallel()
	service, _, _ := newService(t)
	ctx := context.Background()

	file := upload(t, service, "account-a", "notes.txt", "the payload")
	require.Equal(t, FileStateReady, file.State)
	require.Equal(t, int64(len("the payload")), file.Bytes)
	require.NotEmpty(t, file.ID)

	// The size comes from the byte store rather than from the request, so a
	// caller cannot state a size the bytes do not match.
	read, reader, err := service.Open(ctx, "account-a", file.ID)
	require.NoError(t, err)
	defer func() { require.NoError(t, reader.Close()) }()
	content, err := io.ReadAll(reader)
	require.NoError(t, err)
	require.Equal(t, "the payload", string(content))
	require.Equal(t, file.Bytes, read.Bytes)
}

// TestAnotherAccountReadsNotFound holds FIL-V07. A refusal would confirm that
// the identifier exists, and an identifier is the only thing a caller has to
// guess.
func TestAnotherAccountReadsNotFound(t *testing.T) {
	t.Parallel()
	service, _, _ := newService(t)
	ctx := context.Background()

	file := upload(t, service, "account-a", "notes.txt", "the payload")

	_, err := service.Get(ctx, "account-b", file.ID)
	require.ErrorIs(t, err, ErrFileNotFound)

	_, _, err = service.Open(ctx, "account-b", file.ID)
	require.ErrorIs(t, err, ErrFileNotFound)

	require.ErrorIs(t, service.Delete(ctx, "account-b", file.ID), ErrFileNotFound)

	listed, err := service.List(ctx, "account-b", 0)
	require.NoError(t, err)
	require.Empty(t, listed)

	// The owner still reads it. The delete another account attempted did
	// nothing.
	owned, err := service.Get(ctx, "account-a", file.ID)
	require.NoError(t, err)
	require.Equal(t, file.ID, owned.ID)
}

// TestCrashBeforeTheCommitLeavesNoReachableFile holds FIL-V08.
//
// The test writes the pending record and the bytes and then stops, which is
// exactly what a killed process leaves behind. Nothing else runs, so no
// cleanup path can hide the state the sweep has to handle.
func TestCrashBeforeTheCommitLeavesNoReachableFile(t *testing.T) {
	t.Parallel()
	created := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	clock := created
	service, records, bytes := newService(t,
		WithClock(func() time.Time { return clock }),
		WithPendingGrace(time.Hour),
	)
	ctx := context.Background()

	blobKey := newBlobKey()
	pending := File{
		ID: newFileID(), Account: "account-a", Filename: "half.txt",
		Purpose: PurposeUserData, State: FileStatePending,
		CreatedAt: created, blobKey: blobKey,
	}
	require.NoError(t, records.Create(ctx, pending))
	_, err := bytes.Put(ctx, blobKey, strings.NewReader("the payload"))
	require.NoError(t, err)

	// No caller can reach it, through either the record or a listing.
	_, err = service.Get(ctx, "account-a", pending.ID)
	require.ErrorIs(t, err, ErrFileNotFound)
	listed, err := service.List(ctx, "account-a", 0)
	require.NoError(t, err)
	require.Empty(t, listed)

	// A sweep inside the grace window leaves it alone. An upload that is still
	// streaming looks exactly like an abandoned one, and deleting its bytes
	// mid-stream would break a request nobody reported.
	result, err := service.Sweep(ctx)
	require.NoError(t, err)
	require.Equal(t, 0, result.Abandoned)
	_, err = bytes.Stat(ctx, blobKey)
	require.NoError(t, err)

	// Past the window the sweep deletes the bytes the pending record names,
	// and then the record.
	clock = created.Add(2 * time.Hour)
	result, err = service.Sweep(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, result.Abandoned)

	_, err = bytes.Stat(ctx, blobKey)
	require.ErrorIs(t, err, blob.ErrNotFound)
	_, err = records.Get(ctx, "account-a", pending.ID)
	require.ErrorIs(t, err, ErrFileNotFound)

	// A second sweep is safe. The reconciliation ticker runs it repeatedly.
	result, err = service.Sweep(ctx)
	require.NoError(t, err)
	require.Equal(t, 0, result.Abandoned)
}

// TestSweepLeavesACommittedFileAlone guards the other direction. A sweep that
// read the age and not the state would delete every file the moment it aged
// past the grace window.
func TestSweepLeavesACommittedFileAlone(t *testing.T) {
	t.Parallel()
	created := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	clock := created
	service, _, _ := newService(t,
		WithClock(func() time.Time { return clock }),
		WithPendingGrace(time.Hour),
	)
	ctx := context.Background()

	file := upload(t, service, "account-a", "notes.txt", "the payload")
	clock = created.Add(100 * time.Hour)

	result, err := service.Sweep(ctx)
	require.NoError(t, err)
	require.Equal(t, 0, result.Abandoned)

	found, err := service.Get(ctx, "account-a", file.ID)
	require.NoError(t, err)
	require.Equal(t, file.ID, found.ID)
}

// TestRecordExposesNoBlobKey holds FIL-V09.
//
// The key is the one value that turns knowledge of a file into access to its
// bytes across every account boundary. It stays inside this package, so a
// response body, a log line, or a console payload cannot carry it out by
// accident.
func TestRecordExposesNoBlobKey(t *testing.T) {
	t.Parallel()
	service, _, _ := newService(t)

	file := upload(t, service, "account-a", "notes.txt", "the payload")
	blobKey := file.blobKey
	require.NotEmpty(t, blobKey, "the record names no bytes")

	// No exported field carries the key, whatever a later change adds.
	value := reflect.ValueOf(file)
	for i := 0; i < value.NumField(); i++ {
		field := value.Type().Field(i)
		if !field.IsExported() {
			continue
		}
		rendered, err := json.Marshal(value.Field(i).Interface())
		require.NoError(t, err)
		require.NotContainsf(t, string(rendered), blobKey, "field %s carries the blob key", field.Name)
	}

	// An encoder cannot reach it either, which is what a response body uses.
	encoded, err := json.Marshal(file)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), blobKey)

	// The record store does carry it. That is the one place it belongs, and
	// the sweep depends on it.
	stored, err := encodeFile(file)
	require.NoError(t, err)
	require.Contains(t, string(stored), blobKey)
}

func TestUploadRefusesAPurposeThisGatewayDoesNotServe(t *testing.T) {
	t.Parallel()
	service, records, _ := newService(t)
	ctx := context.Background()

	for _, purpose := range []Purpose{"assistants", "fine-tune", ""} {
		_, err := service.Upload(ctx, UploadRequest{
			Account: "account-a", Filename: "notes.txt", Purpose: purpose,
		}, strings.NewReader("the payload"))
		require.ErrorIsf(t, err, ErrInvalidPurpose, "purpose %q", purpose)
	}

	// The refusal happens before any write, so a refused purpose leaves no
	// record for the sweep to find.
	scanned, err := records.Scan(ctx, 0)
	require.NoError(t, err)
	require.Empty(t, scanned)
}

// TestFailedUploadLeavesNothing covers the case the sweep should never see. A
// reader that stops mid-stream is the common failure, and the service undoes
// both writes while it still knows the key.
func TestFailedUploadLeavesNothing(t *testing.T) {
	t.Parallel()
	service, records, bytes := newService(t)
	ctx := context.Background()

	_, err := service.Upload(ctx, UploadRequest{
		Account: "account-a", Filename: "half.txt", Purpose: PurposeUserData,
	}, &stopsEarly{err: errors.New("the client went away")})
	require.Error(t, err)

	scanned, err := records.Scan(ctx, 0)
	require.NoError(t, err)
	require.Empty(t, scanned)

	// The byte store lists no keys, so the check is the sweep's own question:
	// is there a record that names anything at all.
	result, err := service.Sweep(ctx)
	require.NoError(t, err)
	require.Equal(t, 0, result.Abandoned)
	require.NotNil(t, bytes)
}

// TestDeleteRemovesBothWrites keeps the record and the bytes in step.
func TestDeleteRemovesBothWrites(t *testing.T) {
	t.Parallel()
	service, records, bytes := newService(t)
	ctx := context.Background()

	file := upload(t, service, "account-a", "notes.txt", "the payload")
	blobKey := file.blobKey

	require.NoError(t, service.Delete(ctx, "account-a", file.ID))

	_, err := records.Get(ctx, "account-a", file.ID)
	require.ErrorIs(t, err, ErrFileNotFound)
	_, err = bytes.Stat(ctx, blobKey)
	require.ErrorIs(t, err, blob.ErrNotFound)
}

// TestOpenReportsNotFoundWhenTheBytesAreGone covers a record that outlived its
// bytes. An operator who cleared a bucket should get an honest answer rather
// than a byte-store error a caller cannot act on.
func TestOpenReportsNotFoundWhenTheBytesAreGone(t *testing.T) {
	t.Parallel()
	service, _, bytes := newService(t)
	ctx := context.Background()

	file := upload(t, service, "account-a", "notes.txt", "the payload")
	require.NoError(t, bytes.Delete(ctx, file.blobKey))

	_, _, err := service.Open(ctx, "account-a", file.ID)
	require.ErrorIs(t, err, ErrFileNotFound)
}

func TestNewServiceRefusesAMissingStore(t *testing.T) {
	t.Parallel()
	records, err := OpenRepository(storage.NewMockStore())
	require.NoError(t, err)
	bytes, err := blob.NewFilesystem(t.TempDir())
	require.NoError(t, err)

	_, err = NewService(nil, bytes)
	require.ErrorIs(t, err, ErrServiceRequired)
	_, err = NewService(records, nil)
	require.ErrorIs(t, err, ErrServiceRequired)
	_, err = OpenRepository(nil)
	require.ErrorIs(t, err, ErrRepositoryRequired)
}

// stopsEarly fails on the first read. It stands for a client that drops its
// connection before it sends a byte.
type stopsEarly struct{ err error }

func (s *stopsEarly) Read([]byte) (int, error) { return 0, s.err }
