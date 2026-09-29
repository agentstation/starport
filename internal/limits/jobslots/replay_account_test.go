package jobslots

import (
	"context"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

type slotReadOnly struct{ RecoveryReader }
type slotReverseOrder struct{ slotReplaySnapshot }

func (s slotReverseOrder) Enumerate(_ context.Context, visit func(storage.TransferRecord) error) error {
	keys := slices.Sorted(maps.Keys(s.slotReplaySnapshot))
	slices.Reverse(keys)
	for _, key := range keys {
		if err := visit(s.slotReplaySnapshot[key]); err != nil {
			return err
		}
	}
	return nil
}

func TestSlotAccountReplayRequiresIndependentCompleteState(t *testing.T) {
	first, second := slotClaim("first"), slotClaim("second")
	independent := slotSnapshot(t, first, second)
	state, err := CaptureAccountReplayState(t.Context(), independent, "account")
	require.NoError(t, err)
	for _, mode := range []string{"counter", "history", "orphan-claim", "marker", "no-enumeration", "canceled"} {
		t.Run(mode, func(t *testing.T) {
			before := slotReplaySnapshot{}
			switch mode {
			case "counter":
				before[countKey("account")] = independent[countKey("account")]
			case "history":
				before[historyKey("account")] = independent[historyKey("account")]
			case "orphan-claim":
				before[claimKey("account", "second")] = independent[claimKey("account", "second")]
			case "marker":
				key := replayAccountKey("account")
				before[key] = storage.TransferRecord{Key: key, Value: []byte("wrong-owner")}
			}
			var source RecoveryReader = before
			if mode == "no-enumeration" {
				source = slotReadOnly{before}
			}
			ctx := t.Context()
			if mode == "canceled" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			_, err := PrepareAccountReplay(ctx, source, state, []Claim{first})
			require.Error(t, err)
		})
	}
	for _, mode := range []string{"missing-history", "wrong-total", "expiring-claim", "bad-order", "no-census", "incomplete-claims"} {
		t.Run("capture-"+mode, func(t *testing.T) {
			view := maps.Clone(independent)
			switch mode {
			case "missing-history":
				delete(view, historyKey("account"))
			case "wrong-total":
				r := view[countKey("account")]
				r.Value = []byte(`{"version":3,"total":0}`)
				view[r.Key] = r
			case "expiring-claim":
				r := view[claimKey("account", "first")]
				r.ExpiresAtMillis = 1
				view[r.Key] = r
			case "incomplete-claims":
				delete(view, claimKey("account", "second"))
			}
			var source RecoveryReader = view
			if mode == "bad-order" {
				source = slotReverseOrder{view}
			}
			if mode == "no-census" {
				source = slotReadOnly{view}
			}
			_, err := CaptureAccountReplayState(t.Context(), source, "account")
			require.Error(t, err)
		})
	}
}

func TestSlotAccountReplayKeepsStagedClaimsImmutable(t *testing.T) {
	claim := slotClaim("first")
	state, err := CaptureAccountReplayState(t.Context(), slotSnapshot(t, claim), "account")
	require.NoError(t, err)
	empty := slotReplaySnapshot{}
	changes, err := PrepareAccountReplay(t.Context(), empty, state, []Claim{claim})
	require.NoError(t, err)
	staged := applySlotReplay(t, empty, changes)
	repeated, err := PrepareAccountReplay(t.Context(), empty, state, []Claim{claim})
	require.NoError(t, err)
	require.Equal(t, changes, repeated)
	changed := claim
	changed.Released = true
	_, err = PrepareAccountReplay(t.Context(), staged, state, []Claim{changed})
	require.ErrorIs(t, err, ErrClaimConflict)
	_, err = PrepareClaimReplay(t.Context(), staged, []Claim{claim})
	require.ErrorIs(t, err, ErrHistoryUnknown)
	for _, mode := range []string{"missing-claim", "extra-claim", "changed-identity", "missing-marker", "expiring-marker", "different-manifest", "early-history", "no-enumeration", "counter"} {
		t.Run(mode, func(t *testing.T) {
			view := maps.Clone(staged)
			expected := state
			switch mode {
			case "missing-claim":
				delete(view, claimKey(claim.Account, claim.ID))
			case "extra-claim":
				extra := slotClaim("extra")
				data, err := replayClaimBytes(extra)
				require.NoError(t, err)
				key := claimKey(extra.Account, extra.ID)
				view[key] = storage.TransferRecord{Key: key, Value: data}
			case "changed-identity":
				changed := claim
				changed.JobID = "different"
				data, err := replayClaimBytes(changed)
				require.NoError(t, err)
				key := claimKey(claim.Account, claim.ID)
				view[key] = storage.TransferRecord{Key: key, Value: data}
			case "missing-marker":
				delete(view, replayAccountKey("account"))
			case "expiring-marker":
				r := view[replayAccountKey("account")]
				r.ExpiresAtMillis = 1
				view[r.Key] = r
			case "different-manifest":
				expected.Held = 0
			case "early-history":
				view[historyKey("account")] = storage.TransferRecord{Key: historyKey("account"), Value: []byte("3")}
			case "counter":
				view[countKey("account")] = storage.TransferRecord{Key: countKey("account"), Value: []byte(`{"version":3,"total":1}`)}
			}
			var source RecoveryReader = view
			if mode == "no-enumeration" {
				source = slotReadOnly{view}
			}
			_, err := FinalizeAccountReplay(t.Context(), source, expected)
			require.Error(t, err)
		})
	}
}

func TestSlotAccountReplayRequiresExplicitZeroHistory(t *testing.T) {
	released := slotClaim("released")
	released.Released = true
	state, err := CaptureAccountReplayState(t.Context(), slotSnapshot(t, released), "account")
	require.NoError(t, err)
	require.Zero(t, state.Held)
	before := slotReplaySnapshot{}
	writes, err := PrepareAccountReplay(t.Context(), before, state, []Claim{released})
	require.NoError(t, err)
	staged := applySlotReplay(t, before, writes)
	final, err := FinalizeAccountReplay(t.Context(), staged, state)
	require.NoError(t, err)
	actual, err := CaptureAccountReplayState(t.Context(), applySlotReplay(t, staged, final), "account")
	require.NoError(t, err)
	require.Equal(t, state, actual)
	_, err = PrepareAccountReplay(t.Context(), before, AccountReplayState{Account: "account"}, []Claim{released})
	require.Error(t, err)
}

func TestSlotReplayInputBounds(t *testing.T) {
	claim := slotClaim("one")
	source := slotSnapshot(t, claim)
	state, err := CaptureAccountReplayState(t.Context(), source, "account")
	require.NoError(t, err)
	for _, claims := range [][]Claim{nil, {claim, claim}, make([]Claim, 65)} {
		_, err := PrepareAccountReplay(t.Context(), slotReplaySnapshot{}, state, claims)
		require.Error(t, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = CaptureAccountReplayState(ctx, source, "account")
	require.ErrorIs(t, err, context.Canceled)
	_, err = PrepareClaimReplay(nil, source, []Claim{claim})
	require.Error(t, err)
	_, err = PrepareClaimReplay(t.Context(), nil, []Claim{claim})
	require.Error(t, err)
	changed := claim
	changed.CreatedAt = time.Time{}
	_, err = PrepareAccountReplay(t.Context(), slotReplaySnapshot{}, state, []Claim{changed})
	require.Error(t, err)
}

func TestSlotAccountReplayEmptyIndependentHistory(t *testing.T) {
	for _, backend := range []string{"badger", "valkey"} {
		t.Run(backend, func(t *testing.T) {
			independent := slotReplaySnapshot{
				countKey("account"):   {Key: countKey("account"), Value: []byte(`{"version":3,"total":0}`)},
				historyKey("account"): {Key: historyKey("account"), Value: []byte("3")},
			}
			state, err := CaptureAccountReplayState(t.Context(), independent, "account")
			require.NoError(t, err)
			require.Zero(t, state.Claims)
			require.Zero(t, state.Held)
			empty := slotReplaySnapshot{}
			_, err = CaptureAccountReplayState(t.Context(), empty, "account")
			require.ErrorIs(t, err, ErrHistoryUnknown)
			store, importer, claim := nativeSlotReplay(t, backend, empty)
			stage, err := PrepareAccountReplay(t.Context(), empty, state, nil)
			require.NoError(t, err)
			first, err := importer.ReconcileImport(t.Context(), claim, 1, "", strings.Repeat("c", 64), stage)
			require.NoError(t, err)
			staged := applySlotReplay(t, empty, stage)
			final, err := FinalizeAccountReplay(t.Context(), staged, state)
			require.NoError(t, err)
			_, err = importer.ReconcileImport(t.Context(), claim, 2, first, strings.Repeat("d", 64), final)
			require.NoError(t, err)
			retry, err := importer.ReconcileImport(t.Context(), claim, 1, "", strings.Repeat("c", 64), stage)
			require.NoError(t, err)
			require.Equal(t, first, retry)
			meter, err := Open(store)
			require.NoError(t, err)
			total, err := meter.Total(t.Context(), "account")
			require.NoError(t, err)
			require.Zero(t, total)
			require.ErrorIs(t, storage.CheckImportBarrier(t.Context(), store), storage.ErrImportRestricted)
			malformed := state
			malformed.SHA256 = strings.Repeat("0", 64)
			_, err = PrepareAccountReplay(t.Context(), empty, malformed, nil)
			require.Error(t, err)
		})
	}
}
