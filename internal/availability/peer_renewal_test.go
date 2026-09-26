package availability

import (
	"github.com/agentstation/starport/internal/failure"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestPeerPublicationRenewsUnchangedHealthHint(t *testing.T) {
	clock := &fakeClock{now: time.Unix(100, 0)}
	store := newFakeKVStore()
	origin := sharedTracker(t, clock, store, "origin")
	receiver := sharedTracker(t, clock, store, "receiver")
	route := sharedTestRoute()
	origin.RecordFailure(route, failure.New(failure.NotFound, "missing", false, failure.ProviderDetails{StateScope: failure.ScopeOffering}, nil), time.Second)
	origin.exchangeShared(t.Context())
	receiver.exchangeShared(t.Context())
	require.False(t, receiver.Acquire(route))
	clock.Advance(45 * time.Second)
	origin.exchangeShared(t.Context())
	receiver.exchangeShared(t.Context())
	clock.Advance(20 * time.Second)
	receiver.Refresh(t.Context())
	require.False(t, receiver.Acquire(route), "the latest source publication remains inside its validity interval")
	clock.Advance(41 * time.Second)
	receiver.Refresh(t.Context())
	require.True(t, receiver.Acquire(route), "a stopped source must still expire")
}
