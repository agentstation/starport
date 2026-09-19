package authorization

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestStatusReadsRetainedPermissionWithoutReload(t *testing.T) {
	now := time.Now()
	calls := 0
	cache := newTestCache(t, sourceFunc(func(_ context.Context, id Identity) (Candidate, error) {
		calls++
		return cacheCandidate(id, now), nil
	}), cacheTestLimits(), func() (time.Time, bool) { return now, true })
	bundle, err := cache.Resolve(t.Context(), Identity{Subject: "private-key-hash"})
	if err != nil {
		t.Fatal(err)
	}
	deadline := bundle.Permit().Deadline()
	status := cache.Status()
	if !status.Ready || status.ValidBundles != 1 || status.InvalidBundles != 0 || !status.LatestDeadline.Equal(deadline) || calls != 1 {
		t.Fatalf("initial status = %+v, loads = %d", status, calls)
	}
	encoded, err := json.Marshal(status)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "private-key-hash") {
		t.Fatal("status disclosed caller identity")
	}
	now = deadline
	status = cache.Status()
	if !status.Ready || status.ValidBundles != 0 || status.InvalidBundles != 1 || !status.LatestDeadline.IsZero() || calls != 1 {
		t.Fatalf("expired caller disabled unrelated admission or renewed permission: %+v, loads = %d", status, calls)
	}
	finish := cache.authorities.fences[0].BeginMutation()
	status = cache.Status()
	if status.Ready || status.Authorities[0].State != "mutation_pending" || status.Authorities[0].Recovery != "await_mutation_and_reverify" {
		t.Fatalf("mutation status = %+v", status)
	}
	finish()
	cache.authorities.fences[0].Withdraw()
	status = cache.Status()
	if status.Ready || status.Authorities[0].State != "withdrawn" || status.Authorities[0].Recovery != "reinitialize_authority_epoch" {
		t.Fatalf("withdrawal status = %+v", status)
	}
	cache.Close()
	if status := cache.Status(); status.Ready || status.Recovery != "restart_authorization" {
		t.Fatalf("closed = %+v", status)
	}
}
