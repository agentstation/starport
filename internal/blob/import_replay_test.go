package blob

import (
	"context"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func publicationStep() ImportPublicationStep {
	return ImportPublicationStep{Sequence: 1, EvidenceSHA256: strings.Repeat("a", 64), Key: "independent-asset", Expected: PublicationState{Kind: "absent"}, Next: PublicationState{Kind: "live", Size: 5, SHA256: blobDigest([]byte("bytes"))}}
}

func TestBlobPublicationReplayNativeHistoryAndExactPositions(t *testing.T) {
	for _, kind := range []string{"filesystem", "objectstore"} {
		t.Run(kind, func(t *testing.T) {
			target, store, archive, original := activationFixture(t, kind)
			replay := target.(ImportPublicationReplayer)
			inspect := target.(ImportReplayInspector)
			step := publicationStep()
			scratch := snapshotDirectory(t)
			receipt, err := replay.ReplayPublication(t.Context(), "operation", original, step, strings.NewReader("bytes"), scratch)
			require.NoError(t, err)
			position := ImportReplayPosition{Sequence: 1, ReceiptSHA256: receipt}
			require.NoError(t, inspect.CheckImportPosition(t.Context(), "operation", original, position))
			require.Error(t, target.(ImportInspector).CheckImport(t.Context(), "operation", original))
			_, err = target.(ImportInspector).SnapshotImport(t.Context(), filepath.Join(snapshotDirectory(t), "stale.tar"), "operation", original)
			require.Error(t, err)
			require.Error(t, target.(ImportActivator).ActivateImport(t.Context(), "operation", original, strings.Repeat("b", 64)))
			repeated, err := replay.ReplayPublication(t.Context(), "operation", original, step, nil, scratch)
			require.NoError(t, err)
			require.Equal(t, receipt, repeated)
			changed := step
			changed.EvidenceSHA256 = strings.Repeat("c", 64)
			_, err = replay.ReplayPublication(t.Context(), "operation", original, changed, nil, scratch)
			require.Error(t, err)
			retired := ImportPublicationStep{Sequence: 2, PreviousSHA256: receipt, EvidenceSHA256: strings.Repeat("d", 64), Key: step.Key, Expected: step.Next, Next: PublicationState{Kind: "retired"}}
			next, err := replay.ReplayPublication(t.Context(), "operation", original, retired, nil, scratch)
			require.NoError(t, err)
			_, err = replay.ReplayPublication(t.Context(), "operation", original, step, nil, scratch)
			require.NoError(t, err)
			state, err := store.(RecoveryPublicationReader).InspectPublication(t.Context(), step.Key)
			require.NoError(t, err)
			require.Equal(t, "retired", state.Kind)
			require.Error(t, inspect.CheckImportPosition(t.Context(), "operation", original, position))
			position = ImportReplayPosition{Sequence: 2, ReceiptSHA256: next}
			require.NoError(t, inspect.CheckImportPosition(t.Context(), "operation", original, position))
			snapshot, err := inspect.SnapshotImportAt(t.Context(), filepath.Join(snapshotDirectory(t), "later.tar"), "operation", original, position)
			require.NoError(t, err)
			require.Greater(t, snapshot.Objects, original.Objects)
			require.NoError(t, target.(ImportReplayActivator).ActivateImportAt(t.Context(), "operation", original, position, strings.Repeat("b", 64)))
			_, err = replay.ReplayPublication(t.Context(), "operation", original, retired, nil, scratch)
			require.Error(t, err)
			require.Error(t, target.Restore(t.Context(), archive, scratch, "operation", original))
		})
	}
}

type interruptedReplayRecords struct {
	activationRecords
	step   int
	writes int
	after  bool
}

var errReplayInterrupted = errors.New("publication replay interrupted")

func (r *interruptedReplayRecords) compare(ctx context.Context, key string, before, after []byte) error {
	r.writes++
	if r.writes == r.step && !r.after {
		return errReplayInterrupted
	}
	err := r.activationRecords.compare(ctx, key, before, after)
	if err == nil && r.writes == r.step {
		return errReplayInterrupted
	}
	return err
}

type interruptedReplayOwner struct {
	replayPublicationOwner
	after bool
}

func (o interruptedReplayOwner) replayWrite(ctx context.Context, key string, before, after PublicationState, input io.Reader) error {
	if !o.after {
		return errReplayInterrupted
	}
	if err := o.replayPublicationOwner.replayWrite(ctx, key, before, after, input); err != nil {
		return err
	}
	return errReplayInterrupted
}
func nativeReplayOwner(store Store) replayPublicationOwner {
	switch store := store.(type) {
	case *Filesystem:
		return store
	case *ObjectStore:
		return store
	default:
		panic("unsupported fixture")
	}
}

func TestBlobPublicationReplayCrashCutsKeepBarrierAndResume(t *testing.T) {
	for _, kind := range []string{"filesystem", "objectstore"} {
		t.Run(kind, func(t *testing.T) {
			for _, cut := range []string{"before-intent", "after-intent", "before-bytes", "after-bytes", "after-history", "after-cursor"} {
				t.Run(cut, func(t *testing.T) {
					target, store, _, original := activationFixture(t, kind)
					records := activationRecordsFor(t, target)
					var guarded activationRecords = records
					owner := nativeReplayOwner(store)
					step := publicationStep()
					scratch := snapshotDirectory(t)
					switch cut {
					case "before-intent":
						guarded = &interruptedReplayRecords{activationRecords: records, step: 1}
					case "after-intent":
						guarded = &interruptedReplayRecords{activationRecords: records, step: 1, after: true}
					case "before-bytes":
						owner = interruptedReplayOwner{replayPublicationOwner: owner}
					case "after-bytes":
						owner = interruptedReplayOwner{replayPublicationOwner: owner, after: true}
					case "after-history":
						guarded = &interruptedReplayRecords{activationRecords: records, step: 2, after: true}
					case "after-cursor":
						guarded = &interruptedReplayRecords{activationRecords: records, step: 3, after: true}
					}
					receipt, err := replayPublication(t.Context(), guarded, owner, "operation", original, step, strings.NewReader("bytes"), scratch)
					require.ErrorIs(t, err, errReplayInterrupted)
					require.Empty(t, receipt)
					if cut != "before-intent" {
						require.Error(t, target.(ImportActivator).ActivateImport(t.Context(), "operation", original, strings.Repeat("b", 64)), "pending or completed nonzero replay cannot use an unbound activation")
					}
					// Before the intent write, the zero position remains valid, but no activation is attempted.
					if cut == "before-intent" {
						require.NoError(t, target.(ImportInspector).CheckImport(t.Context(), "operation", original))
					}
					receipt, err = target.(ImportPublicationReplayer).ReplayPublication(t.Context(), "operation", original, step, strings.NewReader("bytes"), scratch)
					require.NoError(t, err)
					require.NotEmpty(t, receipt)
					require.NoError(t, target.(ImportReplayInspector).CheckImportPosition(t.Context(), "operation", original, ImportReplayPosition{Sequence: 1, ReceiptSHA256: receipt}))
				})
			}
		})
	}
}

func TestBlobPublicationReplayRefusesInvalidBytesAndRetirementResurrection(t *testing.T) {
	target, store, _, original := activationFixture(t, "filesystem")
	replay := target.(ImportPublicationReplayer)
	step := publicationStep()
	scratch := snapshotDirectory(t)
	for _, payload := range []string{"bad", "bytes-extra"} {
		_, err := replay.ReplayPublication(t.Context(), "operation", original, step, strings.NewReader(payload), scratch)
		require.Error(t, err)
		require.NoError(t, target.(ImportInspector).CheckImport(t.Context(), "operation", original))
	}
	_, err := replay.ReplayPublication(t.Context(), "wrong", original, step, strings.NewReader("bytes"), scratch)
	require.Error(t, err)
	require.NoError(t, store.(*Filesystem).Retire(t.Context(), step.Key))
	_, err = replay.ReplayPublication(t.Context(), "operation", original, step, strings.NewReader("bytes"), scratch)
	require.Error(t, err)
	step.Expected = PublicationState{Kind: "retired"}
	_, err = replay.ReplayPublication(t.Context(), "operation", original, step, strings.NewReader("bytes"), scratch)
	require.Error(t, err)
	state, err := store.(RecoveryPublicationReader).InspectPublication(t.Context(), step.Key)
	require.NoError(t, err)
	require.Equal(t, "retired", state.Kind)
}

func TestBlobPublicationRetirementReplayLostReplyAndPortableHistory(t *testing.T) {
	for _, kind := range []string{"filesystem", "objectstore"} {
		t.Run(kind, func(t *testing.T) {
			target, store, _, original := activationFixture(t, kind)
			step := publicationStep()
			scratch := snapshotDirectory(t)
			first, err := target.(ImportPublicationReplayer).ReplayPublication(t.Context(), "operation", original, step, strings.NewReader("bytes"), scratch)
			require.NoError(t, err)
			retirement := ImportPublicationStep{Sequence: 2, PreviousSHA256: first, EvidenceSHA256: strings.Repeat("b", 64), Key: step.Key, Expected: step.Next, Next: PublicationState{Kind: "retired"}}
			_, err = replayPublication(t.Context(), activationRecordsFor(t, target), interruptedReplayOwner{replayPublicationOwner: nativeReplayOwner(store), after: true}, "operation", original, retirement, nil, scratch)
			require.ErrorIs(t, err, errReplayInterrupted)
			_, err = target.(ImportReplayInspector).SnapshotImportAt(t.Context(), filepath.Join(snapshotDirectory(t), "pending.tar"), "operation", original, ImportReplayPosition{Sequence: 1, ReceiptSHA256: first})
			require.Error(t, err)
			second, err := target.(ImportPublicationReplayer).ReplayPublication(t.Context(), "operation", original, retirement, nil, scratch)
			require.NoError(t, err)
			archive := filepath.Join(snapshotDirectory(t), "retained.tar")
			snapshot, err := target.(ImportReplayInspector).SnapshotImportAt(t.Context(), archive, "operation", original, ImportReplayPosition{Sequence: 2, ReceiptSHA256: second})
			require.NoError(t, err)
			restored, err := FilesystemRestoreTarget(filepath.Join(snapshotDirectory(t), "second-target"))
			require.NoError(t, err)
			require.NoError(t, restored.Restore(t.Context(), archive, scratch, "later-operation", snapshot))
			require.NoError(t, restored.(ImportInspector).CheckImport(t.Context(), "later-operation", snapshot))
			restoredView, err := OpenSnapshot(t.Context(), archive, scratch, snapshot)
			require.NoError(t, err)
			defer func() { require.NoError(t, restoredView.Close()) }()
			state, err := restoredView.(RecoveryPublicationReader).InspectPublication(t.Context(), step.Key)
			require.NoError(t, err)
			require.Equal(t, "retired", state.Kind)
		})
	}
}

func TestBlobPublicationReplayMultipartIndependentAsset(t *testing.T) {
	target, store, _, original := activationFixture(t, "objectstore")
	payload := strings.Repeat("retain", 1<<20)
	step := publicationStep()
	step.Next = PublicationState{Kind: publicationLive, Size: int64(len(payload)), SHA256: blobDigest([]byte(payload))}
	receipt, err := target.(ImportPublicationReplayer).ReplayPublication(t.Context(), "operation", original, step, strings.NewReader(payload), snapshotDirectory(t))
	require.NoError(t, err)
	require.NoError(t, target.(ImportReplayInspector).CheckImportPosition(t.Context(), "operation", original, ImportReplayPosition{Sequence: 1, ReceiptSHA256: receipt}))
	actual, err := store.(RecoveryPublicationReader).InspectPublication(t.Context(), step.Key)
	require.NoError(t, err)
	require.Equal(t, step.Next, actual)
}
