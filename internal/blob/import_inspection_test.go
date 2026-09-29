package blob

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBlobImportInspection(t *testing.T) {
	for _, kind := range []string{"filesystem", "objectstore"} {
		t.Run(kind, func(t *testing.T) {
			target, store, _, original := activationFixture(t, kind)
			inspector := target.(ImportInspector)
			path := filepath.Join(snapshotDirectory(t), "inspected.tar")
			receipt, err := inspector.SnapshotImport(t.Context(), path, "operation", original)
			require.NoError(t, err)
			require.Equal(t, original, receipt)
			view, err := OpenSnapshot(t.Context(), path, snapshotDirectory(t), receipt)
			require.NoError(t, err)
			reader, err := view.Get(t.Context(), "data")
			require.NoError(t, err)
			data, err := io.ReadAll(reader)
			require.NoError(t, err)
			require.NoError(t, reader.Close())
			require.Equal(t, "original", string(data))
			require.NoError(t, view.Close())
			require.NoError(t, inspector.CheckImport(t.Context(), "operation", original))
			_, err = Backup(t.Context(), store, filepath.Join(snapshotDirectory(t), "ordinary.tar"))
			require.ErrorIs(t, err, ErrImportRestricted)
			require.Error(t, inspector.CheckImport(t.Context(), "wrong", original))
			changed := original
			changed.SHA256 = strings.Repeat("a", 64)
			require.Error(t, inspector.CheckImport(t.Context(), "operation", changed))
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			require.ErrorIs(t, inspector.CheckImport(ctx, "operation", original), context.Canceled)
			require.NoError(t, target.(ImportActivator).ActivateImport(t.Context(), "operation", original, strings.Repeat("b", 64)))
			require.Error(t, inspector.CheckImport(t.Context(), "operation", original))
			receipt, err = inspector.SnapshotImport(t.Context(), filepath.Join(snapshotDirectory(t), "active.tar"), "operation", original)
			require.Error(t, err)
			require.Empty(t, receipt)
		})
	}
}

func TestBlobImportInspectionRefusesIncompleteControls(t *testing.T) {
	for _, kind := range []string{"filesystem", "objectstore"} {
		t.Run(kind, func(t *testing.T) {
			for _, broken := range []string{"barrier", "current", "history"} {
				t.Run(broken, func(t *testing.T) {
					target, _, _, original := activationFixture(t, kind)
					claim, err := makeBlobClaim("operation", original)
					require.NoError(t, err)
					key, history, pending, _, err := makeActivation(claim, strings.Repeat("a", 64))
					require.NoError(t, err)
					switch broken {
					case "barrier":
						replaceActivationRecord(t, target, blobImportKey, nil)
					case "current":
						replaceActivationRecord(t, target, blobActivationCurrent, pending)
					case "history":
						replaceActivationRecord(t, target, key, history)
					}
					receipt, err := target.(ImportInspector).SnapshotImport(t.Context(), filepath.Join(snapshotDirectory(t), "refused.tar"), "operation", original)
					require.Error(t, err)
					require.Empty(t, receipt)
				})
			}
		})
	}
}

func TestBlobImportInspectionDiscardsChangedControlState(t *testing.T) {
	target, _, _, original := activationFixture(t, "filesystem")
	inspector := target.(ImportInspector)
	guard := func(ctx context.Context) error { return inspector.CheckImport(ctx, "operation", original) }
	walk := func(ctx context.Context, yield blobObjectVisitor) error {
		if err := yield(blobAddress(objectsDir, "data"), 8, strings.NewReader("original")); err != nil {
			return err
		}
		return target.(ImportActivator).ActivateImport(ctx, "operation", original, strings.Repeat("a", 64))
	}
	receipt, err := snapshotBlobImport(t.Context(), filepath.Join(snapshotDirectory(t), "interrupted.tar"), walk, guard)
	require.ErrorIs(t, err, ErrImportRestricted)
	require.Empty(t, receipt)
}

func TestBlobImportInspectionCannotWriteInsideSource(t *testing.T) {
	target, _, _, original := activationFixture(t, "filesystem")
	root := target.(filesystemRestoreTarget).destination
	for _, parent := range []string{root, filepath.Join(root, objectsDir), filepath.Join(root, ".starport")} {
		output := filepath.Join(parent, "inspection.tar")
		receipt, err := target.(ImportInspector).SnapshotImport(t.Context(), output, "operation", original)
		require.Error(t, err)
		require.Empty(t, receipt)
		_, err = os.Lstat(output)
		require.ErrorIs(t, err, os.ErrNotExist)
	}
}
