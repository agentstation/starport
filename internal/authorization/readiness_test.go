package authorization

import (
	"context"
	"testing"
	"time"
)

func TestReadinessDoesNotLoadOrRenewPolicy(t *testing.T) {
	now, healthy := time.Unix(1000, 0), false
	cache := newTestCache(t, sourceFunc(func(context.Context, Identity) (Candidate, error) {
		t.Error("readiness loaded caller policy")
		return Candidate{}, ErrUnavailable
	}), cacheTestLimits(), func() (time.Time, bool) { return now, healthy })
	if cache.Ready() {
		t.Fatal("unqualified clock reports ready")
	}
	healthy = true
	if !cache.Ready() {
		t.Fatal("valid prerequisites report unavailable")
	}
	if allocations := testing.AllocsPerRun(1000, func() { cache.Ready() }); allocations != 0 {
		t.Fatalf("readiness allocations = %v", allocations)
	}
	finish := cache.authorities.fences[0].BeginMutation()
	if cache.Ready() {
		t.Fatal("pending mutation reports ready")
	}
	finish()
	if !cache.Ready() {
		t.Fatal("completed mutation prevents readiness")
	}
	now = time.Time{}
	if cache.Ready() {
		t.Fatal("zero clock reports ready")
	}
	now = time.Unix(1000, 0)
	cache.Close()
	if cache.Ready() {
		t.Fatal("closed cache reports ready")
	}
	var absent *Cache
	if absent.Ready() {
		t.Fatal("absent cache reports ready")
	}
}
