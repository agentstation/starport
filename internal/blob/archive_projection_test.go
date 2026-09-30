package blob

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

const archiveProjectionMissingOwner = "nil-owner"

func TestArchiveProjectionPreservesOriginalAndMatchesOwnerState(t *testing.T) {
	store, err := NewFilesystem(filepath.Join(snapshotDirectory(t), "private-source"))
	require.NoError(t, err)
	_, err = store.Publish(t.Context(), "old", strings.NewReader("original bytes"))
	require.NoError(t, err)
	require.NoError(t, store.Retire(t.Context(), "retired"))
	root := snapshotDirectory(t)
	archive := filepath.Join(root, "original.tar")
	original, err := Backup(t.Context(), store, archive)
	require.NoError(t, err)
	projection, err := OpenArchiveProjection(t.Context(), archive, root, original)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, projection.Close()) })
	old, err := store.InspectPublication(t.Context(), "old")
	require.NoError(t, err)
	_, err = store.Publish(t.Context(), "new", strings.NewReader("independent bytes"))
	require.NoError(t, err)
	next, err := store.InspectPublication(t.Context(), "new")
	require.NoError(t, err)
	require.NoError(t, store.Retire(t.Context(), "old"))
	require.NoError(t, projection.ApplyPublication(t.Context(), "new", PublicationState{Kind: publicationAbsent}, next, strings.NewReader("independent bytes")))
	require.NoError(t, projection.ApplyPublication(t.Context(), "old", old, PublicationState{Kind: publicationRetired}, nil))
	projected, err := projection.Snapshot(t.Context(), filepath.Join(root, "projected.tar"))
	require.NoError(t, err)
	actual, err := Backup(t.Context(), store, filepath.Join(root, "actual.tar"))
	require.NoError(t, err)
	require.Equal(t, actual, projected)
	retained, err := OpenSnapshot(t.Context(), archive, root, original)
	require.NoError(t, err)
	reader, err := retained.ReadPublished(t.Context(), "old")
	require.NoError(t, err)
	body, err := io.ReadAll(reader)
	require.NoError(t, err)
	require.NoError(t, reader.Close())
	require.Equal(t, "original bytes", string(body))
	_, err = retained.ReadPublished(t.Context(), "new")
	require.ErrorIs(t, err, ErrNotFound)
	require.NoError(t, retained.Close())
	for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%p", "%+p", "%#p"} {
		require.NotContains(t, fmt.Sprintf(verb, *projection), root)
		require.NotContains(t, fmt.Sprintf(verb, *projection), "original bytes")
	}
	require.NoError(t, projection.Close())
	require.Error(t, projection.ApplyPublication(t.Context(), "new", PublicationState{Kind: publicationAbsent}, next, strings.NewReader("independent bytes")))
}

func TestArchiveProjectionRefusesInvalidTransitionsWithoutChangingCopy(t *testing.T) {
	for _, name := range []string{"changed-preimage", "retired-resurrection", "missing-payload", "changed-payload", "wrong-size", "cancel", "delete", archiveProjectionMissingOwner} {
		t.Run(name, func(t *testing.T) {
			store, err := NewFilesystem(filepath.Join(snapshotDirectory(t), "source"))
			require.NoError(t, err)
			require.NoError(t, store.Retire(t.Context(), "retired"))
			root := snapshotDirectory(t)
			archive := filepath.Join(root, "original.tar")
			original, err := Backup(t.Context(), store, archive)
			require.NoError(t, err)
			projection, err := OpenArchiveProjection(t.Context(), archive, root, original)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, projection.Close()) })
			key, expected := "new", PublicationState{Kind: publicationAbsent}
			sum := sha256.Sum256([]byte("payload"))
			next := PublicationState{Kind: publicationLive, Size: 7, SHA256: hex.EncodeToString(sum[:])}
			var payload io.Reader = strings.NewReader("payload")
			ctx := t.Context()
			switch name {
			case "changed-preimage":
				expected.Kind = publicationRetired
			case "retired-resurrection":
				key, expected.Kind = publicationRetired, publicationRetired
			case "missing-payload":
				payload = nil
			case "changed-payload":
				payload = strings.NewReader("changed")
			case "wrong-size":
				next.Size++
			case "cancel":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			case "delete":
				next = PublicationState{Kind: publicationAbsent}
			case archiveProjectionMissingOwner:
				var absent *ArchiveProjection
				require.Error(t, absent.ApplyPublication(ctx, key, expected, next, payload))
			}
			if name != archiveProjectionMissingOwner {
				require.Error(t, projection.ApplyPublication(ctx, key, expected, next, payload))
			}
			after, err := projection.Snapshot(t.Context(), filepath.Join(root, "unchanged.tar"))
			require.NoError(t, err)
			require.Equal(t, original, after)
		})
	}
}
