package authorization

import (
	"errors"
	"testing"
	"time"
)

func authorityEvidence(name, epoch string, sequence uint64, now time.Time) Evidence {
	return Evidence{Authority: name, Epoch: epoch, Sequence: sequence, VerifiedAt: now, ValidUntil: now.Add(5 * time.Minute)}
}

func authorityPair(t *testing.T) *AuthoritySet {
	t.Helper()
	set, err := NewAuthoritySet(NewFence("kv", "kv-epoch"), NewFence("sql", "sql-epoch"))
	if err != nil {
		t.Fatal(err)
	}
	return set
}

func TestAuthorityVectorCannotCompensateForStaleSQL(t *testing.T) {
	now := time.Unix(1000, 0)
	set := authorityPair(t)
	if err := set.Observe(authorityEvidence("sql", "sql-epoch", 2, now)); err != nil {
		t.Fatal(err)
	}
	ticket, err := set.start()
	if err != nil {
		t.Fatal(err)
	}
	stale := []Evidence{authorityEvidence("kv", "kv-epoch", 100, now), authorityEvidence("sql", "sql-epoch", 1, now)}
	if _, err := ticket.accept(stale, now, 5*time.Minute, 30*time.Second, true); !errors.Is(err, ErrEvidence) {
		t.Fatalf("stale SQL = %v", err)
	}
	stale[1].Sequence = 2
	if _, err := ticket.accept(stale, now, 5*time.Minute, 30*time.Second, true); err != nil {
		t.Fatal(err)
	}
}

func TestAuthorityVectorRequiresExactOwners(t *testing.T) {
	now := time.Unix(1000, 0)
	kv := authorityEvidence("kv", "kv-epoch", 1, now)
	sql := authorityEvidence("sql", "sql-epoch", 1, now)
	for _, tc := range []struct {
		name     string
		evidence []Evidence
		want     error
	}{
		{"valid", []Evidence{kv, sql}, nil},
		{"reordered", []Evidence{sql, kv}, nil},
		{"missing", []Evidence{kv}, ErrEvidence},
		{"duplicate", []Evidence{kv, kv}, ErrEvidence},
		{"foreign", []Evidence{kv, authorityEvidence("foreign", "epoch", 1, now)}, ErrEvidence},
		{"wrong_epoch", []Evidence{kv, authorityEvidence("sql", "other", 1, now)}, ErrEvidence},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ticket, err := authorityPair(t).start()
			if err != nil {
				t.Fatal(err)
			}
			_, err = ticket.accept(tc.evidence, now, 5*time.Minute, 30*time.Second, true)
			if !errors.Is(err, tc.want) {
				t.Fatalf("accept = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestAuthorityEpochChangeBlocksRetainedPermits(t *testing.T) {
	now := time.Unix(1000, 0)
	set := authorityPair(t)
	ticket, err := set.start()
	if err != nil {
		t.Fatal(err)
	}
	permit, err := ticket.accept([]Evidence{authorityEvidence("kv", "kv-epoch", 1, now), authorityEvidence("sql", "sql-epoch", 1, now)}, now, 5*time.Minute, 30*time.Second, true)
	if err != nil {
		t.Fatal(err)
	}
	finish := set.beginMutation()
	if err := set.Observe(authorityEvidence("sql", "restored-epoch", 1, now)); !errors.Is(err, ErrWithdrawn) {
		t.Fatalf("epoch change = %v", err)
	}
	finish()
	if err := permit.Check(now, true); !errors.Is(err, ErrWithdrawn) {
		t.Fatalf("old permit = %v", err)
	}
	if _, err := set.start(); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("mutation completion reopened changed epoch: %v", err)
	}
	if err := set.Observe(authorityEvidence("sql", "sql-epoch", 10, now)); err != nil {
		t.Fatal(err)
	}
	if _, err := set.start(); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("old epoch publication reopened authority: %v", err)
	}
}

func TestPermitUsesEarliestDeadlineWithoutAllocations(t *testing.T) {
	now := time.Unix(1000, 0)
	set := authorityPair(t)
	ticket, err := set.start()
	if err != nil {
		t.Fatal(err)
	}
	evidence := []Evidence{authorityEvidence("kv", "kv-epoch", 1, now), authorityEvidence("sql", "sql-epoch", 1, now)}
	evidence[1].ValidUntil = now.Add(time.Minute)
	permit, err := ticket.accept(evidence, now, 5*time.Minute, 30*time.Second, true)
	if err != nil {
		t.Fatal(err)
	}
	if permit.Deadline() != now.Add(30*time.Second) {
		t.Fatalf("deadline = %v", permit.Deadline())
	}
	view := permit.Evidence()
	view[1].ValidUntil = now.Add(time.Hour)
	if allocations := testing.AllocsPerRun(1000, func() {
		if err := permit.Check(now, true); err != nil {
			panic(err)
		}
	}); allocations != 0 {
		t.Fatalf("allocations = %v", allocations)
	}
	if err := permit.Check(now.Add(30*time.Second), true); !errors.Is(err, ErrExpired) {
		t.Fatalf("expiry = %v", err)
	}
}

func TestAuthoritySetRefusesAmbiguousConfiguration(t *testing.T) {
	for _, fences := range [][]*Fence{nil, {nil}, {NewFence("", "epoch")}, {NewFence("kv", "")}, {NewFence("kv", "one"), NewFence("kv", "two")}} {
		if _, err := NewAuthoritySet(fences...); !errors.Is(err, ErrEvidence) {
			t.Fatalf("configuration = %v", err)
		}
	}
}
