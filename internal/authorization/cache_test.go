package authorization

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/agentstation/starport/internal/account"
	"github.com/agentstation/starport/internal/apikey"
)

type sourceFunc func(context.Context, Identity) (Candidate, error)

func (f sourceFunc) Load(ctx context.Context, identity Identity) (Candidate, error) {
	return f(ctx, identity)
}

func cacheTestLimits() CacheLimits {
	return CacheLimits{Entries: 4, Bytes: 16384, BundleBytes: 4096, ConcurrentLoads: 4, TenantLoads: 2, LoadTimeout: time.Second, PermissionLifetime: 5 * time.Minute, ClockUncertainty: 30 * time.Second}
}

func cacheCandidate(id Identity, now time.Time) Candidate {
	tenant := id.Tenant
	if tenant == "" {
		tenant = "account"
	}
	return Candidate{
		Key:      apikey.Record{Revision: 1, APIKey: apikey.APIKey{ID: "key", Hash: id.Subject, AccountID: tenant, Active: true, Scopes: []string{"chat:write"}, Metadata: map[string]any{"nested": map[string]any{"value": "original"}}}},
		Account:  account.Record{Revision: 1, Account: account.Account{ID: tenant, Active: true, Access: []account.ProviderAccess{{Provider: "provider", Models: []string{"model"}}}}},
		Evidence: []Evidence{{Authority: "deployment", Epoch: "epoch", Sequence: 1, VerifiedAt: now, ValidUntil: now.Add(5 * time.Minute)}},
	}
}

func newTestCache(t *testing.T, source Source, limits CacheLimits, clock Clock) *Cache {
	t.Helper()
	authorities, err := NewAuthoritySet(NewFence("deployment", "epoch"))
	if err != nil {
		t.Fatal(err)
	}
	cache, err := NewCache(source, authorities, limits, clock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cache.Close)
	return cache
}

func TestCacheWarmReadsAndCallerOwnership(t *testing.T) {
	now := time.Unix(1000, 0)
	id := Identity{Tenant: "account", Subject: "hash"}
	candidate := cacheCandidate(id, now)
	var calls atomic.Int64
	cache := newTestCache(t, sourceFunc(func(context.Context, Identity) (Candidate, error) { calls.Add(1); return candidate, nil }), cacheTestLimits(), func() (time.Time, bool) { return now, true })
	bundle, err := cache.Resolve(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	candidate.Key.APIKey.Scopes[0] = "admin"
	candidate.Account.Account.Access[0].Models[0] = "foreign"
	key := bundle.Key()
	key.APIKey.Metadata["nested"].(map[string]any)["value"] = "changed"
	key.APIKey.Scopes[0] = "admin"
	ownedAccount := bundle.Account()
	ownedAccount.Account.Access[0].Models[0] = "changed"
	for range 10 {
		current, err := cache.Resolve(t.Context(), id)
		if err != nil {
			t.Fatal(err)
		}
		if current.Key().APIKey.Scopes[0] != "chat:write" || current.Key().APIKey.Metadata["nested"].(map[string]any)["value"] != "original" || current.Account().Account.Access[0].Models[0] != "model" {
			t.Fatal("caller or source changed cached policy")
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("source calls = %d", calls.Load())
	}
	allocations := testing.AllocsPerRun(1000, func() {
		if _, err := cache.Resolve(t.Context(), id); err != nil {
			panic(err)
		}
	})
	if allocations != 0 {
		t.Fatalf("warm lookup allocations = %v", allocations)
	}
}

func TestCacheCoalescesAndSeparatesCallerCancellation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var calls atomic.Int64
		release := make(chan struct{})
		cache := newTestCache(t, sourceFunc(func(ctx context.Context, id Identity) (Candidate, error) {
			calls.Add(1)
			select {
			case <-ctx.Done():
				return Candidate{}, ctx.Err()
			case <-release:
				return cacheCandidate(id, time.Now()), nil
			}
		}), cacheTestLimits(), func() (time.Time, bool) { return time.Now(), true })
		id := Identity{Tenant: "account", Subject: "hash"}
		firstCtx, cancel := context.WithCancel(t.Context())
		first := make(chan error, 1)
		second := make(chan error, 1)
		go func() { _, err := cache.Resolve(firstCtx, id); first <- err }()
		synctest.Wait()
		go func() { _, err := cache.Resolve(t.Context(), id); second <- err }()
		synctest.Wait()
		cancel()
		if err := <-first; !errors.Is(err, context.Canceled) {
			t.Fatalf("first = %v", err)
		}
		close(release)
		if err := <-second; err != nil {
			t.Fatal(err)
		}
		if calls.Load() != 1 {
			t.Fatalf("source calls = %d", calls.Load())
		}
	})
}

func TestCacheBoundsTenantLoadsWithoutBlockingOtherTenants(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		release := make(chan struct{})
		limits := cacheTestLimits()
		limits.TenantLoads = 1
		cache := newTestCache(t, sourceFunc(func(ctx context.Context, id Identity) (Candidate, error) {
			select {
			case <-ctx.Done():
				return Candidate{}, ctx.Err()
			case <-release:
				return cacheCandidate(id, time.Now()), nil
			}
		}), limits, func() (time.Time, bool) { return time.Now(), true })
		done := make(chan error, 2)
		go func() { _, err := cache.Resolve(t.Context(), Identity{Tenant: "one", Subject: "first"}); done <- err }()
		synctest.Wait()
		if _, err := cache.Resolve(t.Context(), Identity{Tenant: "one", Subject: "second"}); !errors.Is(err, ErrCapacity) {
			t.Fatalf("tenant cap = %v", err)
		}
		go func() { _, err := cache.Resolve(t.Context(), Identity{Tenant: "two", Subject: "third"}); done <- err }()
		synctest.Wait()
		close(release)
		for range 2 {
			if err := <-done; err != nil {
				t.Fatal(err)
			}
		}
	})
}

func TestCacheMutationRejectsInFlightPublication(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		release := make(chan struct{})
		var calls atomic.Int64
		cache := newTestCache(t, sourceFunc(func(ctx context.Context, id Identity) (Candidate, error) {
			call := calls.Add(1)
			select {
			case <-ctx.Done():
				return Candidate{}, ctx.Err()
			case <-release:
				candidate := cacheCandidate(id, time.Now())
				if call > 1 {
					candidate.Key.APIKey.Scopes = []string{"models:read"}
				}
				return candidate, nil
			}
		}), cacheTestLimits(), func() (time.Time, bool) { return time.Now(), true })
		done := make(chan *Bundle, 1)
		go func() {
			bundle, err := cache.Resolve(t.Context(), Identity{Subject: "hash"})
			if err != nil {
				t.Error(err)
			}
			done <- bundle
		}()
		synctest.Wait()
		cache.authorities.beginMutation()()
		close(release)
		bundle := <-done
		if bundle == nil || len(bundle.Key().APIKey.Scopes) != 1 || bundle.Key().APIKey.Scopes[0] != "models:read" {
			t.Fatal("published policy from before the mutation")
		}
		if calls.Load() != 2 {
			t.Fatalf("full source reads = %d", calls.Load())
		}
	})
}

func TestCacheExpiryDoesNotConvertFailureToAbsence(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var outage atomic.Bool
		var calls atomic.Int64
		cache := newTestCache(t, sourceFunc(func(_ context.Context, id Identity) (Candidate, error) {
			calls.Add(1)
			if outage.Load() {
				return Candidate{}, ErrUnavailable
			}
			return cacheCandidate(id, time.Now()), nil
		}), cacheTestLimits(), func() (time.Time, bool) { return time.Now(), true })
		id := Identity{Subject: "hash"}
		old, err := cache.Resolve(t.Context(), id)
		if err != nil {
			t.Fatal(err)
		}
		outage.Store(true)
		time.Sleep(270 * time.Second)
		for range 2 {
			if _, err := cache.Resolve(t.Context(), id); !errors.Is(err, ErrUnavailable) {
				t.Fatalf("outage = %v", err)
			}
		}
		if calls.Load() != 3 {
			t.Fatalf("failed reads became cached absence: %d calls", calls.Load())
		}
		if err := old.Permit().Check(time.Now(), true); !errors.Is(err, ErrExpired) {
			t.Fatalf("old receipt = %v", err)
		}
	})
}

func TestCacheDeadlineAndCloseCancelWorkers(t *testing.T) {
	for _, closeEarly := range []bool{false, true} {
		synctest.Test(t, func(t *testing.T) {
			cache := newTestCache(t, sourceFunc(func(ctx context.Context, _ Identity) (Candidate, error) { <-ctx.Done(); return Candidate{}, ctx.Err() }), cacheTestLimits(), func() (time.Time, bool) { return time.Now(), true })
			done := make(chan error, 1)
			go func() { _, err := cache.Resolve(t.Context(), Identity{Subject: "hash"}); done <- err }()
			synctest.Wait()
			if closeEarly {
				cache.Close()
			} else {
				time.Sleep(time.Second)
			}
			err := <-done
			expected := error(context.DeadlineExceeded)
			if closeEarly {
				expected = ErrUnavailable
			}
			if !errors.Is(err, expected) {
				t.Fatalf("close=%v: %v", closeEarly, err)
			}
			cache.Close()
			if _, err := cache.Resolve(t.Context(), Identity{Subject: "hash"}); !errors.Is(err, ErrUnavailable) {
				t.Fatalf("closed cache = %v", err)
			}
		})
	}
}

func TestCacheRejectsMissingDependenciesAndOversizedBundles(t *testing.T) {
	now := time.Unix(1000, 0)
	for _, tc := range []struct {
		name   string
		change func(*Candidate)
		want   error
	}{
		{"account", func(c *Candidate) { c.Account.Revision = 0 }, ErrEvidence},
		{"team", func(c *Candidate) { c.Key.APIKey.TeamID = "missing" }, ErrEvidence},
		{"foreign_account", func(c *Candidate) { c.Account.Account.ID = "foreign" }, ErrEvidence},
		{"oversized", func(c *Candidate) { c.Key.APIKey.Scopes = make([]string, 10000) }, ErrCapacity},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cache := newTestCache(t, sourceFunc(func(_ context.Context, id Identity) (Candidate, error) {
				candidate := cacheCandidate(id, now)
				tc.change(&candidate)
				return candidate, nil
			}), cacheTestLimits(), func() (time.Time, bool) { return now, true })
			if _, err := cache.Resolve(t.Context(), Identity{Subject: "hash"}); !errors.Is(err, tc.want) {
				t.Fatalf("resolve = %v", err)
			}
		})
	}
}

func TestCacheEvictsWorkingSetAndCloseRevokesReceipts(t *testing.T) {
	now := time.Unix(1000, 0)
	var calls atomic.Int64
	limits := cacheTestLimits()
	limits.Entries = 1
	cache := newTestCache(t, sourceFunc(func(_ context.Context, id Identity) (Candidate, error) {
		calls.Add(1)
		return cacheCandidate(id, now), nil
	}), limits, func() (time.Time, bool) { return now, true })
	first, err := cache.Resolve(t.Context(), Identity{Subject: "first"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cache.Resolve(t.Context(), Identity{Subject: "second"}); err != nil {
		t.Fatal(err)
	}
	if _, err := cache.Resolve(t.Context(), Identity{Subject: "first"}); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 3 {
		t.Fatalf("source calls = %d", calls.Load())
	}
	cache.Close()
	if err := first.Permit().Check(now, true); !errors.Is(err, ErrWithdrawn) {
		t.Fatalf("closed receipt = %v", err)
	}
}

func TestCacheKeyExpiryDoesNotRewriteAuthorityReceipt(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		now := time.Now()
		cache := newTestCache(t, sourceFunc(func(_ context.Context, id Identity) (Candidate, error) {
			candidate := cacheCandidate(id, now)
			expiry := now.Add(time.Minute)
			candidate.Key.APIKey.ExpiresAt = &expiry
			return candidate, nil
		}), cacheTestLimits(), func() (time.Time, bool) { return time.Now(), true })
		bundle, err := cache.Resolve(t.Context(), Identity{Subject: "hash"})
		if err != nil {
			t.Fatal(err)
		}
		if bundle.Permit().Evidence()[0].ValidUntil != now.Add(5*time.Minute) {
			t.Fatal("key expiry rewrote authority evidence")
		}
		time.Sleep(30 * time.Second)
		if err := bundle.Permit().Check(time.Now(), true); !errors.Is(err, ErrExpired) {
			t.Fatalf("key expiry = %v", err)
		}
	})
}

func TestCacheBoundsTotalBytes(t *testing.T) {
	now := time.Unix(1000, 0)
	id := Identity{Subject: "one"}
	candidate := cacheCandidate(id, now)
	authorities, err := NewAuthoritySet(NewFence("deployment", "epoch"))
	if err != nil {
		t.Fatal(err)
	}
	ticket, err := authorities.start()
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := ticket.accept(candidate.Evidence, now, 5*time.Minute, 30*time.Second, true)
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := freeze(candidate, id, receipt, 4096)
	if err != nil {
		t.Fatal(err)
	}
	limits := cacheTestLimits()
	limits.Bytes, limits.BundleBytes = bundle.bytes, bundle.bytes
	var calls atomic.Int64
	cache := newTestCache(t, sourceFunc(func(_ context.Context, id Identity) (Candidate, error) {
		calls.Add(1)
		return cacheCandidate(id, now), nil
	}), limits, func() (time.Time, bool) { return now, true })
	for _, subject := range []string{"one", "two", "one"} {
		if _, err := cache.Resolve(t.Context(), Identity{Subject: subject}); err != nil {
			t.Fatal(err)
		}
	}
	if calls.Load() != 3 {
		t.Fatalf("byte limit did not evict: %d calls", calls.Load())
	}
}

func TestCacheGlobalLoadLimitProtectsInFlightEntries(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		limits := cacheTestLimits()
		limits.Entries, limits.ConcurrentLoads, limits.TenantLoads = 1, 1, 1
		release := make(chan struct{})
		cache := newTestCache(t, sourceFunc(func(ctx context.Context, id Identity) (Candidate, error) {
			select {
			case <-ctx.Done():
				return Candidate{}, ctx.Err()
			case <-release:
				return cacheCandidate(id, time.Now()), nil
			}
		}), limits, func() (time.Time, bool) { return time.Now(), true })
		done := make(chan error, 1)
		go func() { _, err := cache.Resolve(t.Context(), Identity{Tenant: "first", Subject: "first"}); done <- err }()
		synctest.Wait()
		if _, err := cache.Resolve(t.Context(), Identity{Tenant: "second", Subject: "second"}); !errors.Is(err, ErrCapacity) {
			t.Fatalf("global cap = %v", err)
		}
		close(release)
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		if _, err := cache.Resolve(t.Context(), Identity{Tenant: "second", Subject: "second"}); err != nil {
			t.Fatal(err)
		}
	})
}

func TestCacheUnknownClockRefusesWarmRead(t *testing.T) {
	now := time.Unix(1000, 0)
	var healthy atomic.Bool
	healthy.Store(true)
	var calls atomic.Int64
	cache := newTestCache(t, sourceFunc(func(_ context.Context, id Identity) (Candidate, error) {
		calls.Add(1)
		return cacheCandidate(id, now), nil
	}), cacheTestLimits(), func() (time.Time, bool) { return now, healthy.Load() })
	id := Identity{Subject: "hash"}
	if _, err := cache.Resolve(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	healthy.Store(false)
	if _, err := cache.Resolve(t.Context(), id); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("clock health = %v", err)
	}
	if calls.Load() != 1 {
		t.Fatal("unknown clock started another authority load")
	}
}

func TestInvalidColdLookupPreservesWarmWorkingSet(t *testing.T) {
	now := time.Unix(1000, 0)
	var calls atomic.Int64
	limits := cacheTestLimits()
	limits.Entries = 1
	cache := newTestCache(t, sourceFunc(func(_ context.Context, id Identity) (Candidate, error) {
		calls.Add(1)
		if id.Subject == "invalid" {
			return Candidate{}, ErrEvidence
		}
		return cacheCandidate(id, now), nil
	}), limits, func() (time.Time, bool) { return now, true })
	if _, err := cache.Resolve(t.Context(), Identity{Subject: "valid"}); err != nil {
		t.Fatal(err)
	}
	if _, err := cache.Resolve(t.Context(), Identity{Subject: "invalid"}); !errors.Is(err, ErrEvidence) {
		t.Fatalf("invalid lookup = %v", err)
	}
	if _, err := cache.Resolve(t.Context(), Identity{Subject: "valid"}); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 {
		t.Fatal("invalid lookup evicted valid policy")
	}
}

func TestCacheRetainsEveryAuthorityRequirement(t *testing.T) {
	now := time.Unix(1000, 0)
	set := authorityPair(t)
	sqlSequence := uint64(1)
	kvSequence := uint64(1)
	source := sourceFunc(func(_ context.Context, id Identity) (Candidate, error) {
		candidate := cacheCandidate(id, now)
		candidate.Evidence = []Evidence{authorityEvidence("kv", "kv-epoch", kvSequence, now), authorityEvidence("sql", "sql-epoch", sqlSequence, now)}
		return candidate, nil
	})
	cache, err := NewCache(source, set, cacheTestLimits(), func() (time.Time, bool) { return now, true })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cache.Close)
	id := Identity{Subject: "hash"}
	old, err := cache.Resolve(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	if err := set.Observe(authorityEvidence("sql", "sql-epoch", 2, now)); err != nil {
		t.Fatal(err)
	}
	kvSequence = 100
	if _, err := cache.Resolve(t.Context(), id); !errors.Is(err, ErrEvidence) {
		t.Fatalf("stale SQL admitted: %v", err)
	}
	if err := old.Permit().Check(now, true); !errors.Is(err, ErrWithdrawn) {
		t.Fatalf("old permit = %v", err)
	}
	sqlSequence = 2
	if _, err := cache.Resolve(t.Context(), id); err != nil {
		t.Fatal(err)
	}
}
