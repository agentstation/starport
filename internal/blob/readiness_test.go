package blob

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestPublicationReadinessCoalescesAndCaches(t *testing.T) {
	var state blobReadiness
	var calls atomic.Int32
	entered, release := make(chan struct{}), make(chan struct{})
	probe := func(ctx context.Context) error { calls.Add(1); close(entered); <-release; return nil }
	var workers sync.WaitGroup
	failures := make(chan error, 32)
	for range 32 {
		workers.Go(func() { failures <- state.ensure(t.Context(), probe, ErrPublicationUnavailable) })
	}
	<-entered
	waiting, stopWaiting := context.WithTimeout(t.Context(), 10*time.Millisecond)
	defer stopWaiting()
	require.ErrorIs(t, state.ensure(waiting, probe, ErrPublicationUnavailable), context.DeadlineExceeded)
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	require.ErrorIs(t, state.ensure(canceled, probe, ErrPublicationUnavailable), context.Canceled)
	close(release)
	workers.Wait()
	close(failures)
	for err := range failures {
		require.NoError(t, err)
	}
	require.EqualValues(t, 1, calls.Load())
	require.Zero(t, testing.AllocsPerRun(100, func() {
		if err := state.ensure(t.Context(), probe, ErrPublicationUnavailable); err != nil {
			panic(err)
		}
	}))
}

func TestPublicationReadinessRecoversAfterFailure(t *testing.T) {
	var state blobReadiness
	sentinel := errors.New("offline")
	require.ErrorIs(t, state.ensure(t.Context(), func(context.Context) error { return sentinel }, ErrPublicationUnavailable), sentinel)
	require.ErrorIs(t, state.ensure(t.Context(), func(context.Context) error { t.Fatal("cooldown ignored"); return nil }, ErrPublicationUnavailable), ErrPublicationUnavailable)
	state.mu.Lock()
	state.retryAfter = time.Time{}
	state.mu.Unlock()
	require.NoError(t, state.ensure(t.Context(), func(context.Context) error { return nil }, ErrPublicationUnavailable))
}
