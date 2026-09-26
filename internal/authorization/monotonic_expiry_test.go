package authorization

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestKeyExpiryPreservesMonotonicCacheDeadline(t *testing.T) {
	now := time.Now()
	keyExpiry := now.Add(time.Minute).UTC()
	cache := newTestCache(t, sourceFunc(func(_ context.Context, id Identity) (Candidate, error) {
		candidate := cacheCandidate(id, now)
		candidate.Key.APIKey.ExpiresAt = &keyExpiry
		return candidate, nil
	}), cacheTestLimits(), func() (time.Time, bool) { return now, true })
	bundle, err := cache.Resolve(t.Context(), Identity{Subject: "key"})
	if err != nil {
		t.Fatal(err)
	}
	deadline := bundle.Permit().Deadline()
	if deadline == deadline.Round(0) {
		t.Fatal("persisted key expiry removed monotonic cache expiry")
	}
	if err := bundle.Permit().Check(now.Add(30*time.Second), true); !errors.Is(err, ErrExpired) {
		t.Fatalf("permission at uncertainty-adjusted expiry = %v", err)
	}
}

func TestInvalidReceiptCannotReviveAfterClockRecovery(t *testing.T) {
	for _, shift := range []time.Duration{-time.Second, 2 * time.Minute} {
		t.Run(shift.String(), func(t *testing.T) {
			now := time.Now()
			fence := NewFence("policy", "epoch")
			ticket, err := fence.Start()
			if err != nil {
				t.Fatal(err)
			}
			receipt, err := ticket.Accept(Evidence{Authority: "policy", Epoch: "epoch", Sequence: 1, VerifiedAt: now, ValidUntil: now.Add(time.Minute)}, now, time.Minute, 0, true)
			if err != nil {
				t.Fatal(err)
			}
			copy := receipt
			if err := receipt.Check(now.Round(0).Add(shift), true); err == nil {
				t.Fatal("clock discontinuity accepted")
			}
			if err := copy.Check(now.Add(time.Second), true); err == nil {
				t.Fatal("invalid receipt revived after clock recovery")
			}
			evidence := receipt.Evidence()
			evidence.VerifiedAt = now.Add(time.Second)
			evidence.ValidUntil = evidence.VerifiedAt.Add(time.Minute)
			fresh, err := ticket.Accept(evidence, evidence.VerifiedAt, time.Minute, 0, true)
			if err != nil {
				t.Fatal(err)
			}
			if err := fresh.Check(now.Add(time.Second), true); err != nil {
				t.Fatal("unrelated fresh receipt blocked", err)
			}
		})
	}
}
