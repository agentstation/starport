package catalog

import (
	"context"
	"encoding/json/v2"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agentstation/starmap/runtime"
	"github.com/agentstation/starport/internal/recovery"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type adoptionSelectionHook struct {
	storage.IncarnationProvider
	before func(context.Context) error
}

type adoptionSelectionStore struct {
	storage.IncarnationStore
	before func(context.Context) error
}

func (p adoptionSelectionHook) BindIncarnation(ctx context.Context, identity string) (storage.IncarnationStore, error) {
	store, err := p.IncarnationProvider.BindIncarnation(ctx, identity)
	if err != nil {
		return nil, err
	}
	return adoptionSelectionStore{IncarnationStore: store, before: p.before}, nil
}

func (s adoptionSelectionStore) CompareAndSwap(ctx context.Context, mutations []storage.CompareAndSwapMutation, live ...string) error {
	for _, mutation := range mutations {
		if strings.Contains(mutation.Key, ":adoption-operation:") {
			if err := s.before(ctx); err != nil {
				return err
			}
			break
		}
	}
	return s.IncarnationStore.CompareAndSwap(ctx, mutations, live...)
}

func TestFleetAdoptionRepeatedRecoveryAndConcurrentSelection(t *testing.T) {
	originalURL, replacementURL := os.Getenv("TEST_VALKEY_URL"), os.Getenv("TEST_VALKEY_REPLACEMENT_URL")
	if originalURL == "" || replacementURL == "" {
		t.Skip("UNVERIFIED: two native Valkey processes are required")
	}
	originalAddress, err := url.Parse(originalURL)
	require.NoError(t, err)
	replacementAddress, err := url.Parse(replacementURL)
	require.NoError(t, err)
	proxy, address := newFleetBackendProxy(t, originalURL)
	t.Setenv("TEST_VALKEY_URL", address)
	fleet, kv, witness := fleetTestStore(t)
	ctx := t.Context()
	provider := kv.(storage.IncarnationProvider)
	t.Cleanup(func() { require.NoError(t, kv.Delete(context.Background(), "recovery:authority:v1")) })
	settings := identityTestSettings(filepath.Join(t.TempDir(), "state"), "", "127.0.0.1:0")
	settings.DeploymentID = fleet.identity.DeploymentID
	connected, err := openRuntime(ctx, kv, settings, runtimeCollectors{fleet: fleet})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, connected.Close(context.Background())) })
	_, err = connected.runtime.RefreshSource(ctx)
	require.NoError(t, err)
	candidate, err := connected.CurrentCandidate(ctx)
	require.NoError(t, err)
	require.NoError(t, connected.Accept(ctx, candidate))
	require.NoError(t, connected.Close(ctx))
	original, err := fleet.AcceptedPublication(ctx)
	require.NoError(t, err)
	options, err := settings.starmapOptions()
	require.NoError(t, err)
	closed, err := witness.Close(ctx, fleet.approval)
	require.NoError(t, err)
	request := FleetAdoptionRequest{SourceApproval: fleet.approval, Closed: closed, Head: original.Head,
		BackendID: fleet.identity.BackendID, OperationID: "first-recovery", Evidence: "fenced-complete-snapshot"}

	t.Run("backend-changes-during-selection", func(t *testing.T) {
		changed := adoptionSelectionHook{IncarnationProvider: provider, before: func(ctx context.Context) error {
			proxy.selectBackend(replacementAddress.Host)
			// Read-only probes drain connections closed by the transport switch.
			// The selection mutation itself is never retried.
			require.EventuallyWithT(t, func(c *assert.CollectT) {
				observed, err := provider.ObserveIncarnation(ctx)
				assert.NoError(c, err)
				assert.NotEmpty(c, observed)
				assert.NotEqual(c, request.BackendID, observed)
			}, 5*time.Second, 10*time.Millisecond)
			return nil
		}}
		_, err := adoptFleet(ctx, changed, witness, request, options)
		// Restore transport before cleanup, including on an assertion failure.
		proxy.selectBackend(originalAddress.Host)
		require.EventuallyWithT(t, func(c *assert.CollectT) {
			observed, err := provider.ObserveIncarnation(ctx)
			assert.NoError(c, err)
			assert.Equal(c, request.BackendID, observed)
		}, 5*time.Second, 10*time.Millisecond)
		require.ErrorIs(t, err, storage.ErrIncarnationChanged)
		current, err := witness.Current(ctx, fleet.identity.DeploymentID)
		require.NoError(t, err)
		require.Equal(t, closed, current)
		data, _, err := fleet.store.ReadWithLifetime(ctx, fleet.prefix+"head", 4096)
		require.NoError(t, err)
		var head runtime.FleetHead
		require.NoError(t, json.Unmarshal(data, &head))
		require.Equal(t, original.Head, head)
		// The failed owner's release reached the replacement and could not clear its old lease.
		// Expire only that fixture lease before the next explicit recovery attempt.
		require.NoError(t, kv.ExpireAt(ctx, fleet.prefix+"maintenance", time.Now().Add(-time.Second)))
	})
	approved, err := adoptFleet(ctx, provider, witness, request, options)
	require.NoError(t, err)
	first, err := NewFleetStore(ctx, provider, witness, fleet.identity.DeploymentID)
	require.NoError(t, err)
	adopted, err := first.AcceptedPublication(ctx)
	require.NoError(t, err)
	require.Equal(t, original.Publication, adopted.Publication)
	closed, err = witness.Close(ctx, approved)
	require.NoError(t, err)
	second := FleetAdoptionRequest{SourceApproval: approved, Closed: closed, Head: adopted.Head,
		BackendID: request.BackendID, OperationID: "second-recovery", Evidence: "second-fenced-complete-snapshot"}

	t.Run("one-competing-recovery-wins", func(t *testing.T) {
		reached, release := make(chan struct{}), make(chan struct{})
		paused := adoptionSelectionHook{IncarnationProvider: provider, before: func(ctx context.Context) error {
			close(reached)
			select {
			case <-release:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		}}
		workCtx, cancel := context.WithCancel(ctx)
		defer cancel()
		type result struct {
			approval recovery.Record
			err      error
		}
		finished := make(chan result, 1)
		go func() {
			record, err := adoptFleet(workCtx, paused, witness, second, options)
			finished <- result{record, err}
		}()
		joined := false
		defer func() {
			cancel()
			if !joined {
				<-finished
			}
		}()
		select {
		case <-reached:
		case got := <-finished:
			joined = true
			t.Fatalf("selection failed before barrier: %v", got.err)
		case <-time.After(2 * time.Minute):
			t.Fatal("recovery did not reach selection")
		}
		competitor := second
		competitor.OperationID, competitor.Evidence = "competing-recovery", "different-fenced-snapshot"
		_, err := adoptFleet(ctx, provider, witness, competitor, options)
		require.ErrorContains(t, err, "maintenance is busy")
		current, err := witness.Current(ctx, fleet.identity.DeploymentID)
		require.NoError(t, err)
		require.Equal(t, closed, current)
		close(release)
		got := <-finished
		joined = true
		require.NoError(t, got.err)
		require.Equal(t, second.approval(), got.approval)
		_, err = adoptFleet(ctx, provider, witness, competitor, options)
		require.ErrorIs(t, err, recovery.ErrConflict)
	})
	restored, err := NewFleetStore(ctx, provider, witness, fleet.identity.DeploymentID)
	require.NoError(t, err)
	final, err := restored.AcceptedPublication(ctx)
	require.NoError(t, err)
	require.Equal(t, original.Publication, final.Publication)
	require.Equal(t, adopted.Head, final.Adoption.Previous)
	require.Equal(t, uint64(closed.Epoch), final.Head.Identity.RecoveryEpoch)
	_, err = first.CurrentHead(ctx)
	require.Error(t, err, "the first recovered owner must remain fenced by the second recovery")
	_, err = witness.OpenAuthority(ctx, provider, fleet.identity.DeploymentID)
	require.NoError(t, err)
}
