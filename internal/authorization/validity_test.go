package authorization

import (
	"errors"
	"sync"
	"testing"
	"time"
)

func verifiedReceipt(t *testing.T, fence *Fence, now time.Time) Receipt {
	t.Helper()
	ticket, err := fence.Start()
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := ticket.Accept(Evidence{
		Authority: "deployment", Epoch: "epoch-one", Sequence: 1,
		VerifiedAt: now, ValidUntil: now.Add(10 * time.Minute),
	}, now, 5*time.Minute, 30*time.Second, true)
	if err != nil {
		t.Fatal(err)
	}
	return receipt
}

func TestReceiptBoundsValidityAndClockHealth(t *testing.T) {
	now := time.Unix(1000, 0)
	receipt := verifiedReceipt(t, NewFence("deployment", "epoch-one"), now)
	if want := now.Add(270 * time.Second); receipt.Deadline() != want {
		t.Fatalf("deadline = %v, want %v", receipt.Deadline(), want)
	}
	for _, tc := range []struct {
		name    string
		at      time.Time
		healthy bool
		want    error
	}{
		{"valid", now.Add(269 * time.Second), true, nil},
		{"boundary", now.Add(270 * time.Second), true, ErrExpired},
		{"expired", now.Add(271 * time.Second), true, ErrExpired},
		{"clock_unknown", now, false, ErrUnavailable},
		{"clock_reversed", now.Add(-31 * time.Second), true, ErrUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := receipt.Check(tc.at, tc.healthy); !errors.Is(err, tc.want) {
				t.Fatalf("check = %v, want %v", err, tc.want)
			}
		})
	}
	if receipt.Evidence().ValidUntil != now.Add(10*time.Minute) {
		t.Fatal("receipt changed the original evidence")
	}
}

func TestMutationRevokesCopiesAndInFlightLoads(t *testing.T) {
	now := time.Unix(1000, 0)
	fence := NewFence("deployment", "epoch-one")
	old := verifiedReceipt(t, fence, now)
	copy := old
	ticket, err := fence.Start()
	if err != nil {
		t.Fatal(err)
	}
	finish := fence.BeginMutation()
	if _, err := fence.Start(); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("load during mutation = %v", err)
	}
	for _, receipt := range []Receipt{old, copy} {
		if err := receipt.Check(now, true); !errors.Is(err, ErrWithdrawn) {
			t.Fatalf("old receipt = %v", err)
		}
	}
	finish()
	finish()
	if _, err := ticket.Accept(old.Evidence(), now, 5*time.Minute, 0, true); !errors.Is(err, ErrWithdrawn) {
		t.Fatalf("stale load = %v", err)
	}
	if err := old.Check(now, true); !errors.Is(err, ErrWithdrawn) {
		t.Fatalf("mutation completion restored old receipt: %v", err)
	}
	if err := verifiedReceipt(t, fence, now).Check(now, true); err != nil {
		t.Fatal(err)
	}
}

func TestOverlappingMutationsKeepAdmissionClosed(t *testing.T) {
	fence := NewFence("deployment", "epoch-one")
	first := fence.BeginMutation()
	second := fence.BeginMutation()
	first()
	if _, err := fence.Start(); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("unfinished mutation = %v", err)
	}
	second()
	if _, err := fence.Start(); err != nil {
		t.Fatal(err)
	}
}

func TestReceiptRejectsUnverifiedEvidence(t *testing.T) {
	now := time.Unix(1000, 0)
	valid := Evidence{Authority: "deployment", Epoch: "epoch-one", Sequence: 1, VerifiedAt: now, ValidUntil: now.Add(time.Minute)}
	for _, tc := range []struct {
		name   string
		change func(*Evidence)
	}{
		{"authority", func(e *Evidence) { e.Authority = "" }},
		{"epoch", func(e *Evidence) { e.Epoch = "" }},
		{"sequence", func(e *Evidence) { e.Sequence = 0 }},
		{"future", func(e *Evidence) { e.VerifiedAt = now.Add(time.Hour) }},
		{"reversed", func(e *Evidence) { e.ValidUntil = now.Add(-time.Second) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ticket, err := NewFence("deployment", "epoch-one").Start()
			if err != nil {
				t.Fatal(err)
			}
			evidence := valid
			tc.change(&evidence)
			if _, err := ticket.Accept(evidence, now, 5*time.Minute, 30*time.Second, true); !errors.Is(err, ErrEvidence) {
				t.Fatalf("accept = %v", err)
			}
		})
	}
}

func TestConcurrentMutationCompletion(t *testing.T) {
	fence := NewFence("deployment", "epoch-one")
	receipt := verifiedReceipt(t, fence, time.Unix(1000, 0))
	finish := make([]func(), 16)
	for i := range finish {
		finish[i] = fence.BeginMutation()
	}
	var group sync.WaitGroup
	for _, done := range finish {
		group.Go(func() { done(); done() })
	}
	group.Wait()
	if _, err := fence.Start(); err != nil {
		t.Fatal(err)
	}
	if err := receipt.Check(time.Unix(1001, 0), true); !errors.Is(err, ErrWithdrawn) {
		t.Fatalf("old receipt = %v", err)
	}
}

func TestReceiptCheckDoesNotAllocate(t *testing.T) {
	now := time.Unix(1000, 0)
	receipt := verifiedReceipt(t, NewFence("deployment", "epoch-one"), now)
	allocations := testing.AllocsPerRun(1000, func() {
		if err := receipt.Check(now, true); err != nil {
			panic(err)
		}
	})
	if allocations != 0 {
		t.Fatalf("allocations = %v, want zero", allocations)
	}
}

func TestReceiptRejectsInvalidClockBounds(t *testing.T) {
	now := time.Unix(1000, 0)
	receipt := verifiedReceipt(t, NewFence("deployment", "epoch-one"), now)
	for _, tc := range []struct {
		name                  string
		lifetime, uncertainty time.Duration
		healthy               bool
	}{
		{"zero_lifetime", 0, 0, true},
		{"negative_uncertainty", time.Minute, -time.Second, true},
		{"exhausted_validity", time.Minute, time.Minute, true},
		{"unknown_clock", time.Minute, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ticket, err := NewFence("deployment", "epoch-one").Start()
			if err != nil {
				t.Fatal(err)
			}
			if _, err := ticket.Accept(receipt.Evidence(), now, tc.lifetime, tc.uncertainty, tc.healthy); !errors.Is(err, ErrUnavailable) {
				t.Fatalf("accept = %v", err)
			}
		})
	}
}

func TestVerifiedRequirementRejectsRollback(t *testing.T) {
	now := time.Unix(1000, 0)
	fence := NewFence("deployment", "epoch-one")
	old := verifiedReceipt(t, fence, now)
	if err := fence.Require(2); err != nil {
		t.Fatal(err)
	}
	if err := old.Check(now, true); !errors.Is(err, ErrWithdrawn) {
		t.Fatalf("withdrawal = %v", err)
	}
	if err := fence.Require(1); err != nil {
		t.Fatal(err)
	}
	ticket, err := fence.Start()
	if err != nil {
		t.Fatal(err)
	}
	evidence := old.Evidence()
	if _, err := ticket.Accept(evidence, now, 5*time.Minute, 30*time.Second, true); !errors.Is(err, ErrEvidence) {
		t.Fatalf("rollback = %v", err)
	}
	evidence.Sequence = 2
	if _, err := ticket.Accept(evidence, now, 5*time.Minute, 30*time.Second, true); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*Evidence){
		func(e *Evidence) { e.Authority = "foreign" },
		func(e *Evidence) { e.Epoch = "foreign" },
	} {
		foreign := evidence
		change(&foreign)
		if _, err := ticket.Accept(foreign, now, 5*time.Minute, 30*time.Second, true); !errors.Is(err, ErrEvidence) {
			t.Fatalf("foreign authority = %v", err)
		}
	}
}

func TestConcurrentChecksObserveWithdrawal(t *testing.T) {
	now := time.Unix(1000, 0)
	fence := NewFence("deployment", "epoch-one")
	receipt := verifiedReceipt(t, fence, now)
	var group sync.WaitGroup
	for range 4 {
		group.Go(func() {
			for range 1000 {
				if err := receipt.Check(now, true); err != nil && !errors.Is(err, ErrWithdrawn) {
					t.Errorf("check = %v", err)
				}
			}
		})
	}
	group.Go(func() {
		for range 100 {
			fence.BeginMutation()()
		}
	})
	group.Wait()
	if err := receipt.Check(now, true); !errors.Is(err, ErrWithdrawn) {
		t.Fatalf("final check = %v", err)
	}
}
