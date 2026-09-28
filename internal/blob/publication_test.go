package blob_test

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/blob"
	"github.com/stretchr/testify/require"
)

func TestPublicationContract(t *testing.T) {
	runContract(t, "immutable publication and retirement", func(t *testing.T, store blob.Store) {
		ctx := t.Context()
		for _, content := range []string{"", "payload"} {
			key := "key-" + content
			info, err := store.Publish(ctx, key, strings.NewReader(content))
			require.NoError(t, err)
			require.Equal(t, int64(len(content)), info.Size)
			reader, err := store.ReadPublished(ctx, key)
			require.NoError(t, err)
			require.Equal(t, content, read(t, reader))
			_, err = store.Publish(ctx, key, strings.NewReader("replacement"))
			require.ErrorIs(t, err, blob.ErrPublicationExists)
			reader, err = store.ReadPublished(ctx, key)
			require.NoError(t, err)
			require.Equal(t, content, read(t, reader))
			require.NoError(t, store.Retire(ctx, key))
			require.NoError(t, store.Retire(ctx, key))
			_, err = store.Publish(ctx, key, strings.NewReader(content))
			require.ErrorIs(t, err, blob.ErrPublicationExists)
			_, err = store.StatPublished(ctx, key)
			require.ErrorIs(t, err, blob.ErrNotFound)
			_, err = store.ReadPublished(ctx, key)
			require.ErrorIs(t, err, blob.ErrNotFound)
		}
		require.NoError(t, store.Retire(ctx, "never-published"))
		_, err := store.Publish(ctx, "never-published", strings.NewReader("late"))
		require.ErrorIs(t, err, blob.ErrPublicationExists)
	})
}

type pausedPublicationReader struct {
	entered, release chan struct{}
	once             sync.Once
	reader           io.Reader
}

func (r *pausedPublicationReader) Read(p []byte) (int, error) {
	r.once.Do(func() { close(r.entered); <-r.release })
	return r.reader.Read(p)
}

func TestRetirementRejectsInFlightPublication(t *testing.T) {
	runContract(t, "retire while writer reads", func(t *testing.T, store blob.Store) {
		reader := &pausedPublicationReader{entered: make(chan struct{}), release: make(chan struct{}), reader: strings.NewReader("late")}
		var release sync.Once
		t.Cleanup(func() { release.Do(func() { close(reader.release) }) })
		done := make(chan error, 1)
		go func() { _, err := store.Publish(t.Context(), "delayed", reader); done <- err }()
		select {
		case <-reader.entered:
		case <-time.After(5 * time.Second):
			t.Fatal("writer did not reach input")
		}
		require.NoError(t, store.Retire(t.Context(), "delayed"))
		release.Do(func() { close(reader.release) })
		select {
		case err := <-done:
			require.ErrorIs(t, err, blob.ErrPublicationExists)
		case <-time.After(5 * time.Second):
			t.Fatal("writer did not finish")
		}
		_, err := store.ReadPublished(t.Context(), "delayed")
		require.ErrorIs(t, err, blob.ErrNotFound)
	})
}

func TestPublicationFailureLeavesIdentityAvailable(t *testing.T) {
	runContract(t, "failed input and cancellation", func(t *testing.T, store blob.Store) {
		_, err := store.Publish(t.Context(), "input", io.MultiReader(strings.NewReader("partial"), brokenPublicationReader{}))
		require.Error(t, err)
		_, err = store.StatPublished(t.Context(), "input")
		require.ErrorIs(t, err, blob.ErrNotFound)
		_, err = store.Publish(t.Context(), "input", strings.NewReader("whole"))
		require.NoError(t, err)
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		_, err = store.Publish(ctx, "cancelled", strings.NewReader("x"))
		require.ErrorIs(t, err, context.Canceled)
		require.ErrorIs(t, store.Retire(ctx, "cancelled"), context.Canceled)
		_, err = store.Publish(t.Context(), "cancelled", strings.NewReader("x"))
		require.NoError(t, err)
		for _, key := range []string{"", "../bad", "bad/path"} {
			_, err = store.Publish(t.Context(), key, strings.NewReader("x"))
			require.ErrorIs(t, err, blob.ErrInvalidKey)
			require.ErrorIs(t, store.Retire(t.Context(), key), blob.ErrInvalidKey)
			_, err = store.ReadPublished(t.Context(), key)
			require.ErrorIs(t, err, blob.ErrInvalidKey)
			_, err = store.StatPublished(t.Context(), key)
			require.ErrorIs(t, err, blob.ErrInvalidKey)
		}
	})
}

type brokenPublicationReader struct{}

func (brokenPublicationReader) Read([]byte) (int, error) { return 0, errors.New("broken input") }
