package reservation

import (
	"context"
	"crypto/rand"
	"math"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/limits"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

type fixture struct {
	repository *Repository
	store      storage.TimeBoundStore
	raw        storage.KVStore
	now        time.Time
	deployment string
}

func openFixture(t *testing.T, backend string) fixture {
	t.Helper()
	deployment := "budget-" + rand.Text()
	var raw storage.KVStore
	var authority storage.TimeBoundStore
	if backend == "badger" {
		store, err := storage.OpenBadger(storage.BadgerConfig{Path: t.TempDir(), SyncWrites: true, NumVersions: 1, MemTableSize: 64 << 20})
		require.NoError(t, err)
		raw, authority = store, store
	} else {
		address := os.Getenv("TEST_VALKEY_URL")
		if address == "" {
			t.Skip("UNVERIFIED: TEST_VALKEY_URL is not set")
		}
		store, err := storage.OpenValkey(storage.ValkeyConfig{URL: address, DeploymentID: deployment, AllowInsecure: true})
		require.NoError(t, err)
		provider := store.(storage.IncarnationProvider)
		identity, err := provider.ObserveIncarnation(t.Context())
		require.NoError(t, err)
		bound, err := provider.BindIncarnation(t.Context(), identity)
		require.NoError(t, err)
		raw, authority = store, bound.(storage.TimeBoundStore)
	}
	t.Cleanup(func() { require.NoError(t, raw.Close()) })
	repository, err := Open(authority)
	require.NoError(t, err)
	now, err := authority.AuthorityTime(t.Context())
	require.NoError(t, err)
	return fixture{repository: repository, store: authority, raw: raw, now: now, deployment: deployment}
}

func attemptFixture() Attempt {
	identity := rand.Text()
	return Attempt{
		ID: rand.Text(), RequestID: rand.Text(), AccountID: "account-" + identity, KeyID: "key-" + identity, TeamID: "team-" + identity,
		OfferingID: "fixture/model", CatalogGeneration: "generation-a", Operation: "chat",
		Valuation: Valuation{Version: ArithmeticVersion, Components: []Component{{Unit: "output", Price: Price{USD: "0.000000001", PerUnits: 1}}}},
		Bound:     Quantities{"output": 600}, TokenBound: 600,
	}
}

func (f fixture) provision(t *testing.T, attempt *Attempt) {
	t.Helper()
	for _, holder := range []struct {
		scope limits.Scope
		id    string
	}{{limits.ScopeAccount, attempt.AccountID}, {limits.ScopeKey, attempt.KeyID}, {limits.ScopeTeam, attempt.TeamID}} {
		for _, dimension := range []limits.Dimension{limits.DimensionSpend, limits.DimensionTokens} {
			meter := Meter{Scope: holder.scope, Holder: holder.id, Dimension: dimension, Interval: limits.IntervalDay}
			attempt.Rules = append(attempt.Rules, Rule{Meter: meter, Limit: 1000, PolicyRevision: "revision-1"})
			require.NoError(t, f.repository.EstablishWindow(t.Context(), meter, f.now, 0, "empty-fixture-history"))
		}
	}
}

func TestReservationContract(t *testing.T) {
	for _, backend := range []string{"badger", "valkey"} {
		t.Run(backend, func(t *testing.T) {
			f := openFixture(t, backend)
			t.Run("concurrent admission reserves all populations once", func(t *testing.T) {
				base := attemptFixture()
				f.provision(t, &base)
				var accepted atomic.Int64
				var group sync.WaitGroup
				outcomes := make(chan error, 16)
				for range 16 {
					group.Go(func() {
						attempt := base
						attempt.ID = rand.Text()
						_, err := f.repository.Reserve(t.Context(), attempt)
						if err == nil {
							accepted.Add(1)
						}
						outcomes <- err
					})
				}
				group.Wait()
				close(outcomes)
				for err := range outcomes {
					if err != nil {
						require.ErrorIs(t, err, ErrExhausted)
					}
				}
				require.EqualValues(t, 1, accepted.Load())
				for _, rule := range base.Rules {
					state, err := f.repository.Window(t.Context(), rule.Meter, f.now)
					require.NoError(t, err)
					require.EqualValues(t, 600, state.Reserved)
					require.Zero(t, state.Consumed)
				}
			})
			t.Run("idempotency dispatch uncertainty and settlement", func(t *testing.T) {
				attempt := attemptFixture()
				f.provision(t, &attempt)
				first, err := f.repository.Reserve(t.Context(), attempt)
				require.NoError(t, err)
				second, err := f.repository.Reserve(t.Context(), attempt)
				require.NoError(t, err)
				require.Equal(t, first, second)
				changed := attempt
				changed.CatalogGeneration = "another-generation"
				_, err = f.repository.Reserve(t.Context(), changed)
				require.ErrorIs(t, err, ErrIdentityConflict)
				require.NoError(t, f.repository.Begin(t.Context(), attempt.ID))
				require.ErrorIs(t, f.repository.Begin(t.Context(), attempt.ID), ErrAlreadyDispatched)
				require.ErrorIs(t, f.repository.CancelBeforeDispatch(t.Context(), attempt.ID), ErrTransition)
				require.NoError(t, f.repository.MarkUncertain(t.Context(), attempt.ID, "provider_timeout"))
				state, err := f.repository.Window(t.Context(), attempt.Rules[0].Meter, f.now)
				require.NoError(t, err)
				require.EqualValues(t, 600, state.Reserved)
				evidence := Evidence{ID: "provider-usage", Quantities: Quantities{"output": 120}, Tokens: 140}
				require.NoError(t, f.repository.Reconcile(t.Context(), attempt.ID, evidence))
				require.NoError(t, f.repository.Reconcile(t.Context(), attempt.ID, evidence))
				for _, rule := range attempt.Rules {
					state, err := f.repository.Window(t.Context(), rule.Meter, f.now)
					require.NoError(t, err)
					require.Zero(t, state.Reserved)
					expected := int64(120)
					if rule.Meter.Dimension == limits.DimensionTokens {
						expected = 140
					}
					require.Equal(t, expected, state.Consumed)
				}
				evidence.Tokens++
				require.ErrorIs(t, f.repository.Reconcile(t.Context(), attempt.ID, evidence), ErrIdentityConflict)
			})
			t.Run("cancellation wins only before dispatch", func(t *testing.T) {
				attempt := attemptFixture()
				f.provision(t, &attempt)
				_, err := f.repository.Reserve(t.Context(), attempt)
				require.NoError(t, err)
				var startErr, cancelErr error
				var group sync.WaitGroup
				group.Go(func() { startErr = f.repository.Begin(t.Context(), attempt.ID) })
				group.Go(func() { cancelErr = f.repository.CancelBeforeDispatch(t.Context(), attempt.ID) })
				group.Wait()
				require.True(t, (startErr == nil) != (cancelErr == nil))
				state, err := f.repository.Window(t.Context(), attempt.Rules[0].Meter, f.now)
				require.NoError(t, err)
				if startErr == nil {
					require.EqualValues(t, 600, state.Reserved)
				} else {
					require.Zero(t, state.Reserved)
				}
			})
			t.Run("unknown history and exhausted team change no other meter", func(t *testing.T) {
				attempt := attemptFixture()
				f.provision(t, &attempt)
				last := &attempt.Rules[len(attempt.Rules)-1]
				last.Limit = 100
				_, err := f.repository.Reserve(t.Context(), attempt)
				require.ErrorIs(t, err, ErrExhausted)
				last.Limit = 1000
				last.Meter.Interval = limits.IntervalMonth
				_, err = f.repository.Reserve(t.Context(), attempt)
				require.ErrorIs(t, err, ErrHistoryUnknown)
				state, err := f.repository.Window(t.Context(), attempt.Rules[0].Meter, f.now)
				require.NoError(t, err)
				require.Zero(t, state.Reserved)
				_, err = f.repository.Inspect(t.Context(), attempt.ID)
				require.ErrorIs(t, err, storage.ErrNotFound)
			})
			t.Run("policy revisions and provider overruns preserve spending", func(t *testing.T) {
				attempt := attemptFixture()
				f.provision(t, &attempt)
				_, err := f.repository.Reserve(t.Context(), attempt)
				require.NoError(t, err)
				require.NoError(t, f.repository.Begin(t.Context(), attempt.ID))
				require.NoError(t, f.repository.Reconcile(t.Context(), attempt.ID, Evidence{ID: "overrun", Quantities: Quantities{"output": 1200}, Tokens: 1200}))
				for i := range attempt.Rules {
					attempt.Rules[i].PolicyRevision = "revision-2"
				}
				attempt.ID = rand.Text()
				_, err = f.repository.Reserve(t.Context(), attempt)
				require.ErrorIs(t, err, ErrExhausted)
				rule := attempt.Rules[0]
				require.NoError(t, f.repository.EstablishWindow(t.Context(), rule.Meter, f.now, 0, "empty-fixture-history"))
				state, err := f.repository.Window(t.Context(), rule.Meter, f.now)
				require.NoError(t, err)
				require.EqualValues(t, 1200, state.Consumed, "initialization retry cannot reset spending")
				require.ErrorIs(t, f.repository.EstablishWindow(t.Context(), rule.Meter, f.now, 0, "different-history"), ErrIdentityConflict)
			})
			t.Run("lost dispatch and settlement acknowledgements cannot repeat spending", func(t *testing.T) {
				attempt := attemptFixture()
				f.provision(t, &attempt)
				_, err := f.repository.Reserve(t.Context(), attempt)
				require.NoError(t, err)
				fault := &lostAcknowledgement{TimeBoundStore: f.store}
				repository, err := Open(fault)
				require.NoError(t, err)
				fault.lose.Store(true)
				require.ErrorIs(t, repository.Begin(t.Context(), attempt.ID), context.DeadlineExceeded)
				require.ErrorIs(t, repository.Begin(t.Context(), attempt.ID), ErrAlreadyDispatched)
				evidence := Evidence{ID: "recovered-provider-record", Quantities: Quantities{"output": 25}, Tokens: 30}
				fault.lose.Store(true)
				require.ErrorIs(t, repository.Reconcile(t.Context(), attempt.ID, evidence), context.DeadlineExceeded)
				require.NoError(t, repository.Reconcile(t.Context(), attempt.ID, evidence))
				state, err := repository.Window(t.Context(), attempt.Rules[0].Meter, f.now)
				require.NoError(t, err)
				require.EqualValues(t, 25, state.Consumed)
				require.Zero(t, state.Reserved)
			})
			t.Run("unrepresentable charges retain evidence and block every meter", func(t *testing.T) {
				attempt := attemptFixture()
				attempt.Valuation.Components[0].Price.USD = "1"
				attempt.Bound["output"] = 0
				f.provision(t, &attempt)
				_, err := f.repository.Reserve(t.Context(), attempt)
				require.NoError(t, err)
				require.NoError(t, f.repository.Begin(t.Context(), attempt.ID))
				evidence := Evidence{ID: "provider-overflow", Quantities: Quantities{"output": math.MaxInt64}, Tokens: 600}
				require.ErrorIs(t, f.repository.Reconcile(t.Context(), attempt.ID, evidence), ErrOverflow)
				record, err := f.repository.Inspect(t.Context(), attempt.ID)
				require.NoError(t, err)
				require.Equal(t, Uncertain, record.State)
				require.Equal(t, evidence, *record.Unresolved)
				for _, rule := range attempt.Rules {
					state, err := f.repository.Window(t.Context(), rule.Meter, f.now)
					require.NoError(t, err)
					require.True(t, state.Overflow)
				}
				attempt.ID = rand.Text()
				_, err = f.repository.Reserve(t.Context(), attempt)
				require.ErrorIs(t, err, ErrExhausted)
			})
			t.Run("expiring and corrupt records cannot grant capacity", func(t *testing.T) {
				attempt := attemptFixture()
				f.provision(t, &attempt)
				key := meterKey(attempt.Rules[0].Meter, windowFor(limits.IntervalDay, f.now))
				bytes, err := f.raw.Get(t.Context(), key)
				require.NoError(t, err)
				require.NoError(t, f.raw.SetWithTTL(t.Context(), key, bytes, time.Minute))
				_, err = f.repository.Reserve(t.Context(), attempt)
				require.ErrorIs(t, err, ErrUnavailable)
				require.NoError(t, f.raw.Delete(t.Context(), key))
				require.NoError(t, f.raw.Set(t.Context(), key, []byte(`{"version":1}`)))
				_, err = f.repository.Reserve(t.Context(), attempt)
				require.ErrorIs(t, err, ErrUnavailable)
			})
		})
	}
}

type lostAcknowledgement struct {
	storage.TimeBoundStore
	lose atomic.Bool
}

func (s *lostAcknowledgement) CompareAndSwapInWindow(ctx context.Context, mutations []storage.CompareAndSwapMutation, window storage.TimeWindow) error {
	err := s.TimeBoundStore.CompareAndSwapInWindow(ctx, mutations, window)
	if err == nil && s.lose.Swap(false) {
		return context.DeadlineExceeded
	}
	return err
}

func TestFixedUTCWindows(t *testing.T) {
	at := time.Date(2027, time.January, 1, 23, 59, 59, 0, time.FixedZone("east", 2*3600))
	require.Equal(t, time.Date(2026, time.December, 28, 0, 0, 0, 0, time.UTC), windowFor(limits.IntervalWeek, at).Start)
	require.Equal(t, time.Date(2027, time.January, 2, 0, 0, 0, 0, time.UTC), windowFor(limits.IntervalDay, at).End)
	require.Equal(t, time.Date(2027, time.February, 1, 0, 0, 0, 0, time.UTC), windowFor(limits.IntervalMonth, at).End)
}
