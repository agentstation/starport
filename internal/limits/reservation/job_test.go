package reservation

import (
	"encoding/json/v2"
	"errors"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestJobBindingIsExclusiveAndSurvivesSettlement(t *testing.T) {
	for _, backend := range []string{"badger", "valkey"} {
		t.Run(backend, func(t *testing.T) {
			f := openFixture(t, backend)
			attempt := attemptFixture()
			f.provision(t, &attempt)
			_, err := f.repository.Reserve(t.Context(), attempt)
			require.NoError(t, err)
			require.ErrorIs(t, f.repository.BindJob(t.Context(), attempt.ID, "before-begin"), ErrTransition)
			require.NoError(t, f.repository.Begin(t.Context(), attempt.ID))
			var group sync.WaitGroup
			outcomes := make(chan error, 2)
			for _, id := range []string{"first-job", "second-job"} {
				group.Go(func() { outcomes <- f.repository.BindJob(t.Context(), attempt.ID, id) })
			}
			group.Wait()
			close(outcomes)
			var accepted int
			for err := range outcomes {
				if err == nil {
					accepted++
				} else {
					require.ErrorIs(t, err, ErrIdentityConflict)
				}
			}
			require.Equal(t, 1, accepted)
			record, err := f.repository.Inspect(t.Context(), attempt.ID)
			require.NoError(t, err)
			require.NotEmpty(t, record.JobID)
			require.NoError(t, f.repository.BindJob(t.Context(), attempt.ID, record.JobID))
			for _, rule := range attempt.Rules {
				state, err := f.repository.Window(t.Context(), rule.Meter, f.now)
				require.NoError(t, err)
				require.EqualValues(t, 600, state.Reserved)
				require.Zero(t, state.Consumed)
			}
			require.NoError(t, f.repository.Reconcile(t.Context(), attempt.ID, Evidence{ID: "measured", Quantities: Quantities{"output": 200}, Tokens: 250}))
			reopened, err := Open(f.store)
			require.NoError(t, err)
			require.NoError(t, reopened.BindJob(t.Context(), attempt.ID, record.JobID))
			require.ErrorIs(t, reopened.BindJob(t.Context(), attempt.ID, "other-job"), ErrIdentityConflict)
			checkRecovered(t, f, attempt)
		})
	}
}

func TestJobBindingRecoversLostAcknowledgement(t *testing.T) {
	for _, backend := range []string{"badger", "valkey"} {
		t.Run(backend, func(t *testing.T) {
			f := openFixture(t, backend)
			attempt := attemptFixture()
			f.provision(t, &attempt)
			_, err := f.repository.Reserve(t.Context(), attempt)
			require.NoError(t, err)
			require.NoError(t, f.repository.Begin(t.Context(), attempt.ID))
			fault := &recoveryAckLoss{TimeBoundStore: f.store}
			repository, err := Open(fault)
			require.NoError(t, err)
			require.ErrorIs(t, repository.BindJob(t.Context(), attempt.ID, "job"), ErrUnavailable)
			require.NoError(t, repository.BindJob(t.Context(), attempt.ID, "job"))
			record, err := repository.Inspect(t.Context(), attempt.ID)
			require.NoError(t, err)
			require.Equal(t, "job", record.JobID)
			require.Equal(t, Dispatched, record.State)
			require.Nil(t, record.Evidence)
		})
	}
}

func TestJobBindingRefusesUncertainAndLegacyAttempts(t *testing.T) {
	f := openFixture(t, "badger")
	attempt := retainFixture(t, f)
	require.ErrorIs(t, f.repository.BindJob(t.Context(), attempt.ID, "job"), ErrTransition)
	record, err := f.repository.Inspect(t.Context(), attempt.ID)
	require.NoError(t, err)
	record.Version = 1
	data, err := json.Marshal(record)
	require.NoError(t, err)
	require.NoError(t, f.raw.Set(t.Context(), storageKey("attempt", attempt.ID), data))
	_, err = f.repository.Inspect(t.Context(), attempt.ID)
	require.True(t, errors.Is(err, ErrUnavailable), "an older writer cannot retain or enforce job ownership")
}
