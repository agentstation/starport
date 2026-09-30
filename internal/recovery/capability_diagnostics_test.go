package recovery

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"reflect"
	"runtime"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRecoveryCapabilityDiagnosticsRedactCopiedValues(t *testing.T) {
	const marker = "SYNTHETIC-PRIVATE-RECOVERY-MARKER"
	cases := []struct {
		name       string
		pointer    any
		nilPointer any
		want       string
	}{
		{"journal", &ActivationJournal{activationJournalState: &activationJournalState{path: marker, body: []byte(marker), digest: marker}}, (*ActivationJournal)(nil), "<private activation journal>"},
		{"lane", &CatalogPreparationLane{catalogPreparationState: &catalogPreparationState{runner: &historyRunner{runBytes: []byte(marker)}, state: catalogJournalState{previous: marker}}}, (*CatalogPreparationLane)(nil), "<private catalog preparation lane>"},
		{"prefix", &HistoryPrefix{runner: &historyRunner{runBytes: []byte(marker)}}, (*HistoryPrefix)(nil), "<private recovery history prefix>"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			value := reflect.ValueOf(test.pointer).Elem().Interface()
			for _, verb := range []string{"%v", "%+v", "%#v", "%s"} {
				for _, candidate := range []any{test.pointer, value} {
					if fmt.Sprintf(verb, candidate) != test.want {
						t.Fatal("capability diagnostics disclosed private fields", verb)
					}
				}
				if got := fmt.Sprintf(verb, test.nilPointer); got != "<nil>" && got != test.want {
					t.Fatal("nil capability diagnostics failed", verb)
				}
			}
		})
	}
}

func checkConcurrentCapabilityDiagnostics(pointer any, expected string) func() error {
	stop := make(chan struct{})
	ready := make(chan struct{})
	failed := make(chan error, 1)
	var workers sync.WaitGroup
	workers.Go(func() {
		close(ready)
		for {
			select {
			case <-stop:
				return
			default:
			}
			value := reflect.ValueOf(pointer).Elem().Interface()
			for _, verb := range []string{"%v", "%+v", "%#v", "%s"} {
				for _, candidate := range []any{pointer, value} {
					if fmt.Sprintf(verb, candidate) != expected {
						failed <- errors.New("concurrent diagnostics disclosed private fields")
						return
					}
				}
			}
			runtime.Gosched()
		}
	})
	<-ready
	return func() error {
		close(stop)
		workers.Wait()
		select {
		case err := <-failed:
			return err
		default:
			return nil
		}
	}
}

func TestRecoveryCapabilityDiagnosticsDuringNativeJournalTransitions(t *testing.T) {
	path := t.TempDir()
	require.NoError(t, os.Chmod(path, 0o700))
	body := []byte(`{"version":1,"original":"synthetic-private-phase-evidence"}`)
	journal, err := SealActivationJournal(t.Context(), path, body)
	require.NoError(t, err)
	copied := *journal
	require.Same(t, journal.activationJournalState, copied.activationJournalState)
	finish := checkConcurrentCapabilityDiagnostics(journal, "<private activation journal>")
	t.Cleanup(func() { require.NoError(t, finish()) })
	for index, phase := range activationPhases {
		require.NoError(t, copied.PublishPhase(t.Context(), phase))
		require.Equal(t, index+1, journal.CompletedPhases())
		require.Equal(t, journal.CompletedPhases(), copied.CompletedPhases())
	}
	reopened, err := InspectActivationJournal(t.Context(), path, journal.Digest())
	require.NoError(t, err)
	require.Equal(t, len(activationPhases), reopened.CompletedPhases())
	retained, err := reopened.Record()
	require.NoError(t, err)
	require.Equal(t, body, retained)
}

func TestRecoveryCapabilityDiagnosticsDuringNativeCatalogTransitions(t *testing.T) {
	f := newCatalogLaneFixture(t, false, "")
	copied := *f.lane
	require.Same(t, f.lane.catalogPreparationState, copied.catalogPreparationState)
	finish := checkConcurrentCapabilityDiagnostics(f.lane, "<private catalog preparation lane>")
	t.Cleanup(func() { require.NoError(t, finish()) })
	results := make(chan error, 2)
	start := make(chan struct{})
	var workers sync.WaitGroup
	for _, lane := range []*CatalogPreparationLane{f.lane, &copied} {
		workers.Go(func() {
			<-start
			results <- lane.ApplyCatalogTopology(t.Context(), f.topology, 0, catalogFixtureStage())
		})
	}
	close(start)
	workers.Wait()
	for range 2 {
		require.NoError(t, <-results)
	}
	require.EqualValues(t, 1, f.lane.state.positions.KV.Sequence)
	require.Equal(t, 1, f.lane.state.count)
	require.Equal(t, f.lane.state.count, copied.state.count)
	require.NoError(t, f.lane.ApplyCatalogTopology(t.Context(), f.topology, 1, catalogFixtureSelection()))
	require.Equal(t, 2, copied.state.count)
	done, err := copied.CompletedCatalogTopology(t.Context(), f.topology, 0)
	require.NoError(t, err)
	require.True(t, done)
	complete, err := f.history.witness.ReplayImportedHistory(t.Context(), f.history.source, f.history.accepted, f.history.targets, f.history.request)
	require.NoError(t, err)
	require.True(t, complete.Report().KVRotated)
	require.True(t, complete.Report().SQLRotated)
}

func TestRecoveryCapabilityDiagnosticsEmptyStateRemainsClosed(t *testing.T) {
	for _, journal := range []*ActivationJournal{nil, {}} {
		_, err := journal.Record()
		require.ErrorIs(t, err, ErrConflict)
		require.Empty(t, journal.Digest())
		require.Zero(t, journal.CompletedPhases())
		require.Empty(t, journal.CompletionPhase())
		require.ErrorIs(t, journal.PublishPhase(t.Context(), ActivationBlobs), ErrConflict)
		require.ErrorIs(t, journal.CompletePhase(t.Context(), ActivationBlobs, func(context.Context) error { t.Fatal("empty journal ran host check"); return nil }), ErrConflict)
	}
	for _, lane := range []*CatalogPreparationLane{nil, {}} {
		_, err := lane.ReadCatalogTopology(t.Context(), "catalog:test:selection", 16)
		require.ErrorIs(t, err, ErrConflict)
		done, err := lane.CompletedCatalogTopology(t.Context(), "synthetic-topology", 0)
		require.ErrorIs(t, err, ErrConflict)
		require.False(t, done)
		require.ErrorIs(t, lane.ApplyCatalogTopology(t.Context(), "synthetic-topology", 0, catalogFixtureStage()), ErrConflict)
		require.ErrorIs(t, lane.ApplyExpiringCatalogTopology(t.Context(), "synthetic-topology", 0, nil), ErrConflict)
		require.ErrorIs(t, lane.guard(t.Context(), func(context.Context, *sql.Conn) error { t.Fatal("empty lane ran native guard"); return nil }), ErrConflict)
	}
}
