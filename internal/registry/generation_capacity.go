package registry

import (
	"errors"

	runtimecatalog "github.com/agentstation/starport/internal/catalog"
)

// maxRuntimeGenerations bounds current, draining, and prepared generations.
const maxRuntimeGenerations = 4

// reserveGeneration requires lifecycleMu so publication cannot change the count.
func (r *Registry) reserveGeneration() error {
	r.capacityMu.Lock()
	defer r.capacityMu.Unlock()
	count := r.preparedGenerations + len(r.drainingGenerations)
	if r.current.Load() != nil {
		count++
	}
	if count >= maxRuntimeGenerations {
		return runtimecatalog.ErrRuntimeGenerationCapacity
	}
	r.preparedGenerations++
	return nil
}

func (r *Registry) releaseReservation() {
	r.capacityMu.Lock()
	r.preparedGenerations--
	r.capacityMu.Unlock()
}

func (r *Registry) generationClosed(generation *runtimeGeneration, err error) {
	r.capacityMu.Lock()
	delete(r.drainingGenerations, generation)
	r.capacityMu.Unlock()
	r.recordDrainError(err)
}

func (c *Candidate) reserveFor(owner *Registry) error {
	if c == nil || c.generation == nil {
		return errors.New("runtime generation candidate is required")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.consumed {
		return errors.New("runtime generation candidate is already consumed")
	}
	if c.owner != nil {
		if c.owner != owner {
			return errors.New("runtime generation belongs to another registry")
		}
		return nil
	}
	if err := owner.reserveGeneration(); err != nil {
		return err
	}
	c.owner = owner
	return nil
}
