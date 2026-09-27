package reservation

import (
	"context"
	"crypto/rand"
	"encoding/json/v2"
	"errors"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

func TestReservationRestart(t *testing.T) {
	directory := t.TempDir()
	options := storage.BadgerConfig{Path: directory, SyncWrites: true, NumVersions: 1, MemTableSize: 64 << 20}
	store, err := storage.OpenBadger(options)
	require.NoError(t, err)
	repository, err := Open(store)
	require.NoError(t, err)
	now, err := store.AuthorityTime(t.Context())
	require.NoError(t, err)
	f := fixture{repository: repository, store: store, raw: store, now: now}
	attempt := attemptFixture()
	f.provision(t, &attempt)
	_, err = repository.Reserve(t.Context(), attempt)
	require.NoError(t, err)
	require.NoError(t, repository.Begin(t.Context(), attempt.ID))
	require.NoError(t, store.Close())
	store, err = storage.OpenBadger(options)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	repository, err = Open(store)
	require.NoError(t, err)
	record, err := repository.Inspect(t.Context(), attempt.ID)
	require.NoError(t, err)
	require.Equal(t, Dispatched, record.State)
	require.ErrorIs(t, repository.Begin(t.Context(), attempt.ID), ErrAlreadyDispatched)
	attempt.ID = rand.Text()
	_, err = repository.Reserve(t.Context(), attempt)
	require.ErrorIs(t, err, ErrExhausted, "restart cannot refund an unanswered provider attempt")
	require.NoError(t, repository.Reconcile(t.Context(), record.Attempt.ID, Evidence{ID: "provider-recovery", Quantities: Quantities{"output": 20}, Tokens: 25}))
	_, err = repository.Reserve(t.Context(), attempt)
	require.NoError(t, err)
}

func TestLateSettlementKeepsOriginalWindowAndValuation(t *testing.T) {
	f := openFixture(t, "badger")
	clock := &selectedAuthorityTime{TimeBoundStore: f.store, now: time.Date(2026, time.December, 31, 23, 59, 59, 0, time.UTC)}
	repository, err := Open(clock)
	require.NoError(t, err)
	f.repository, f.store, f.now = repository, clock, clock.now
	attempt := attemptFixture()
	f.provision(t, &attempt)
	record, err := repository.Reserve(t.Context(), attempt)
	require.NoError(t, err)
	require.NoError(t, repository.Begin(t.Context(), attempt.ID))
	clock.now = clock.now.Add(2 * time.Hour)
	// The caller changes its catalog facts after dispatch. Stored evidence owns its copy.
	attempt.Valuation.Components[0].Price.USD = "1"
	require.NoError(t, repository.Reconcile(t.Context(), attempt.ID, Evidence{ID: "late-provider-usage", Quantities: Quantities{"output": 40}, Tokens: 45}))
	state, err := repository.Window(t.Context(), attempt.Rules[0].Meter, record.AdmittedAt)
	require.NoError(t, err)
	require.EqualValues(t, 40, state.Consumed)
	require.Zero(t, state.Reserved)
	_, err = repository.Window(t.Context(), attempt.Rules[0].Meter, clock.now)
	require.ErrorIs(t, err, ErrHistoryUnknown)
}

func TestDelayedDispatchCannotUseAClosedWindow(t *testing.T) {
	f := openFixture(t, "badger")
	clock := &selectedAuthorityTime{TimeBoundStore: f.store, now: time.Date(2026, time.December, 31, 23, 59, 59, 0, time.UTC)}
	repository, err := Open(clock)
	require.NoError(t, err)
	f.repository, f.now = repository, clock.now
	attempt := attemptFixture()
	f.provision(t, &attempt)
	_, err = repository.Reserve(t.Context(), attempt)
	require.NoError(t, err)
	clock.now = clock.now.Add(time.Second)
	require.ErrorIs(t, repository.Begin(t.Context(), attempt.ID), storage.ErrTimeWindowChanged)
	require.NoError(t, repository.CancelBeforeDispatch(t.Context(), attempt.ID))
	state, err := repository.Window(t.Context(), attempt.Rules[0].Meter, f.now)
	require.NoError(t, err)
	require.Zero(t, state.Reserved)
}

// selectedAuthorityTime changes the authority clock while retaining real Badger
// transactions. Native time guards have separate storage contract tests.
type selectedAuthorityTime struct {
	storage.TimeBoundStore
	now time.Time
}

func (s *selectedAuthorityTime) AuthorityTime(context.Context) (time.Time, error) { return s.now, nil }

func (s *selectedAuthorityTime) CompareAndSwapInWindow(ctx context.Context, mutations []storage.CompareAndSwapMutation, window storage.TimeWindow) error {
	if window != (storage.TimeWindow{}) && (s.now.Before(window.Start) || !s.now.Before(window.End)) {
		return storage.ErrTimeWindowChanged
	}
	return s.TimeBoundStore.CompareAndSwapInWindow(ctx, mutations, storage.TimeWindow{})
}

func TestAggregateOverflowPreservesDebtAndBlocksAdmission(t *testing.T) {
	state := &WindowState{Consumed: math.MaxInt64 - 1}
	consume(state, 2)
	require.True(t, state.Overflow)
	require.EqualValues(t, math.MaxInt64, state.Consumed)
	consume(state, 0)
	require.True(t, state.Overflow)
}

func TestValkeyReservationAcrossProcesses(t *testing.T) {
	if input := os.Getenv("STARPORT_RESERVATION_CHILD"); input != "" {
		runReservationChild(t, input)
		return
	}
	f := openFixture(t, "valkey")
	attempt := attemptFixture()
	f.provision(t, &attempt)
	// Each process opens a distinct connection to the same deployment and binds
	// the same test-approved native identity. No process shares a Go mutex.
	input := processInput{Attempt: attempt, URL: os.Getenv("TEST_VALKEY_URL")}
	keys, err := f.raw.ScanWithPrefix(t.Context(), "budget:v1:", 100)
	require.NoError(t, err)
	input.Deployment = f.deployment
	provider, err := storage.OpenValkey(storage.ValkeyConfig{URL: input.URL, DeploymentID: input.Deployment, AllowInsecure: true})
	require.NoError(t, err)
	defer func() { require.NoError(t, provider.Close()) }()
	// Verify that the process fixture opens the exact namespace provisioned above.
	otherKeys, err := provider.ScanWithPrefix(t.Context(), "budget:v1:", 100)
	require.NoError(t, err)
	require.ElementsMatch(t, keys, otherKeys)
	input.Identity, err = provider.(storage.IncarnationProvider).ObserveIncarnation(t.Context())
	require.NoError(t, err)
	directory := t.TempDir()
	var workers sync.WaitGroup
	outputs := make(chan struct {
		log []byte
		err error
	}, 4)
	for i := range 4 {
		copy := input
		copy.Attempt.ID = rand.Text()
		copy.Result = filepath.Join(directory, string(rune('a'+i)))
		encoded, err := json.Marshal(copy)
		require.NoError(t, err)
		command := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestValkeyReservationAcrossProcesses$", "-test.timeout=30s")
		command.Env = append(os.Environ(), "STARPORT_RESERVATION_CHILD="+string(encoded))
		workers.Go(func() {
			log, err := command.CombinedOutput()
			outputs <- struct {
				log []byte
				err error
			}{log, err}
		})
	}
	workers.Wait()
	close(outputs)
	for output := range outputs {
		require.NoError(t, output.err, "%s", output.log)
	}
	accepted := 0
	for i := range 4 {
		data, err := os.ReadFile(filepath.Join(directory, string(rune('a'+i))))
		require.NoError(t, err)
		if string(data) == "accepted" {
			accepted++
		} else {
			require.Equal(t, "exhausted", string(data))
		}
	}
	require.Equal(t, 1, accepted)
	state, err := f.repository.Window(t.Context(), attempt.Rules[0].Meter, f.now)
	require.NoError(t, err)
	require.EqualValues(t, 600, state.Reserved)
}

type processInput struct {
	Attempt                           Attempt
	URL, Deployment, Identity, Result string
}

func runReservationChild(t *testing.T, input string) {
	t.Helper()
	var config processInput
	require.NoError(t, json.Unmarshal([]byte(input), &config))
	store, err := storage.OpenValkey(storage.ValkeyConfig{URL: config.URL, DeploymentID: config.Deployment, AllowInsecure: true})
	require.NoError(t, err)
	defer func() { require.NoError(t, store.Close()) }()
	bound, err := store.(storage.IncarnationProvider).BindIncarnation(t.Context(), config.Identity)
	require.NoError(t, err)
	repository, err := Open(bound.(storage.TimeBoundStore))
	require.NoError(t, err)
	_, err = repository.Reserve(t.Context(), config.Attempt)
	result := "accepted"
	if errors.Is(err, ErrExhausted) {
		result = "exhausted"
	} else {
		require.NoError(t, err)
	}
	require.NoError(t, os.WriteFile(config.Result, []byte(result), 0o600))
}
