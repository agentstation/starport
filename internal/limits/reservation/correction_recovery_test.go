package reservation

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/limits"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

func TestLateCorrectionChangesOnlyOriginalWindows(t *testing.T) {
	f := openFixture(t, "badger")
	clock := &selectedAuthorityTime{TimeBoundStore: f.store, now: time.Date(2026, time.December, 31, 23, 59, 0, 0, time.UTC)}
	repository, err := Open(clock)
	require.NoError(t, err)
	f.repository, f.store, f.now = repository, clock, clock.now
	attempt := attemptFixture()
	f.provision(t, &attempt)
	settleCorrectionFixture(t, f, attempt, Evidence{ID: "original", Quantities: Quantities{"output": 40}, Tokens: 50})
	require.NoError(t, repository.FlagDispute(t.Context(), attempt.ID, "late-evidence"))
	clock.now = clock.now.Add(2 * time.Hour)
	next := attempt
	next.ID += "-next"
	_, err = repository.Reserve(t.Context(), next)
	require.NoError(t, err)
	correction := correctionFixture(t, f, attempt.ID, Evidence{ID: "corrected", Quantities: Quantities{"output": 80}, Tokens: 90})
	_, err = repository.Correct(t.Context(), attempt.ID, correction)
	require.NoError(t, err)
	for _, rule := range attempt.Rules {
		original, err := repository.Window(t.Context(), rule.Meter, f.now)
		require.NoError(t, err)
		expected := int64(80)
		if rule.Meter.Dimension == limits.DimensionTokens {
			expected = 90
		}
		require.Equal(t, expected, original.Consumed)
		require.Zero(t, original.Reserved)
		require.Zero(t, original.ActiveDisputes)
		current, err := repository.Window(t.Context(), rule.Meter, clock.now)
		require.NoError(t, err)
		require.Zero(t, current.Consumed)
		require.EqualValues(t, 600, current.Reserved)
	}
}

type correctionCrashStore struct {
	storage.TimeBoundStore
	root, stage string
}

func (s *correctionCrashStore) CompareAndSwapInWindow(ctx context.Context, mutations []storage.CompareAndSwapMutation, window storage.TimeWindow) error {
	if s.stage == "after" {
		if err := s.TimeBoundStore.CompareAndSwapInWindow(ctx, mutations, window); err != nil {
			return err
		}
	}
	if err := os.WriteFile(filepath.Join(s.root, "ready"), []byte("ready"), 0600); err != nil {
		return err
	}
	select {}
}

func TestCorrectionSurvivesProcessLoss(t *testing.T) {
	const childKey = "STARPORT_CORRECTION_PROCESS_CHILD"
	const stageKey = "STARPORT_CORRECTION_PROCESS_STAGE"
	openStore := func(t *testing.T, root string) storage.TimeBoundStore {
		t.Helper()
		store, err := storage.OpenBadger(storage.BadgerConfig{Path: filepath.Join(root, "kv"), SyncWrites: true, NumVersions: 1, MemTableSize: 64 << 20})
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, store.Close()) })
		return store
	}
	type input struct {
		Attempt    Attempt
		Correction Correction
	}
	publication := []storage.CompareAndSwapMutation{{Key: "jobs:fixture:correction", ExpectedValue: []byte("intent"), NewValue: []byte("applied")}, {Key: "jobs:fixture:audit", NewValue: []byte("retained")}}
	if root := os.Getenv(childKey); root != "" {
		store := openStore(t, root)
		repository, err := Open(store)
		require.NoError(t, err)
		now, err := store.AuthorityTime(t.Context())
		require.NoError(t, err)
		f := fixture{repository: repository, store: store, now: now}
		attempt := attemptFixture()
		f.provision(t, &attempt)
		settleCorrectionFixture(t, f, attempt, Evidence{ID: "original", NoCharge: true})
		require.NoError(t, repository.FlagDispute(t.Context(), attempt.ID, "late-evidence"))
		correction := correctionFixture(t, f, attempt.ID, Evidence{ID: "verified", Quantities: Quantities{"output": 80}, Tokens: 90})
		require.NoError(t, store.(storage.KVStore).Set(t.Context(), publication[0].Key, publication[0].ExpectedValue))
		encoded, err := json.Marshal(input{Attempt: attempt, Correction: correction})
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(root, "input.json"), encoded, 0600))
		interrupted, err := Open(&correctionCrashStore{TimeBoundStore: store, root: root, stage: os.Getenv(stageKey)})
		require.NoError(t, err)
		_, err = interrupted.CorrectWith(t.Context(), attempt.ID, correction, publication)
		t.Fatalf("correction returned before process interruption: %v", err)
	}
	for _, stage := range []string{"before", "after"} {
		t.Run(stage, func(t *testing.T) {
			root := t.TempDir()
			var output bytes.Buffer
			command := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestCorrectionSurvivesProcessLoss$", "-test.timeout=25s")
			command.Env = append(os.Environ(), childKey+"="+root, stageKey+"="+stage)
			command.Stdout, command.Stderr = &output, &output
			require.NoError(t, command.Start())
			t.Cleanup(func() { _ = command.Process.Kill() })
			ready := false
			deadline := time.Now().Add(15 * time.Second)
			for time.Now().Before(deadline) {
				if _, err := os.Stat(filepath.Join(root, "ready")); err == nil {
					ready = true
					break
				}
				time.Sleep(5 * time.Millisecond)
			}
			require.NoError(t, command.Process.Kill())
			require.Error(t, command.Wait())
			require.True(t, ready, "child did not reach correction boundary: %s", output.String())
			encoded, err := os.ReadFile(filepath.Join(root, "input.json"))
			require.NoError(t, err)
			var config input
			require.NoError(t, json.Unmarshal(encoded, &config))
			store := openStore(t, root)
			repository, err := Open(store)
			require.NoError(t, err)
			record, err := repository.Inspect(t.Context(), config.Attempt.ID)
			require.NoError(t, err)
			receipt, err := repository.InspectCorrection(t.Context(), config.Attempt.ID, config.Correction.ID)
			value, readErr := store.(storage.KVStore).Get(t.Context(), publication[0].Key)
			require.NoError(t, readErr)
			if stage == "before" {
				require.Equal(t, "intent", string(value))
			} else {
				require.Equal(t, "applied", string(value))
			}
			if stage == "before" {
				require.ErrorIs(t, err, storage.ErrNotFound)
				require.Empty(t, record.CorrectionID)
				require.Equal(t, "late-evidence", record.DisputeID)
				require.Zero(t, *record.NanoUSD)
			} else {
				require.NoError(t, err)
				require.True(t, receipt.Before.Evidence.NoCharge)
				require.Equal(t, config.Correction.ID, record.CorrectionID)
				require.Empty(t, record.DisputeID)
				require.EqualValues(t, 80, *record.NanoUSD)
			}
			_, err = repository.CorrectWith(t.Context(), config.Attempt.ID, config.Correction, publication)
			require.NoError(t, err)
			for _, rule := range config.Attempt.Rules {
				window, err := repository.Window(t.Context(), rule.Meter, record.AdmittedAt)
				require.NoError(t, err)
				expected := int64(80)
				if rule.Meter.Dimension == limits.DimensionTokens {
					expected = 90
				}
				require.Equal(t, expected, window.Consumed)
				require.Zero(t, window.Reserved)
				require.Zero(t, window.ActiveDisputes)
			}
		})
	}
}
