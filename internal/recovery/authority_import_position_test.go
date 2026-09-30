package recovery

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/sqlstore"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

type positionedImportedAuthority interface {
	ApproveImportedAuthorityAt(context.Context, storage.IncarnationProvider, ImportedAuthorityRequest, sqlstore.RelationalReplayPosition) (Record, error)
}

type positionedAuthorityStore interface {
	storage.IncarnationStore
	storage.TimeBoundStore
}

type lostPositionedAuthority struct{ storage.IncarnationProvider }

func (p lostPositionedAuthority) BindIncarnation(ctx context.Context, identity string) (storage.IncarnationStore, error) {
	bound, err := p.IncarnationProvider.BindIncarnation(ctx, identity)
	if err != nil {
		return nil, err
	}
	complete, ok := bound.(positionedAuthorityStore)
	if !ok {
		return nil, ErrClosed
	}
	return lostPositionedAuthorityReply{complete}, nil
}

type lostPositionedAuthorityReply struct{ positionedAuthorityStore }

func (s lostPositionedAuthorityReply) CompareAndSwap(ctx context.Context, mutations []storage.CompareAndSwapMutation, live ...string) error {
	if err := s.positionedAuthorityStore.CompareAndSwap(ctx, mutations, live...); err != nil {
		return err
	}
	return errors.New("lost native authority reply")
}

func TestImportedAuthorityBindsFinalSQLPosition(t *testing.T) {
	for _, kind := range []string{sqlstore.TypeSQLite, sqlstore.TypePostgres, sqlstore.TypeMySQL} {
		t.Run(kind, func(t *testing.T) {
			witness, _, guard := closedImportGuardFixture(t, kind)
			activate, ok := any(witness).(positionedImportedAuthority)
			require.True(t, ok, "complete recovery approval must bind its final SQL replay position")
			_, kv, backend := freshTestStores(t)
			identity, err := backend.ObserveIncarnation(t.Context())
			require.NoError(t, err)
			request := ImportedAuthorityRequest{Closed: guard.Boundary, BackendID: identity,
				Evidence: "position-bound-final-proof", OperationID: guard.Import.OperationID,
				Snapshot: guard.Snapshot, Import: guard.Import, DecisionSHA256: strings.Repeat("d", 64)}
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			defer cancel()
			assertClosed := func() {
				current, err := witness.Current(t.Context(), request.Closed.DeploymentID)
				require.NoError(t, err)
				require.Equal(t, request.Closed, current)
				require.ErrorIs(t, witness.db.CheckImportBarrier(t.Context()), sqlstore.ErrImportRestricted)
			}
			_, err = activate.ApproveImportedAuthorityAt(t.Context(), backend, request, guard.Position)
			require.Error(t, err, "native work inside the SQL transaction requires a deadline")
			for _, position := range []sqlstore.RelationalReplayPosition{{}, {Sequence: 1, ReceiptSHA256: strings.Repeat("e", 64)}, {Sequence: 2, ReceiptSHA256: guard.Position.ReceiptSHA256}} {
				_, err = activate.ApproveImportedAuthorityAt(ctx, backend, request, position)
				require.Error(t, err)
				assertClosed()
				_, err = kv.Get(t.Context(), authorityKey)
				require.ErrorIs(t, err, storage.ErrNotFound)
			}
			changed := request
			changed.Closed.Epoch++
			_, err = activate.ApproveImportedAuthorityAt(ctx, backend, changed, guard.Position)
			require.Error(t, err)
			assertClosed()
			_, err = activate.ApproveImportedAuthorityAt(ctx, lostPositionedAuthority{backend}, request, guard.Position)
			require.ErrorContains(t, err, "lost native authority reply")
			assertClosed()
			original, err := kv.Get(t.Context(), authorityKey)
			require.NoError(t, err)
			approved, err := activate.ApproveImportedAuthorityAt(ctx, backend, request, guard.Position)
			require.NoError(t, err)
			require.True(t, approved.Open)
			require.NoError(t, witness.db.CheckActivatedRelationalImportAt(ctx, request.Snapshot, request.Import, guard.Position, request.DecisionSHA256))
			actual, err := kv.Get(t.Context(), authorityKey)
			require.NoError(t, err)
			require.Equal(t, original, actual, "retry preserves the first native authority commit")
			again, err := activate.ApproveImportedAuthorityAt(ctx, lostPositionedAuthority{backend}, request, guard.Position)
			require.NoError(t, err, "completed retry must not repeat native writes")
			require.Equal(t, approved, again)
			for _, field := range []string{"decision", "evidence", "backend"} {
				changed = request
				switch field {
				case "decision":
					changed.DecisionSHA256 = strings.Repeat("e", 64)
				case "evidence":
					changed.Evidence = "substituted-proof"
				case "backend":
					changed.BackendID = "substituted-backend"
				}
				_, err = activate.ApproveImportedAuthorityAt(ctx, backend, changed, guard.Position)
				require.Error(t, err)
			}
			withdrawn, err := witness.Close(ctx, approved)
			require.NoError(t, err)
			_, err = activate.ApproveImportedAuthorityAt(ctx, lostPositionedAuthority{backend}, request, guard.Position)
			require.ErrorIs(t, err, ErrConflict)
			current, err := witness.Current(ctx, request.Closed.DeploymentID)
			require.NoError(t, err)
			require.Equal(t, withdrawn, current)
			require.NoError(t, witness.db.CheckActivatedRelationalImportAt(ctx, request.Snapshot, request.Import, guard.Position, request.DecisionSHA256), "historical completion does not grant current permission")
		})
	}
}
