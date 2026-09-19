package authorization

import (
	"slices"
	"sync"
	"time"
)

const maxAuthorities = 8

// AuthoritySet requires independent evidence from each configured policy owner.
// Configuration owns this set. A candidate cannot omit an authority requirement.
type AuthoritySet struct{ fences []*Fence }

// NewAuthoritySet binds at most eight distinct authority epochs without reads.
func NewAuthoritySet(fences ...*Fence) (*AuthoritySet, error) {
	if len(fences) == 0 || len(fences) > maxAuthorities {
		return nil, ErrEvidence
	}
	seen := make(map[string]bool, len(fences))
	for _, fence := range fences {
		if fence == nil || fence.authority == "" || fence.epoch == "" || seen[fence.authority] {
			return nil, ErrEvidence
		}
		seen[fence.authority] = true
	}
	return &AuthoritySet{fences: slices.Clone(fences)}, nil
}

// Observe records a verified requirement for one authority only.
// An epoch change closes that authority until explicit recovery replaces its fence.
func (s *AuthoritySet) Observe(evidence Evidence) error {
	if evidence.Sequence == 0 || evidence.Epoch == "" {
		return ErrEvidence
	}
	for _, fence := range s.fences {
		if fence.authority != evidence.Authority {
			continue
		}
		if fence.epoch != evidence.Epoch {
			fence.Withdraw()
			return ErrWithdrawn
		}
		return fence.Require(evidence.Sequence)
	}
	return ErrEvidence
}

type tickets []Ticket

func (s *AuthoritySet) start() (tickets, error) {
	result := make(tickets, len(s.fences))
	for i, fence := range s.fences {
		ticket, err := fence.Start()
		if err != nil {
			return nil, err
		}
		result[i] = ticket
	}
	return result, nil
}

func (s *AuthoritySet) beginMutation() func() {
	finish := make([]func(), len(s.fences))
	for i, fence := range s.fences {
		finish[i] = fence.BeginMutation()
	}
	return sync.OnceFunc(func() {
		for _, done := range finish {
			done()
		}
	})
}

// Permit requires every authority receipt to remain valid.
// Its private receipts cannot change after publication.
type Permit struct{ receipts []Receipt }

func (t tickets) accept(evidence []Evidence, now time.Time, lifetime, uncertainty time.Duration, healthy bool) (Permit, error) {
	if len(t) == 0 || len(evidence) != len(t) {
		return Permit{}, ErrEvidence
	}
	result := Permit{receipts: make([]Receipt, len(t))}
	for i, ticket := range t {
		found := false
		for _, item := range evidence {
			if item.Authority != ticket.fence.authority {
				continue
			}
			if found {
				return Permit{}, ErrEvidence
			}
			receipt, err := ticket.Accept(item, now, lifetime, uncertainty, healthy)
			if err != nil {
				return Permit{}, err
			}
			result.receipts[i] = receipt
			found = true
		}
		if !found {
			return Permit{}, ErrEvidence
		}
	}
	if err := result.Check(now, healthy); err != nil {
		return Permit{}, err
	}
	return result, nil
}

// Check rechecks every owner before admission, retries, and cached response delivery.
func (p Permit) Check(now time.Time, healthy bool) error {
	if len(p.receipts) == 0 {
		return ErrEvidence
	}
	for _, receipt := range p.receipts {
		if err := receipt.Check(now, healthy); err != nil {
			return err
		}
	}
	return nil
}

// Evidence returns caller-owned authority receipts without extending validity.
func (p Permit) Evidence() []Evidence {
	result := make([]Evidence, len(p.receipts))
	for i, receipt := range p.receipts {
		result[i] = receipt.Evidence()
	}
	return result
}

// Deadline returns the earliest authority deadline.
func (p Permit) Deadline() time.Time {
	var deadline time.Time
	for _, receipt := range p.receipts {
		if deadline.IsZero() || receipt.deadline.Before(deadline) {
			deadline = receipt.deadline
		}
	}
	return deadline
}

func (p *Permit) clamp(deadline, now time.Time) {
	for i := range p.receipts {
		p.receipts[i].absoluteDeadline = deadline.Round(0)
		if deadline.Before(p.receipts[i].deadline) {
			// Anchor persisted UTC expiry to this sample's monotonic reading.
			p.receipts[i].deadline = now.Add(deadline.Sub(now))
		}
	}
}
