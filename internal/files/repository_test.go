package files

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/agentstation/starport/internal/storage"
)

func newRepository(t *testing.T) (Repository, storage.KVStore) {
	t.Helper()
	store := storage.NewMockStore()
	records, err := OpenRepository(store)
	require.NoError(t, err)
	return records, store
}

func sampleFile(tenant, id string) File {
	return File{
		ID: id, Tenant: tenant, Filename: "notes.txt",
		Purpose: PurposeUserData, State: FileStateReady, Bytes: 11,
		CreatedAt: time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC),
		blobKey:   "0123456789abcdef",
	}
}

func TestRepositoryRoundTripsEveryField(t *testing.T) {
	t.Parallel()
	records, _ := newRepository(t)
	ctx := context.Background()

	want := sampleFile("tenant-a", "file-one")
	want.ExpiresAt = want.CreatedAt.Add(24 * time.Hour)
	require.NoError(t, records.Create(ctx, want))

	got, err := records.Get(ctx, "tenant-a", "file-one")
	require.NoError(t, err)
	require.Equal(t, want, got)
}

// TestTenantsCannotShareAnIdentifier proves the isolation is structural. The
// tenant sits above the identifier in the key, so a read for the wrong tenant
// misses rather than passing a check a later change could forget.
func TestTenantsCannotShareAnIdentifier(t *testing.T) {
	t.Parallel()
	records, _ := newRepository(t)
	ctx := context.Background()

	first := sampleFile("tenant-a", "file-one")
	second := sampleFile("tenant-b", "file-one")
	second.Filename = "other.txt"
	require.NoError(t, records.Create(ctx, first))
	require.NoError(t, records.Create(ctx, second))

	a, err := records.Get(ctx, "tenant-a", "file-one")
	require.NoError(t, err)
	require.Equal(t, "notes.txt", a.Filename)

	b, err := records.Get(ctx, "tenant-b", "file-one")
	require.NoError(t, err)
	require.Equal(t, "other.txt", b.Filename)

	listed, err := records.List(ctx, "tenant-a", 0)
	require.NoError(t, err)
	require.Len(t, listed, 1)

	// The sweep sees both, because it answers a question no tenant asks.
	scanned, err := records.Scan(ctx, 0)
	require.NoError(t, err)
	require.Len(t, scanned, 2)
}

func TestCreateRefusesADuplicateIdentifier(t *testing.T) {
	t.Parallel()
	records, _ := newRepository(t)
	ctx := context.Background()

	require.NoError(t, records.Create(ctx, sampleFile("tenant-a", "file-one")))
	require.ErrorIs(t, records.Create(ctx, sampleFile("tenant-a", "file-one")), ErrFileExists)
}

func TestReplaceRefusesAnAbsentRecord(t *testing.T) {
	t.Parallel()
	records, _ := newRepository(t)
	require.ErrorIs(t,
		records.Replace(context.Background(), sampleFile("tenant-a", "file-one")),
		ErrFileNotFound)
}

func TestDeleteIsRepeatable(t *testing.T) {
	t.Parallel()
	records, _ := newRepository(t)
	ctx := context.Background()

	require.NoError(t, records.Create(ctx, sampleFile("tenant-a", "file-one")))
	require.NoError(t, records.Delete(ctx, "tenant-a", "file-one"))
	require.NoError(t, records.Delete(ctx, "tenant-a", "file-one"))
}

// TestCorruptRecordDoesNotPassAsAFile keeps damaged durable data out of a
// response. A record that decoded into a half-built file would answer a read
// with a filename and no bytes behind it.
func TestCorruptRecordDoesNotPassAsAFile(t *testing.T) {
	t.Parallel()
	records, store := newRepository(t)
	ctx := context.Background()

	key := storageKey("tenant-a", "file-one")
	require.NoError(t, store.Set(ctx, key, []byte("{not json")))
	_, err := records.Get(ctx, "tenant-a", "file-one")
	require.ErrorIs(t, err, ErrCorruptRecord)

	require.NoError(t, store.Set(ctx, key, []byte(`{"schema_version":99,"id":"file-one"}`)))
	_, err = records.Get(ctx, "tenant-a", "file-one")
	require.ErrorIs(t, err, ErrCorruptRecord)

	// A record that names no bytes is corrupt too. Without the key nothing can
	// read the file or ever delete it.
	require.NoError(t, store.Set(ctx, key,
		[]byte(`{"schema_version":1,"id":"file-one","tenant":"tenant-a","filename":"notes.txt","purpose":"user_data","state":"ready","created_at":"2026-08-27T12:00:00Z"}`)))
	_, err = records.Get(ctx, "tenant-a", "file-one")
	require.ErrorIs(t, err, ErrCorruptRecord)
}

func TestValidateBoundsTheRecord(t *testing.T) {
	t.Parallel()

	cases := map[string]func(*File){
		"no identifier":           func(f *File) { f.ID = "" },
		"no tenant":               func(f *File) { f.Tenant = "" },
		"no bytes named":          func(f *File) { f.blobKey = "" },
		"negative size":           func(f *File) { f.Bytes = -1 },
		"no created at":           func(f *File) { f.CreatedAt = time.Time{} },
		"no filename":             func(f *File) { f.Filename = "  " },
		"long filename":           func(f *File) { f.Filename = strings.Repeat("n", MaxFilenameLength+1) },
		"newline in the filename": func(f *File) { f.Filename = "notes\n.txt" },
		"unknown state":           func(f *File) { f.State = "archived" },
	}
	for name, damage := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			file := sampleFile("tenant-a", "file-one")
			damage(&file)
			require.ErrorIs(t, file.Validate(), ErrInvalidFile)
		})
	}

	file := sampleFile("tenant-a", "file-one")
	file.Purpose = "assistants"
	require.ErrorIs(t, file.Validate(), ErrInvalidPurpose)
}

func TestExpiredReadsTheBoundary(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)

	file := sampleFile("tenant-a", "file-one")
	require.False(t, file.Expired(at), "a file without an expiry never expires")

	file.ExpiresAt = at
	require.True(t, file.Expired(at), "the expiry moment is expired")
	require.False(t, file.Expired(at.Add(-time.Nanosecond)))
	require.True(t, file.Expired(at.Add(time.Nanosecond)))
}
