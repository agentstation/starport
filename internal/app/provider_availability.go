package app

import (
	"errors"

	"github.com/agentstation/starport/internal/availability"
	runtimecatalog "github.com/agentstation/starport/internal/catalog"
	"github.com/agentstation/starport/internal/providerstate"
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
	return errors.Join(errs...)
}
