package revision

import (
	"context"
	"math"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agentstation/starmap/pkg/productfiles"
	"github.com/agentstation/starport/internal/sqlstore"
	"github.com/stretchr/testify/require"
)

const originalProjectionEpoch = "original"

func TestRevisionRecoverySnapshotReadsOriginalSingletonWithoutRotation(t *testing.T) {
	db := revisionReplayDB(t, revisionReplayConfig(t, "sqlite"))
	for _, present := range []bool{false, true} {
		t.Run(map[bool]string{false: "absent", true: "present"}[present], func(t *testing.T) {
			if present {
				_, err := db.ExecContext(t.Context(), "INSERT INTO authorization_revision(id,epoch,sequence) VALUES(1,'original-private-epoch',19)")
				require.NoError(t, err)
			}
			root := filepath.Join(t.TempDir(), "private")
			_, err := productfiles.CreateDirectory(root)
			require.NoError(t, err)
			result, err := db.SnapshotSQLite(t.Context(), filepath.Join(root, "snapshot"))
			require.NoError(t, err)
			view, err := sqlstore.OpenRelationalSnapshot(t.Context(), filepath.Join(root, "snapshot", "starport.db"), result.Snapshot, root)
			require.NoError(t, err)
			stamp, err := CaptureSQLSnapshotRecovery(t.Context(), view)
			require.NoError(t, err)
			if present {
				require.Equal(t, &Stamp{Epoch: "original-private-epoch", Sequence: 19}, stamp)
			} else {
				require.Nil(t, stamp)
			}
			conn, err := db.Conn(t.Context())
			require.NoError(t, err)
			actual, err := CaptureSQLRecovery(t.Context(), db, conn)
			require.NoError(t, err)
			require.Equal(t, stamp, actual)
			require.NoError(t, conn.Close())
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			_, err = CaptureSQLSnapshotRecovery(ctx, view)
			require.ErrorIs(t, err, context.Canceled)
			require.NoError(t, view.Close())
		})
	}
	_, err := CaptureSQLSnapshotRecovery(t.Context(), nil)
	require.ErrorIs(t, err, ErrRecoveryConflict)
}

func TestRevisionRecoveryPreimageRefusesInvalidRows(t *testing.T) {
	require.NoError(t, ValidateSQLRecoveryPreimage(nil))
	require.NoError(t, ValidateSQLRecoveryPreimage(&Stamp{Epoch: originalProjectionEpoch, Sequence: math.MaxInt64}))
	for _, stamp := range []Stamp{{}, {Epoch: originalProjectionEpoch}, {Sequence: 1}, {Epoch: strings.Repeat("e", 257), Sequence: 1}, {Epoch: originalProjectionEpoch, Sequence: math.MaxUint64}} {
		require.ErrorIs(t, ValidateSQLRecoveryPreimage(&stamp), ErrRecoveryConflict)
	}
}
