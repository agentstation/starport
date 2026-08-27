package blob_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/agentstation/starport/internal/blob"
)

// backend names one implementation of the contract and opens a fresh instance
// of it. Every case below runs against every entry, so a backend earns the
// contract by passing the same cases rather than by carrying its own suite.
type backend struct {
	name string
	open func(t *testing.T) blob.Store
}

func backends(t *testing.T) []backend {
	t.Helper()
	return []backend{
		{
			name: "filesystem",
			open: func(t *testing.T) blob.Store {
				t.Helper()
				store, err := blob.NewFilesystem(t.TempDir())
				require.NoError(t, err)
				return store
			},
		},
		{
			name: "objectstore",
			open: func(t *testing.T) blob.Store {
				t.Helper()
				_, endpoint := newObjectServer(t, "starport-files")
				store, err := blob.NewObjectStore(context.Background(), blob.ObjectStoreOptions{
					Bucket:          "starport-files",
					Region:          "us-east-1",
					Endpoint:        endpoint,
					Prefix:          "deployment-one",
					AccessKeyID:     "test-access-key",
					SecretAccessKey: "test-secret-key",
				})
				require.NoError(t, err)
				return store
			},
		},
	}
}

// runContract holds the cases the contract defines. A new backend joins the
// table above and runs them unchanged.
func runContract(t *testing.T, name string, body func(t *testing.T, store blob.Store)) {
	t.Helper()
	for _, b := range backends(t) {
		t.Run(b.name+"/"+name, func(t *testing.T) {
			t.Parallel()
			body(t, b.open(t))
		})
	}
}

func read(t *testing.T, r io.ReadCloser) string {
	t.Helper()
	defer func() { require.NoError(t, r.Close()) }()
	data, err := io.ReadAll(r)
	require.NoError(t, err)
	return string(data)
}

func TestContractPutGetStatDelete(t *testing.T) {
	t.Parallel()
	runContract(t, "round trip", func(t *testing.T, store blob.Store) {
		ctx := context.Background()
		payload := strings.Repeat("starport", 512)

		info, err := store.Put(ctx, "round-trip", strings.NewReader(payload))
		require.NoError(t, err)
		require.Equal(t, "round-trip", info.Key)
		require.Equal(t, int64(len(payload)), info.Size)

		stat, err := store.Stat(ctx, "round-trip")
		require.NoError(t, err)
		require.Equal(t, int64(len(payload)), stat.Size)

		reader, err := store.Get(ctx, "round-trip")
		require.NoError(t, err)
		require.Equal(t, payload, read(t, reader))

		require.NoError(t, store.Delete(ctx, "round-trip"))

		_, err = store.Get(ctx, "round-trip")
		require.ErrorIs(t, err, blob.ErrNotFound)
		_, err = store.Stat(ctx, "round-trip")
		require.ErrorIs(t, err, blob.ErrNotFound)
	})
}

func TestContractEmptyObject(t *testing.T) {
	t.Parallel()
	// A zero-byte upload is a real upload. A backend that treats an empty
	// object as an absent one would report the wrong reason for a later read.
	runContract(t, "empty object", func(t *testing.T, store blob.Store) {
		ctx := context.Background()

		info, err := store.Put(ctx, "empty", bytes.NewReader(nil))
		require.NoError(t, err)
		require.Equal(t, int64(0), info.Size)

		stat, err := store.Stat(ctx, "empty")
		require.NoError(t, err)
		require.Equal(t, int64(0), stat.Size)

		reader, err := store.Get(ctx, "empty")
		require.NoError(t, err)
		require.Empty(t, read(t, reader))
	})
}

func TestContractMissingKey(t *testing.T) {
	t.Parallel()
	runContract(t, "missing key", func(t *testing.T, store blob.Store) {
		ctx := context.Background()

		_, err := store.Get(ctx, "absent")
		require.ErrorIs(t, err, blob.ErrNotFound)

		_, err = store.Stat(ctx, "absent")
		require.ErrorIs(t, err, blob.ErrNotFound)

		// A repeated delete is safe, so a sweep that runs twice over the same
		// record does not fail the second time.
		require.NoError(t, store.Delete(ctx, "absent"))
		require.NoError(t, store.Delete(ctx, "absent"))
	})
}

func TestContractOverwrite(t *testing.T) {
	t.Parallel()
	runContract(t, "overwrite", func(t *testing.T, store blob.Store) {
		ctx := context.Background()

		_, err := store.Put(ctx, "overwritten", strings.NewReader("the first value"))
		require.NoError(t, err)

		info, err := store.Put(ctx, "overwritten", strings.NewReader("second"))
		require.NoError(t, err)
		require.Equal(t, int64(len("second")), info.Size)

		reader, err := store.Get(ctx, "overwritten")
		require.NoError(t, err)
		require.Equal(t, "second", read(t, reader))

		// The size follows the new value. A backend that appends rather than
		// replaces would report the sum of both writes.
		stat, err := store.Stat(ctx, "overwritten")
		require.NoError(t, err)
		require.Equal(t, int64(len("second")), stat.Size)
	})
}

// errAfter yields n bytes and then fails. It stands for a client that drops
// its connection in the middle of an upload.
type errAfter struct {
	remaining int
	err       error
}

func (e *errAfter) Read(p []byte) (int, error) {
	if e.remaining <= 0 {
		return 0, e.err
	}
	n := len(p)
	if n > e.remaining {
		n = e.remaining
	}
	for i := 0; i < n; i++ {
		p[i] = 'x'
	}
	e.remaining -= n
	return n, nil
}

func TestContractInterruptedPutLeavesNothing(t *testing.T) {
	t.Parallel()
	// This is the property the file record depends on. FIL3 writes a pending
	// record, then the bytes, then commits. A partial object that reads back
	// as a whole one would let a committed record serve a truncated file.
	runContract(t, "interrupted put leaves nothing", func(t *testing.T, store blob.Store) {
		ctx := context.Background()
		broken := errors.New("the client went away")

		_, err := store.Put(ctx, "interrupted", &errAfter{remaining: 4096, err: broken})
		require.ErrorIs(t, err, broken)

		_, err = store.Get(ctx, "interrupted")
		require.ErrorIs(t, err, blob.ErrNotFound)
		_, err = store.Stat(ctx, "interrupted")
		require.ErrorIs(t, err, blob.ErrNotFound)
	})
}

func TestContractInterruptedPutKeepsThePriorObject(t *testing.T) {
	t.Parallel()
	// The other half of the same property. A backend that truncates the target
	// before it writes would destroy a good object on a failed overwrite.
	runContract(t, "interrupted put keeps the prior object", func(t *testing.T, store blob.Store) {
		ctx := context.Background()

		_, err := store.Put(ctx, "kept", strings.NewReader("the durable value"))
		require.NoError(t, err)

		_, err = store.Put(ctx, "kept", &errAfter{remaining: 4096, err: errors.New("the client went away")})
		require.Error(t, err)

		reader, err := store.Get(ctx, "kept")
		require.NoError(t, err)
		require.Equal(t, "the durable value", read(t, reader))
	})
}

func TestContractCanceledContext(t *testing.T) {
	t.Parallel()
	runContract(t, "canceled context", func(t *testing.T, store blob.Store) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		_, err := store.Put(ctx, "canceled", strings.NewReader("value"))
		require.ErrorIs(t, err, context.Canceled)

		_, err = store.Get(context.Background(), "canceled")
		require.ErrorIs(t, err, blob.ErrNotFound)
	})
}

func TestContractRejectsAnInvalidKey(t *testing.T) {
	t.Parallel()
	// Every operation refuses the same set, so a caller cannot reach an object
	// through one method that another method refuses to name.
	runContract(t, "rejects an invalid key", func(t *testing.T, store blob.Store) {
		ctx := context.Background()
		for _, key := range []string{"", "a/b", `a\b`, ".", "..", "spaced key", strings.Repeat("k", blob.MaxKeyLength+1)} {
			_, err := store.Put(ctx, key, strings.NewReader("value"))
			require.ErrorIsf(t, err, blob.ErrInvalidKey, "put %q", key)

			_, err = store.Get(ctx, key)
			require.ErrorIsf(t, err, blob.ErrInvalidKey, "get %q", key)

			_, err = store.Stat(ctx, key)
			require.ErrorIsf(t, err, blob.ErrInvalidKey, "stat %q", key)

			require.ErrorIsf(t, store.Delete(ctx, key), blob.ErrInvalidKey, "delete %q", key)
		}
	})
}

func TestContractBackendNamesItself(t *testing.T) {
	t.Parallel()
	runContract(t, "names itself", func(t *testing.T, store blob.Store) {
		require.NotEmpty(t, store.Backend())
	})
}
