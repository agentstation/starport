package files

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/agentstation/starport/internal/blob"
	"github.com/agentstation/starport/internal/storage"
)

// errStorageFull stands in for the limit vocabulary's own refusal. The files
// package names the meter primitive rather than importing internal/limits, so
// the test names the failure the same way a caller does.
var errStorageFull = errors.New("stored bytes limit exceeded")

// countingMeter is a serial stand-in for the durable meter. Concurrency is the
// meter's own property and internal/limits proves it; what matters here is that
// the service claims before it writes, settles against the real size, and gives
// the claim back on every path that drops a file.
type countingMeter struct {
	totals map[string]int64
}

func newCountingMeter() *countingMeter {
	return &countingMeter{totals: make(map[string]int64)}
}

func (m *countingMeter) Reserve(_ context.Context, holder string, size, bound int64) error {
	if bound > 0 && m.totals[holder]+size > bound {
		return errStorageFull
	}
	m.totals[holder] += size
	return nil
}

func (m *countingMeter) Release(_ context.Context, holder string, size int64) error {
	m.totals[holder] -= size
	return nil
}

func newMeteredService(t *testing.T) (*Service, *countingMeter, string) {
	t.Helper()
	records, err := OpenRepository(storage.NewMockStore())
	require.NoError(t, err)
	root := t.TempDir()
	bytes, err := blob.NewFilesystem(root)
	require.NoError(t, err)
	meter := newCountingMeter()
	service, err := NewService(records, bytes, WithMeter(meter))
	require.NoError(t, err)
	return service, meter, root
}

// countObjects reports how many objects the byte store holds. The blob
// contract lists no keys, so the test counts what the filesystem backend wrote.
func countObjects(t *testing.T, root string) int {
	t.Helper()
	stored := 0
	require.NoError(t, filepath.WalkDir(root, func(_ string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			stored++
		}
		return nil
	}))
	return stored
}

// TestAnUploadPastTheBoundWritesNothing states invariant F7. The check runs
// before the write, so a refused upload costs no storage at all. A check after
// the write would have already spent what it exists to protect.
func TestAnUploadPastTheBoundWritesNothing(t *testing.T) {
	t.Parallel()
	service, meter, root := newMeteredService(t)
	ctx := context.Background()

	payload := strings.Repeat("x", 600)
	first, err := service.Upload(ctx, UploadRequest{
		Tenant: "tenant-a", Filename: "first.txt", Purpose: PurposeUserData,
		Size: int64(len(payload)), StoredBytesBound: 1000,
	}, strings.NewReader(payload))
	require.NoError(t, err)
	require.Equal(t, int64(600), meter.totals["tenant-a"])

	before := countObjects(t, root)
	_, err = service.Upload(ctx, UploadRequest{
		Tenant: "tenant-a", Filename: "second.txt", Purpose: PurposeUserData,
		Size: int64(len(payload)), StoredBytesBound: 1000,
	}, strings.NewReader(payload))
	require.ErrorIs(t, err, errStorageFull)

	require.Equal(t, before, countObjects(t, root), "the refused upload wrote bytes")
	require.Equal(t, int64(600), meter.totals["tenant-a"], "the refusal moved the total")

	listed, err := service.List(ctx, "tenant-a", 0)
	require.NoError(t, err)
	require.Len(t, listed, 1)
	require.Equal(t, first.ID, listed[0].ID)
}

// TestADeleteLowersTheTotalByTheFileSize states the other half. A bound that
// only ever rose would turn every account into a one-way account, and a tenant
// under its bound would still run out of room after enough uploads and deletes.
func TestADeleteLowersTheTotalByTheFileSize(t *testing.T) {
	t.Parallel()
	service, meter, _ := newMeteredService(t)
	ctx := context.Background()

	payload := strings.Repeat("x", 600)
	file, err := service.Upload(ctx, UploadRequest{
		Tenant: "tenant-a", Filename: "first.txt", Purpose: PurposeUserData,
		Size: int64(len(payload)), StoredBytesBound: 1000,
	}, strings.NewReader(payload))
	require.NoError(t, err)

	require.NoError(t, service.Delete(ctx, "tenant-a", file.ID))
	require.Equal(t, int64(0), meter.totals["tenant-a"])

	// The room the delete gave back is usable room. An upload the bound refused
	// a moment ago now lands.
	_, err = service.Upload(ctx, UploadRequest{
		Tenant: "tenant-a", Filename: "second.txt", Purpose: PurposeUserData,
		Size: int64(len(payload)), StoredBytesBound: 1000,
	}, strings.NewReader(payload))
	require.NoError(t, err)
}

// TestTheClaimSettlesAgainstTheRealSize states why a caller cannot buy room by
// understating an upload. The claim goes in on what the caller declared,
// because nothing else is known before the write, and the write reports what
// the upload really weighed.
func TestTheClaimSettlesAgainstTheRealSize(t *testing.T) {
	t.Parallel()
	service, meter, root := newMeteredService(t)
	ctx := context.Background()

	// Understated: the claim rises to the real size once the write lands, and
	// the upload is refused because the real size does not fit.
	payload := strings.Repeat("x", 900)
	_, err := service.Upload(ctx, UploadRequest{
		Tenant: "tenant-a", Filename: "understated.txt", Purpose: PurposeUserData,
		Size: 1, StoredBytesBound: 500,
	}, strings.NewReader(payload))
	require.ErrorIs(t, err, errStorageFull)
	require.Equal(t, int64(0), meter.totals["tenant-a"])
	require.Equal(t, 0, countObjects(t, root), "the refused upload left its bytes behind")

	// Overstated: the claim falls to the real size, so the tenant does not hold
	// room it never used.
	small := strings.Repeat("x", 100)
	_, err = service.Upload(ctx, UploadRequest{
		Tenant: "tenant-b", Filename: "overstated.txt", Purpose: PurposeUserData,
		Size: 400, StoredBytesBound: 500,
	}, strings.NewReader(small))
	require.NoError(t, err)
	require.Equal(t, int64(100), meter.totals["tenant-b"])
}

// TestAFailedWriteGivesBackItsClaim states the synchronous half. An upload
// whose bytes never landed must not leave a tenant paying for it.
func TestAFailedWriteGivesBackItsClaim(t *testing.T) {
	t.Parallel()
	records, err := OpenRepository(storage.NewMockStore())
	require.NoError(t, err)
	root := t.TempDir()
	bytes, err := blob.NewFilesystem(root)
	require.NoError(t, err)
	meter := newCountingMeter()

	// The write fails, which is the same shape as a process that stops: the
	// pending record and its claim are both already in place.
	failing, err := NewService(records, &failingPut{Store: bytes}, WithMeter(meter))
	require.NoError(t, err)
	_, err = failing.Upload(context.Background(), UploadRequest{
		Tenant: "tenant-a", Filename: "lost.txt", Purpose: PurposeUserData,
		Size: 600, StoredBytesBound: 1000,
	}, strings.NewReader("payload"))
	require.Error(t, err)
	require.Equal(t, int64(0), meter.totals["tenant-a"], "the failed write kept the claim")

	// And the room is usable again.
	service, err := NewService(records, bytes, WithMeter(meter))
	require.NoError(t, err)
	_, err = service.Upload(context.Background(), UploadRequest{
		Tenant: "tenant-a", Filename: "next.txt", Purpose: PurposeUserData,
		Size: 900, StoredBytesBound: 1000,
	}, strings.NewReader(strings.Repeat("x", 900)))
	require.NoError(t, err)
}

// TestTheSweepGivesBackAnAbandonedClaim states why the pending record carries
// the claim rather than a variable in the upload call.
//
// A process that stopped between the claim and the commit leaves nothing in
// memory to unwind. The claim survives on the record, so the sweep that
// deletes the abandoned record reads the number off it and gives it back.
func TestTheSweepGivesBackAnAbandonedClaim(t *testing.T) {
	t.Parallel()
	created := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	clock := created
	records, err := OpenRepository(storage.NewMockStore())
	require.NoError(t, err)
	bytes, err := blob.NewFilesystem(t.TempDir())
	require.NoError(t, err)
	meter := newCountingMeter()
	service, err := NewService(records, bytes,
		WithMeter(meter), WithClock(func() time.Time { return clock }))
	require.NoError(t, err)
	ctx := context.Background()

	// This is what a crashed upload left behind: a claim, and a pending record
	// that carries it.
	require.NoError(t, meter.Reserve(ctx, "tenant-a", 600, 1000))
	abandoned := File{
		ID:        newFileID(),
		Tenant:    "tenant-a",
		Filename:  "lost.txt",
		Purpose:   PurposeUserData,
		Bytes:     600,
		State:     FileStatePending,
		CreatedAt: created,
		ExpiresAt: created.Add(DefaultRetention),
		blobKey:   newBlobKey(),
	}
	require.NoError(t, records.Create(ctx, abandoned))

	clock = created.Add(2 * DefaultPendingGrace)
	result, err := service.Sweep(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, result.Abandoned)
	require.Equal(t, int64(0), meter.totals["tenant-a"])
}

// failingPut refuses every write. It stands in for a byte store that went away
// between the record and the bytes.
type failingPut struct {
	blob.Store
}

func (s *failingPut) Put(context.Context, string, io.Reader) (blob.Info, error) {
	return blob.Info{}, os.ErrPermission
}
