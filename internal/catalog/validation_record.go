package catalog

import (
	"sync"
	"time"
)

// validationRecord holds what happened to the newest candidate this instance
// observed.
//
// It stays in memory on purpose. It describes the work of this instance, and
// another instance's refusal is not this instance's state. The accepted head
// is the durable value, and it lives in the generation store.
type validationRecord struct {
	mu        sync.Mutex
	state     RouteValidationState
	candidate GenerationRef
	accepted  GenerationRef
	rejected  Rejection
	now       func() time.Time
}

// observe records one candidate the connected runtime published. A candidate
// the accepted head already carries keeps the accepted state, so a repeated
// publication does not read as pending work.
func (v *validationRecord) observe(candidate Candidate) {
	reference := candidateReference(candidate)
	if reference.GenerationID == "" {
		return
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	v.candidate = reference
	if v.accepted.GenerationID == reference.GenerationID {
		v.state = RouteValidationAccepted
		return
	}
	v.state = RouteValidationPending
}

// accept records the candidate that became the accepted head.
func (v *validationRecord) accept(candidate Candidate) {
	reference := candidateReference(candidate)
	if reference.GenerationID == "" {
		return
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	v.candidate = reference
	v.accepted = reference
	v.state = RouteValidationAccepted
}

// reject records the candidate this instance refused, with the safe cause. The
// accepted head does not move, so the state names a refusal beside a head that
// still routes every request.
func (v *validationRecord) reject(candidate Candidate, failure error) {
	reference := candidateReference(candidate)
	if reference.GenerationID == "" {
		return
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	v.candidate = reference
	v.rejected = Rejection{
		Generation: reference,
		Reason:     ClassifyOperationFailure(failure),
		At:         v.clock()(),
	}
	v.state = RouteValidationRejected
}

// snapshot reads the four values the admin surface reports.
func (v *validationRecord) snapshot() RouteValidation {
	v.mu.Lock()
	defer v.mu.Unlock()
	state := v.state
	if state == "" {
		state = RouteValidationUnknown
	}
	return RouteValidation{
		State:     state,
		Candidate: v.candidate,
		Accepted:  v.accepted,
		Rejected:  v.rejected,
	}
}

// clock returns the time source. The caller holds the lock.
func (v *validationRecord) clock() func() time.Time {
	if v.now != nil {
		return v.now
	}
	return time.Now
}

// candidateReference names one candidate without carrying its catalog.
func candidateReference(candidate Candidate) GenerationRef {
	return GenerationRef{
		GenerationID:    candidate.State.GenerationID,
		PayloadChecksum: candidate.State.PayloadChecksum,
		GeneratedAt:     candidate.State.GeneratedAt,
		LeaseEpoch:      candidate.Epoch,
	}
}
