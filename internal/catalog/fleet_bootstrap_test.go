package catalog

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/agentstation/starmap/runtime"
	"github.com/agentstation/starport/internal/catalog/recovery"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

var errBootstrapResponseLost = errors.New("test transport lost the write response")

type bootstrapReplyFault struct {
	*recovery.Witness
	before bool
	failed bool
}

func TestFleetBootstrapConcurrentExactPublication(t *testing.T) {
	fleet, _, witness := fleetTestStore(t)
	grant, err := fleet.AcquireLease(t.Context(), "publisher", time.Minute)
	require.NoError(t, err)
	publication := fleetTestPublication(t, grant, runtime.FleetHead{}, "first")
	type result struct {
		head runtime.FleetHead
		err  error
	}
	results := make(chan result, 2)
	var work sync.WaitGroup
	for range 2 {
		work.Go(func() {
			head, err := fleet.CommitPublication(t.Context(), publication)
			results <- result{head: head, err: err}
		})
	}
	work.Wait()
	first, second := <-results, <-results
	require.NoError(t, first.err)
	require.NoError(t, second.err)
	require.Equal(t, first.head, second.head)
	require.Equal(t, uint64(1), first.head.Revision)
	require.ErrorIs(t, witness.CheckBootstrap(t.Context(), fleet.approval), recovery.ErrBootstrapConsumed)
}

func (w *bootstrapReplyFault) ConsumeBootstrap(ctx context.Context, expected recovery.Record) error {
	if w.failed {
		return w.Witness.ConsumeBootstrap(ctx, expected)
	}
	w.failed = true
	if !w.before {
		if err := w.Witness.ConsumeBootstrap(ctx, expected); err != nil {
			return err
		}
	}
	return errBootstrapResponseLost
}

type firstHeadReplyFault struct {
	storage.IncarnationStore
	before bool
	failed bool
}

func (s *firstHeadReplyFault) CompareAndSwap(ctx context.Context, changes []storage.CompareAndSwapMutation, live ...string) error {
	for _, change := range changes {
		if !s.failed && strings.HasSuffix(change.Key, ":head") && change.NewValue != nil && change.ExpectedValue == nil {
			s.failed = true
			if !s.before {
				if err := s.IncarnationStore.CompareAndSwap(ctx, changes, live...); err != nil {
					return err
				}
			}
			return errBootstrapResponseLost
		}
	}
	return s.IncarnationStore.CompareAndSwap(ctx, changes, live...)
}

func TestFleetBootstrapWriteBoundaries(t *testing.T) {
	for _, scenario := range []string{"before-sql", "after-sql", "before-kv", "after-kv"} {
		t.Run(scenario, func(t *testing.T) {
			fleet, _, witness := fleetTestStore(t)
			ctx := t.Context()
			grant, err := fleet.AcquireLease(ctx, "publisher", time.Minute)
			require.NoError(t, err)
			publication := fleetTestPublication(t, grant, runtime.FleetHead{}, "first")
			if strings.HasSuffix(scenario, "sql") {
				fleet.witness = &bootstrapReplyFault{Witness: witness, before: scenario == "before-sql"}
			} else {
				fleet.store = &firstHeadReplyFault{IncarnationStore: fleet.store, before: scenario == "before-kv"}
			}
			_, err = fleet.CommitPublication(ctx, publication)
			require.ErrorIs(t, err, errBootstrapResponseLost)
			approval, err := witness.Approved(ctx, fleet.identity.DeploymentID)
			require.NoError(t, err)
			require.Equal(t, fleet.approval, approval, "consumption cannot change recovery identity")
			switch scenario {
			case "before-sql", "after-kv":
				head, err := fleet.CommitPublication(ctx, publication)
				require.NoError(t, err, "retry must recover the unconsumed attempt or completed exact receipt")
				require.Equal(t, uint64(1), head.Revision)
				current, err := fleet.CurrentHead(ctx)
				require.NoError(t, err)
				require.Equal(t, head, current)
				require.ErrorIs(t, witness.CheckBootstrap(ctx, approval), recovery.ErrBootstrapConsumed)
			case "after-sql", "before-kv":
				_, err := fleet.CurrentHead(ctx)
				require.ErrorIs(t, err, recovery.ErrBootstrapConsumed)
				_, err = fleet.CommitPublication(ctx, publication)
				require.ErrorIs(t, err, recovery.ErrBootstrapConsumed, "an uncertain first publication requires controlled recovery")
			}
		})
	}
}
