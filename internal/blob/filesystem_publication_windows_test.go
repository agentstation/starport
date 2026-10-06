package blob_test

import (
	"strings"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/blob"
	"github.com/stretchr/testify/require"
)

func TestRetireWaitsForOpenReader(t *testing.T) {
	store, err := blob.NewFilesystem(t.TempDir())
	require.NoError(t, err)
	_, err = store.Publish(t.Context(), "held", strings.NewReader("payload"))
	require.NoError(t, err)
	reader, err := store.ReadPublished(t.Context(), "held")
	require.NoError(t, err)
	// Go opens the reader without FILE_SHARE_DELETE, so the replace fails
	// until the reader closes.
	closed := make(chan error, 1)
	time.AfterFunc(100*time.Millisecond, func() { closed <- reader.Close() })
	started := time.Now()
	require.NoError(t, store.Retire(t.Context(), "held"))
	require.Less(t, time.Since(started), 2*time.Second)
	require.NoError(t, <-closed)
	_, err = store.StatPublished(t.Context(), "held")
	require.ErrorIs(t, err, blob.ErrNotFound)
	_, err = store.ReadPublished(t.Context(), "held")
	require.ErrorIs(t, err, blob.ErrNotFound)
	_, err = store.Publish(t.Context(), "held", strings.NewReader("payload"))
	require.ErrorIs(t, err, blob.ErrPublicationExists)
}
