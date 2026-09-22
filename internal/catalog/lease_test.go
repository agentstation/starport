package catalog

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	starmaperrors "github.com/agentstation/starmap/pkg/errors"

	"github.com/agentstation/starport/internal/storage"
)

// TestLeaseFencesOneHolderPerDeployment proves the lease is the fence the
// accepted head reads: one live holder at a time, a rising epoch on every
// fresh acquisition, and a takeover only after the held lease expires.
func TestLeaseFencesOneHolderPerDeployment(t *testing.T) {
	now := time.Date(2026, 9, 1, 14, 0, 0, 0, time.UTC)
	leases, err := NewLeaseStore(storage.NewMockStore())
	require.NoError(t, err)
	// The test drives expiry without a real wait.
	leases.now = func() time.Time { return now }

	epoch, err := leases.CurrentEpoch(t.Context())
	require.NoError(t, err)
	assert.Equal(
		t, uint64(0), epoch,
		"a deployment with no lease fences nothing",
	)

	held, err := leases.AcquireLease(t.Context(), "instance-1", time.Minute)
	require.NoError(t, err)
	assert.Equal(t, "instance-1", held.Holder)
	assert.Equal(t, uint64(1), held.Epoch)

	// A second instance meets the live lease and reads a non-owner state.
	_, err = leases.AcquireLease(t.Context(), "instance-2", time.Minute)
	var conflict *starmaperrors.ConflictError
	require.ErrorAs(t, err, &conflict)
	assert.Equal(t, leaseResource, conflict.Resource)
	assert.Equal(t, "instance-1", conflict.Actual)

	// The holder renews and keeps its epoch, so a candidate that started
	// under it still passes the fence.
	renewed, err := leases.Renew(t.Context(), held, time.Minute)
	require.NoError(t, err)
	assert.Equal(t, held.Epoch, renewed.Epoch)

	// After the lease expires the second instance takes it, and the epoch
	// rises, so every candidate of the previous holder is stale.
	now = now.Add(2 * time.Minute)
	taken, err := leases.AcquireLease(t.Context(), "instance-2", time.Minute)
	require.NoError(t, err)
	assert.Equal(t, "instance-2", taken.Holder)
	assert.Equal(t, uint64(2), taken.Epoch)

	// The previous holder can no longer renew what it lost.
	_, err = leases.Renew(t.Context(), held, time.Minute)
	require.ErrorAs(t, err, &conflict)

	// Releasing a lease another holder took changes nothing.
	require.NoError(t, leases.Release(t.Context(), held))
	epoch, err = leases.CurrentEpoch(t.Context())
	require.NoError(t, err)
	assert.Equal(t, uint64(2), epoch)

	// The holder releases its own lease, and the epoch stays where it is.
	require.NoError(t, leases.Release(t.Context(), taken))
	epoch, err = leases.CurrentEpoch(t.Context())
	require.NoError(t, err)
	assert.Equal(t, uint64(2), epoch)
}

// TestLeaseRefusesAnIncompleteRequest proves the typed refusals the store
// gives before it touches storage.
func TestLeaseRefusesAnIncompleteRequest(t *testing.T) {
	leases, err := NewLeaseStore(storage.NewMockStore())
	require.NoError(t, err)

	tests := []struct {
		name   string
		holder string
		ttl    time.Duration
		field  string
	}{
		{name: "no holder", holder: "  ", ttl: time.Minute, field: "lease.holder"},
		{name: "no window", holder: "instance-1", field: "lease.ttl"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := leases.AcquireLease(t.Context(), test.holder, test.ttl)
			var validation *starmaperrors.ValidationError
			require.ErrorAs(t, err, &validation)
			assert.Equal(t, test.field, validation.Field)
		})
	}

	_, err = NewLeaseStore(nil)
	require.Error(t, err)
}
