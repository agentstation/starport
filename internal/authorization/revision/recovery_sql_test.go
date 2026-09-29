package revision

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"testing"

	"github.com/agentstation/starport/internal/sqlstore"
	"github.com/stretchr/testify/require"
)

func TestRevisionRecoverySQLNativeAtomicReplacement(t *testing.T) {
	for _, backend := range []string{"sqlite", "postgres", "mysql"} {
		for _, before := range []*Stamp{nil, {Epoch: "old-epoch", Sequence: math.MaxInt64}} {
			name := backend + "/absent"
			if before != nil {
				name = backend + "/present"
			}
			t.Run(name, func(t *testing.T) {
				db, snapshot, identity := revisionReplayTarget(t, backend, before)
				transition, err := NewSQLRecoveryTransition(before, RecoveryAuthority{RecoveryID: identity.OperationID, Epoch: "accepted-fresh-epoch"})
				require.NoError(t, err)
				digest, err := transition.Digest()
				require.NoError(t, err)
				step := sqlstore.RelationalReplayStep{Sequence: 1, EvidenceSHA256: digest, TransitionSHA256: digest}
				interrupted := errors.New("failure after authority replacement")
				apply := func(ctx context.Context, conn *sql.Conn) error {
					captured, err := CaptureSQLRecovery(ctx, db, conn)
					require.NoError(t, err)
					require.Equal(t, before, captured)
					if err := ApplySQLRecovery(ctx, db, conn, transition); err != nil {
						return err
					}
					_, err = conn.ExecContext(ctx, db.Bind("INSERT INTO users(id,subject,revision,record) VALUES(?,?,?,?)"), "person", "issuer:person", 1, `{"schema_version":1,"revision":1,"user":{"id":"person","subject":"issuer:person","created_at":"2026-09-29T00:00:00Z","updated_at":"2026-09-29T00:00:00Z"}}`)
					return err
				}
				_, err = db.ReplayRelationalImport(t.Context(), snapshot, identity, step, func(ctx context.Context, conn *sql.Conn) error {
					if err := apply(ctx, conn); err != nil {
						return err
					}
					return interrupted
				})
				require.ErrorIs(t, err, interrupted)
				got, err := NewSQL(db, nil).Read(t.Context())
				if before == nil {
					require.ErrorIs(t, err, sql.ErrNoRows)
				} else {
					require.NoError(t, err)
					require.Equal(t, *before, got)
				}
				var count int
				require.NoError(t, db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM users").Scan(&count))
				require.Zero(t, count)
				receipt, err := db.ReplayRelationalImport(t.Context(), snapshot, identity, step, apply)
				require.NoError(t, err)
				again, err := db.ReplayRelationalImport(t.Context(), snapshot, identity, step, func(context.Context, *sql.Conn) error { return errors.New("retry must not apply again") })
				require.NoError(t, err)
				require.Equal(t, receipt, again)
				got, err = NewSQL(db, nil).Read(t.Context())
				require.NoError(t, err)
				require.Equal(t, Stamp{Epoch: "accepted-fresh-epoch", Sequence: 1}, got)
				require.NoError(t, db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM users").Scan(&count))
				require.Equal(t, 1, count)
				require.ErrorIs(t, db.CheckImportBarrier(t.Context()), sqlstore.ErrImportRestricted)
			})
		}
	}
}

func TestRevisionRecoverySQLRefusesChangedOrAdditionalRows(t *testing.T) {
	for _, backend := range []string{"sqlite", "postgres", "mysql"} {
		t.Run(backend, func(t *testing.T) {
			before := Stamp{Epoch: "Old-Epoch", Sequence: 19}
			db, snapshot, identity := revisionReplayTarget(t, backend, &before)
			for _, expected := range []*Stamp{nil, {Epoch: "old-epoch", Sequence: 19}, {Epoch: before.Epoch, Sequence: 18}} {
				transition, err := NewSQLRecoveryTransition(expected, RecoveryAuthority{RecoveryID: identity.OperationID, Epoch: "fresh"})
				require.NoError(t, err)
				digest, err := transition.Digest()
				require.NoError(t, err)
				_, err = db.ReplayRelationalImport(t.Context(), snapshot, identity, sqlstore.RelationalReplayStep{Sequence: 1, EvidenceSHA256: digest, TransitionSHA256: digest}, func(ctx context.Context, conn *sql.Conn) error { return ApplySQLRecovery(ctx, db, conn, transition) })
				require.ErrorIs(t, err, ErrRecoveryConflict)
			}
			_, err := db.ExecContext(t.Context(), db.Bind("INSERT INTO authorization_revision(id,epoch,sequence) VALUES(2,?,1)"), "unsupported-extra-row")
			require.NoError(t, err)
			transition, err := NewSQLRecoveryTransition(&before, RecoveryAuthority{RecoveryID: identity.OperationID, Epoch: "fresh"})
			require.NoError(t, err)
			digest, err := transition.Digest()
			require.NoError(t, err)
			_, err = db.ReplayRelationalImport(t.Context(), snapshot, identity, sqlstore.RelationalReplayStep{Sequence: 1, EvidenceSHA256: digest, TransitionSHA256: digest}, func(ctx context.Context, conn *sql.Conn) error { return ApplySQLRecovery(ctx, db, conn, transition) })
			require.ErrorIs(t, err, ErrRecoveryConflict)
			got, err := NewSQL(db, nil).Read(t.Context())
			require.NoError(t, err)
			require.Equal(t, before, got)
		})
	}
}
