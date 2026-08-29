package files

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/agentstation/starport/internal/blob"
	"github.com/agentstation/starport/internal/storage"
)

// refusingDeletes wraps a byte store and fails the delete of one key. It stands
// in for the object store that answers a delete with a network error, which is
// the only way a delete gets to stop between its two writes.
//
// The refusal is per key rather than global, so a test can prove that one
// failing object does not take a healthy one with it.
type refusingDeletes struct {
	blob.Store
	refuseKey string
	err       error
}

func (s *refusingDeletes) Delete(ctx context.Context, key string) error {
	if s.refuseKey != "" && key == s.refuseKey {
		return s.err
	}
	return s.Store.Delete(ctx, key)
}

func newServiceOver(t *testing.T, bytes blob.Store, options ...Option) (*Service, Repository) {
	t.Helper()
	records, err := OpenRepository(storage.NewMockStore())
	require.NoError(t, err)
	service, err := NewService(records, bytes, options...)
	require.NoError(t, err)
	return service, records
}

// TestDeleteMarksTheRecordBeforeItRemovesTheBytes holds FIL-V14.
//
// A delete is two writes to two stores, and a process can stop between them.
// The order decides what the stop leaves behind. Marking the record first
// leaves a file that nothing reads and that the sweep can finish. Removing the
// bytes first would leave a ready record over bytes that no longer exist, and
// every read of it would fail with no explanation.
func TestDeleteMarksTheRecordBeforeItRemovesTheBytes(t *testing.T) {
	t.Parallel()
	backing, err := blob.NewFilesystem(t.TempDir())
	require.NoError(t, err)
	bytes := &refusingDeletes{Store: backing, err: io.ErrUnexpectedEOF}
	service, records := newServiceOver(t, bytes)
	ctx := context.Background()

	file := upload(t, service, "account-a", "notes.txt", "the payload")
	blobKey := file.blobKey

	// The byte store refuses, so the delete stops between its two writes.
	bytes.refuseKey = blobKey
	require.Error(t, service.Delete(ctx, "account-a", file.ID))

	// The record survives, marked, and no caller reads it.
	stopped, err := records.Get(ctx, "account-a", file.ID)
	require.NoError(t, err)
	require.Equal(t, FileStateDeleting, stopped.State)
	_, err = service.Get(ctx, "account-a", file.ID)
	require.ErrorIs(t, err, ErrFileNotFound)
	listed, err := service.List(ctx, "account-a", 0)
	require.NoError(t, err)
	require.Empty(t, listed)

	// The sweep finishes what the delete started, and removes both writes.
	bytes.refuseKey = ""
	result, err := service.Sweep(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, result.Resumed)

	_, err = bytes.Stat(ctx, blobKey)
	require.ErrorIs(t, err, blob.ErrNotFound)
	_, err = records.Get(ctx, "account-a", file.ID)
	require.ErrorIs(t, err, ErrFileNotFound)

	// A second sweep is safe. The gateway runs it on a ticker.
	result, err = service.Sweep(ctx)
	require.NoError(t, err)
	require.Equal(t, 0, result.Total())
}

// TestDeleteRemovesBothWritesWhenNothingFails states the ordinary path. FIL-V14
// asks for both halves, and the failure test above would pass over a delete
// that never removed anything.
func TestDeleteRemovesBothWritesWhenNothingFails(t *testing.T) {
	t.Parallel()
	service, records, bytes := newService(t)
	ctx := context.Background()

	file := upload(t, service, "account-a", "notes.txt", "the payload")
	require.NoError(t, service.Delete(ctx, "account-a", file.ID))

	_, err := bytes.Stat(ctx, file.blobKey)
	require.ErrorIs(t, err, blob.ErrNotFound)
	_, err = records.Get(ctx, "account-a", file.ID)
	require.ErrorIs(t, err, ErrFileNotFound)
}

// TestExpiredFileReadsAsNotFoundBeforeTheSweep holds FIL-V15.
//
// The sweep runs on an interval. A file that kept answering for the length of
// that interval past its stated window would make the window a suggestion, so
// the read decides expiry and the sweep only reclaims the storage.
func TestExpiredFileReadsAsNotFoundBeforeTheSweep(t *testing.T) {
	t.Parallel()
	created := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	clock := created
	service, records, bytes := newService(t,
		WithClock(func() time.Time { return clock }),
		WithRetention(24*time.Hour),
	)
	ctx := context.Background()

	file := upload(t, service, "account-a", "notes.txt", "the payload")
	require.Equal(t, created.Add(24*time.Hour), file.ExpiresAt)

	// One second before the window closes the file still reads.
	clock = file.ExpiresAt.Add(-time.Second)
	found, err := service.Get(ctx, "account-a", file.ID)
	require.NoError(t, err)
	require.Equal(t, file.ID, found.ID)

	// At the window it stops, with no sweep between the two reads.
	clock = file.ExpiresAt
	_, err = service.Get(ctx, "account-a", file.ID)
	require.ErrorIs(t, err, ErrFileNotFound)
	_, _, err = service.Open(ctx, "account-a", file.ID)
	require.ErrorIs(t, err, ErrFileNotFound)
	listed, err := service.List(ctx, "account-a", 0)
	require.NoError(t, err)
	require.Empty(t, listed)

	// The record and its bytes are still there, because only the sweep
	// reclaims storage.
	_, err = records.Get(ctx, "account-a", file.ID)
	require.NoError(t, err)
	_, err = bytes.Stat(ctx, file.blobKey)
	require.NoError(t, err)

	// The sweep then reclaims both.
	result, err := service.Sweep(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, result.Expired)
	_, err = bytes.Stat(ctx, file.blobKey)
	require.ErrorIs(t, err, blob.ErrNotFound)
	_, err = records.Get(ctx, "account-a", file.ID)
	require.ErrorIs(t, err, ErrFileNotFound)
}

// TestUploadShortensTheWindowAndNeverExtendsIt states the asymmetry. An
// operator caps how long this deployment holds a file, and a caller decides how
// much less than that it wants.
func TestUploadShortensTheWindowAndNeverExtendsIt(t *testing.T) {
	t.Parallel()
	created := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	service, _, _ := newService(t,
		WithClock(func() time.Time { return created }),
		WithRetention(24*time.Hour),
	)
	ctx := context.Background()

	shortened, err := service.Upload(ctx, UploadRequest{
		Account: "account-a", Filename: "notes.txt", Purpose: PurposeUserData,
		Retention: 2 * time.Hour,
	}, strings.NewReader("the payload"))
	require.NoError(t, err)
	require.Equal(t, created.Add(2*time.Hour), shortened.ExpiresAt)

	// A longer window is refused rather than clamped. A caller that asked for a
	// year and silently got a day would find out when the file stopped reading.
	_, err = service.Upload(ctx, UploadRequest{
		Account: "account-a", Filename: "notes.txt", Purpose: PurposeUserData,
		Retention: 365 * 24 * time.Hour,
	}, strings.NewReader("the payload"))
	require.ErrorIs(t, err, ErrRetentionTooLong)

	// A window shorter than an hour would expire a file while the request that
	// stored it is still running.
	_, err = service.Upload(ctx, UploadRequest{
		Account: "account-a", Filename: "notes.txt", Purpose: PurposeUserData,
		Retention: time.Minute,
	}, strings.NewReader("the payload"))
	require.ErrorIs(t, err, ErrRetentionTooShort)
}

// TestEveryStoredFileStatesAWindow holds invariant F6. A file with no expiry
// would be storage that only grows, and no later change should be able to
// create one by leaving a field unset.
func TestEveryStoredFileStatesAWindow(t *testing.T) {
	t.Parallel()
	created := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	service, _, _ := newService(t, WithClock(func() time.Time { return created }))

	file := upload(t, service, "account-a", "notes.txt", "the payload")
	require.False(t, file.ExpiresAt.IsZero(), "the record states no window")
	require.Equal(t, created.Add(DefaultRetention), file.ExpiresAt)
}

// TestSweepContinuesPastAFailingRecord states why the pass collects failures
// instead of returning on the first one.
//
// The sweep runs on a ticker. A pass that stopped at the first unreachable
// object would let one bad record hold every later one hostage, and the next
// tick would repeat the same failure forever.
func TestSweepContinuesPastAFailingRecord(t *testing.T) {
	t.Parallel()
	created := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	clock := created
	backing, err := blob.NewFilesystem(t.TempDir())
	require.NoError(t, err)
	bytes := &refusingDeletes{Store: backing, err: io.ErrUnexpectedEOF}
	service, records := newServiceOver(t, bytes,
		WithClock(func() time.Time { return clock }),
		WithRetention(time.Hour),
	)
	ctx := context.Background()

	stuck, err := service.Upload(ctx, UploadRequest{
		Account: "account-a", Filename: "stuck.txt", Purpose: PurposeUserData,
	}, strings.NewReader("the payload"))
	require.NoError(t, err)

	// The second file is written a second later, so the scan order cannot be
	// what decides which one the sweep reaches.
	clock = created.Add(time.Second)
	healthy, err := service.Upload(ctx, UploadRequest{
		Account: "account-a", Filename: "healthy.txt", Purpose: PurposeUserData,
	}, strings.NewReader("the payload"))
	require.NoError(t, err)

	// The delete of the first one stops, which leaves it marked and failing.
	bytes.refuseKey = stuck.blobKey
	require.Error(t, service.Delete(ctx, "account-a", stuck.ID))

	// Both files are now past the window, and the byte store still refuses one.
	clock = created.Add(2 * time.Hour)
	result, err := service.Sweep(ctx)
	require.Error(t, err, "the sweep reports the record it could not finish")
	require.Contains(t, err.Error(), stuck.ID)

	// The healthy record went, and only the failing one stayed.
	require.Equal(t, 1, result.Expired)
	require.Equal(t, 0, result.Resumed)
	_, err = records.Get(ctx, "account-a", healthy.ID)
	require.ErrorIs(t, err, ErrFileNotFound)
	remaining, err := records.Get(ctx, "account-a", stuck.ID)
	require.NoError(t, err)
	require.Equal(t, FileStateDeleting, remaining.State)
}
