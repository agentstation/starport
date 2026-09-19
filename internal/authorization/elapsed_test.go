package authorization

import (
	"context"
	"errors"
	"testing"
	"time"
)

func testElapsedClock(clock Clock) ElapsedClock {
	return func() (time.Duration, bool) {
		now, healthy := clock()
		return time.Duration(now.UnixNano()), healthy
	}
}

func TestElapsedPermissionExpiresAcrossSuspend(t *testing.T) {
	wall := time.Now()
	elapsed := time.Second
	set := authorityPair(t)
	source := sourceFunc(func(_ context.Context, id Identity) (Candidate, error) {
		candidate := cacheCandidate(id, wall)
		candidate.Evidence = []Evidence{authorityEvidence("kv", "kv-epoch", 1, wall), authorityEvidence("sql", "sql-epoch", 1, wall)}
		return candidate, nil
	})
	limits := cacheTestLimits()
	limits.PermissionLifetime, limits.ClockUncertainty = time.Minute, 0
	cache, err := NewCache(source, set, limits, func() (time.Time, bool) { return wall, true }, func() (time.Duration, bool) { return elapsed, true })
	if err != nil {
		t.Fatal(err)
	}
	defer cache.Close()
	bundle, err := cache.Resolve(t.Context(), Identity{Subject: "hash"})
	if err != nil {
		t.Fatal(err)
	}
	permit := bundle.Permit()
	if allocations := testing.AllocsPerRun(1000, func() {
		if err := permit.Check(wall, true); err != nil {
			panic(err)
		}
	}); allocations != 0 {
		t.Fatalf("permission allocations = %v", allocations)
	}
	// Elapsed time advances while the host wall and Go monotonic samples stay fixed.
	elapsed += time.Minute
	if err := permit.Check(wall, true); !errors.Is(err, ErrExpired) {
		t.Fatalf("permission after suspend = %v", err)
	}
	elapsed = time.Second
	if err := bundle.Permit().Check(wall, true); !errors.Is(err, ErrExpired) {
		t.Fatalf("counter recovery revived permission: %v", err)
	}
}

func TestElapsedPermissionRejectsUnknownAndRollback(t *testing.T) {
	for _, rollback := range []bool{false, true} {
		now, healthy := time.Second, true
		permit := Permit{}
		if err := permit.boundElapsed(func() (time.Duration, bool) { return now, healthy }, now, time.Minute); err != nil {
			t.Fatal(err)
		}
		if rollback {
			now = 0
		} else {
			healthy = false
		}
		if err := permit.elapsed.check(); !errors.Is(err, ErrUnavailable) {
			t.Fatalf("invalid elapsed evidence = %v", err)
		}
		now, healthy = 2*time.Second, true
		if err := permit.elapsed.check(); !errors.Is(err, ErrUnavailable) {
			t.Fatalf("counter recovery revived permission: %v", err)
		}
	}
}

func TestElapsedPermissionCountsSourceDelay(t *testing.T) {
	wall := time.Now()
	elapsed := time.Second
	source := sourceFunc(func(_ context.Context, id Identity) (Candidate, error) {
		elapsed += time.Minute
		candidate := cacheCandidate(id, wall)
		candidate.Evidence = []Evidence{authorityEvidence("kv", "kv-epoch", 1, wall), authorityEvidence("sql", "sql-epoch", 1, wall)}
		return candidate, nil
	})
	limits := cacheTestLimits()
	limits.PermissionLifetime, limits.ClockUncertainty = time.Minute, 0
	cache, err := NewCache(source, authorityPair(t), limits, func() (time.Time, bool) { return wall, true }, func() (time.Duration, bool) { return elapsed, true })
	if err != nil {
		t.Fatal(err)
	}
	defer cache.Close()
	if _, err := cache.Resolve(t.Context(), Identity{Subject: "hash"}); !errors.Is(err, ErrExpired) {
		t.Fatalf("delayed source extended permission: %v", err)
	}
}

func TestElapsedClockFailureRefusesColdLoadsAndReadiness(t *testing.T) {
	cache := newTestCache(t, sourceFunc(func(context.Context, Identity) (Candidate, error) {
		t.Error("unavailable elapsed counter reached policy storage")
		return Candidate{}, ErrUnavailable
	}), cacheTestLimits(), func() (time.Time, bool) { return time.Now(), true })
	cache.elapsed = func() (time.Duration, bool) { return 0, false }
	if cache.Ready() {
		t.Fatal("unknown elapsed counter reported ready")
	}
	if _, err := cache.Resolve(t.Context(), Identity{Subject: "hash"}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("unknown elapsed counter = %v", err)
	}
}
