package app

import (
	"errors"

	"github.com/agentstation/starport/internal/availability"
	runtimecatalog "github.com/agentstation/starport/internal/catalog"
	providerstate "github.com/agentstation/starport/internal/providers/state"
)

type providerAvailabilityPublisher struct {
	catalog *runtimecatalog.ControlPlane
	states  *providerstate.Store
}

func (p providerAvailabilityPublisher) PublishAvailability(snapshot availability.Snapshot) error {
	var errs []error
	if p.catalog != nil {
		if err := p.catalog.PublishAvailability(snapshot); err != nil {
			errs = append(errs, err)
		}
	}
	if p.states != nil {
		if err := p.states.PublishAvailability(snapshot); err != nil {
			errs = append(errs, err)
		}
	}
	// Withholding an offering removes its route, so the planning verdicts the
	// operator reads must move with the availability generation that caused it.
	if p.catalog != nil && p.states != nil {
		if err := publishRouting(p.states, p.catalog.Current()); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
