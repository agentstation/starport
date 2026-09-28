package reservation

import (
	"encoding/json/v2"
	"github.com/agentstation/starport/internal/limits"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
	"slices"
	"sync"
	"testing"
	"time"
)

func TestCorrectionRejectsStalePublication(t *testing.T) {
	for _, backend := range []string{"badger", "valkey"} {
		t.Run(backend, func(t *testing.T) {
			f := openFixture(t, backend)
			attempt := attemptFixture()
			f.provision(t, &attempt)
			settleCorrectionFixture(t, f, attempt, Evidence{ID: "original", NoCharge: true})
			require.NoError(t, f.repository.FlagDispute(t.Context(), attempt.ID, "late"))
			correction := correctionFixture(t, f, attempt.ID, Evidence{ID: "reviewed", Quantities: Quantities{"output": 70}, Tokens: 80})
			key := "jobs:fixture:" + attempt.ID
			require.NoError(t, f.raw.Set(t.Context(), key, []byte("new evidence")))
			publication := []storage.CompareAndSwapMutation{{Key: key, ExpectedValue: []byte("inspected evidence"), NewValue: []byte("corrected")}}
			_, err := f.repository.CorrectWith(t.Context(), attempt.ID, correction, publication)
			require.ErrorIs(t, err, storage.ErrConflict)
			record, err := f.repository.Inspect(t.Context(), attempt.ID)
			require.NoError(t, err)
			require.Equal(t, "late", record.DisputeID, "stale publication cannot clear a restriction")
			require.True(t, record.Evidence.NoCharge)
		})
	}
}

func TestCorrectionPublicationCommitsAndRetries(t *testing.T) {
	for _, backend := range []string{"badger", "valkey"} {
		for _, mode := range []string{"success", "before", "after"} {
			t.Run(backend+"/"+mode, func(t *testing.T) {
				f := openFixture(t, backend)
				attempt := attemptFixture()
				f.provision(t, &attempt)
				settleCorrectionFixture(t, f, attempt, Evidence{ID: "original", NoCharge: true})
				require.NoError(t, f.repository.FlagDispute(t.Context(), attempt.ID, "late"))
				correction := correctionFixture(t, f, attempt.ID, Evidence{ID: "verified", Quantities: Quantities{"output": 70}, Tokens: 80})
				key := "jobs:fixture:" + attempt.ID
				require.NoError(t, f.raw.Set(t.Context(), key, []byte("inspected")))
				publication := []storage.CompareAndSwapMutation{{Key: key, ExpectedValue: []byte("inspected"), NewValue: []byte("corrected")}, {Key: key + ":audit", NewValue: []byte("operator evidence")}}
				fault := &disputeWriteFault{TimeBoundStore: f.store, fail: mode != "success", after: mode == "after"}
				repository, err := Open(fault)
				require.NoError(t, err)
				receipt, err := repository.CorrectWith(t.Context(), attempt.ID, correction, publication)
				if mode == "success" {
					require.NoError(t, err)
					require.NotEmpty(t, receipt.PublicationDigest)
				} else {
					require.ErrorIs(t, err, errDisputeWrite)
				}
				record, err := f.repository.Inspect(t.Context(), attempt.ID)
				require.NoError(t, err)
				value, err := f.raw.Get(t.Context(), key)
				require.NoError(t, err)
				if mode == "before" {
					require.Equal(t, "inspected", string(value))
					require.Equal(t, "late", record.DisputeID)
					_, err = f.raw.Get(t.Context(), key+":audit")
					require.ErrorIs(t, err, storage.ErrNotFound)
				} else {
					require.Equal(t, "corrected", string(value))
					require.Empty(t, record.DisputeID)
				}
				var group sync.WaitGroup
				results := make(chan error, 12)
				for range 12 {
					group.Go(func() {
						_, err := f.repository.CorrectWith(t.Context(), attempt.ID, correction, publication)
						results <- err
					})
				}
				group.Wait()
				close(results)
				for err := range results {
					require.NoError(t, err)
				}
				retained, err := f.repository.InspectCorrection(t.Context(), attempt.ID, correction.ID)
				require.NoError(t, err)
				reordered := slices.Clone(publication)
				slices.Reverse(reordered)
				replay, err := f.repository.CorrectWith(t.Context(), attempt.ID, correction, reordered)
				require.NoError(t, err)
				require.Equal(t, retained, replay)
				_, err = f.repository.Correct(t.Context(), attempt.ID, correction)
				require.ErrorIs(t, err, ErrIdentityConflict, "the receipt binds its publication")
				changed := slices.Clone(publication)
				changed[0].NewValue = []byte("different operator evidence")
				_, err = f.repository.CorrectWith(t.Context(), attempt.ID, correction, changed)
				require.ErrorIs(t, err, ErrIdentityConflict)
				for _, rule := range attempt.Rules {
					state, err := f.repository.Window(t.Context(), rule.Meter, f.now)
					require.NoError(t, err)
					require.Zero(t, state.ActiveDisputes)
					require.Zero(t, state.Reserved)
					expected := int64(70)
					if rule.Meter.Dimension == limits.DimensionTokens {
						expected = 80
					}
					require.Equal(t, expected, state.Consumed)
				}
			})
		}
	}
}

func TestCorrectionPublicationRacesLateEvidence(t *testing.T) {
	for _, backend := range []string{"badger", "valkey"} {
		for _, order := range []string{"correction first", "late first", "late during commit"} {
			t.Run(backend+"/"+order, func(t *testing.T) {
				f := openFixture(t, backend)
				attempt := attemptFixture()
				f.provision(t, &attempt)
				settleCorrectionFixture(t, f, attempt, Evidence{ID: "original", NoCharge: true})
				correction := correctionFixture(t, f, attempt.ID, Evidence{ID: "reviewed", Quantities: Quantities{"output": 70}, Tokens: 80})
				key := "jobs:fixture:" + attempt.ID
				require.NoError(t, f.raw.Set(t.Context(), key, []byte("inspected")))
				publication := []storage.CompareAndSwapMutation{{Key: key, ExpectedValue: []byte("inspected"), NewValue: []byte("corrected")}}
				late := func(expected string) {
					require.NoError(t, f.repository.FlagDisputeWith(t.Context(), attempt.ID, "late", storage.CompareAndSwapMutation{Key: key, ExpectedValue: []byte(expected), NewValue: []byte("late evidence")}))
				}
				switch order {
				case "correction first":
					_, err := f.repository.CorrectWith(t.Context(), attempt.ID, correction, publication)
					require.NoError(t, err)
					late("corrected")
				case "late first":
					late("inspected")
					_, err := f.repository.CorrectWith(t.Context(), attempt.ID, correction, publication)
					require.ErrorIs(t, err, storage.ErrConflict)
				default:
					repository, err := Open(&disputeWriteFault{TimeBoundStore: f.store, before: func() { late("inspected") }})
					require.NoError(t, err)
					_, err = repository.CorrectWith(t.Context(), attempt.ID, correction, publication)
					require.ErrorIs(t, err, storage.ErrConflict)
				}
				record, err := f.repository.Inspect(t.Context(), attempt.ID)
				require.NoError(t, err)
				require.Equal(t, "late", record.DisputeID)
				value, err := f.raw.Get(t.Context(), key)
				require.NoError(t, err)
				require.Equal(t, "late evidence", string(value))
				for _, rule := range attempt.Rules {
					state, err := f.repository.Window(t.Context(), rule.Meter, f.now)
					require.NoError(t, err)
					require.EqualValues(t, 1, state.ActiveDisputes)
					require.True(t, state.ReconciliationRequired)
				}
				next := attempt
				next.ID += "-next"
				_, err = f.repository.Reserve(t.Context(), next)
				require.ErrorIs(t, err, ErrUnavailable)
			})
		}
	}
}

func TestCorrectionPublicationRefusesInvalidWrites(t *testing.T) {
	f := openFixture(t, "badger")
	attempt := attemptFixture()
	f.provision(t, &attempt)
	settleCorrectionFixture(t, f, attempt, Evidence{ID: "original", NoCharge: true})
	correction := correctionFixture(t, f, attempt.ID, Evidence{ID: "verified", NoCharge: true})
	for _, publication := range [][]storage.CompareAndSwapMutation{
		nil,
		{{Key: "job", NewValue: []byte("value")}, {Key: "job", NewValue: []byte("other")}},
		{{Key: "job", ExpectedValue: []byte("value")}},
		{{Key: "job", NewValue: []byte("value"), TTL: time.Minute}},
		{{Key: storageKey("attempt", attempt.ID), NewValue: []byte("replaced")}},
		{{Key: "job", NewValue: make([]byte, maxRecordSize+1)}},
	} {
		_, err := f.repository.CorrectWith(t.Context(), attempt.ID, correction, publication)
		require.ErrorIs(t, err, ErrInvalid)
	}
	record, err := f.repository.Inspect(t.Context(), attempt.ID)
	require.NoError(t, err)
	require.Empty(t, record.CorrectionID)
}

func TestCorrectionPublicationRejectsInvalidReceipt(t *testing.T) {
	for _, mode := range []string{"older schema", "malformed digest"} {
		t.Run(mode, func(t *testing.T) {
			f := openFixture(t, "badger")
			attempt := attemptFixture()
			f.provision(t, &attempt)
			settleCorrectionFixture(t, f, attempt, Evidence{ID: "original", NoCharge: true})
			correction := correctionFixture(t, f, attempt.ID, Evidence{ID: "verified", NoCharge: true})
			publication := []storage.CompareAndSwapMutation{{Key: "jobs:fixture:audit", NewValue: []byte("retained")}}
			receipt, err := f.repository.CorrectWith(t.Context(), attempt.ID, correction, publication)
			require.NoError(t, err)
			if mode == "older schema" {
				receipt.Version = 1
			} else {
				receipt.PublicationDigest = "not-a-digest"
			}
			data, err := json.Marshal(receipt)
			require.NoError(t, err)
			require.NoError(t, f.raw.Set(t.Context(), correctionKey(attempt.ID, correction.ID), data))
			_, err = f.repository.InspectCorrection(t.Context(), attempt.ID, correction.ID)
			require.ErrorIs(t, err, ErrUnavailable)
			_, err = f.repository.CorrectWith(t.Context(), attempt.ID, correction, publication)
			require.ErrorIs(t, err, ErrUnavailable)
		})
	}
}
