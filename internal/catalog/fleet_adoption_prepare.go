package catalog

import (
	"context"
	"time"

	"github.com/agentstation/starmap/acquisition"
	"github.com/agentstation/starmap/runtime"
	"github.com/agentstation/starport/internal/recovery"
	"github.com/agentstation/starport/internal/storage"
)

// PrepareFleetAdoption validates and selects recovered catalog state while SQL
// approval remains closed. It never grants admission or releases import barriers.
// The recovery coordinator must reconcile history and activate every component
// before completing authority approval. All writers must remain fenced.
func PrepareFleetAdoption(ctx context.Context, backend storage.IncarnationProvider, witness *recovery.Witness, request FleetAdoptionRequest, settings Settings) error {
	if ctx == nil || backend == nil || witness == nil {
		return recovery.ErrClosed
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := request.validate(); err != nil {
		return err
	}
	if settings.DeploymentID != request.Closed.DeploymentID {
		return recovery.ErrConflict
	}
	options, closeSource, err := fleetAdoptionOptions(ctx, settings)
	if err != nil {
		return err
	}
	defer closeSource()
	return prepareFleetAdoption(ctx, backend, witness, request, options)
}

func prepareFleetAdoption(ctx context.Context, backend storage.IncarnationProvider, witness *recovery.Witness, request FleetAdoptionRequest, options []runtime.Option) error {
	if ctx == nil || backend == nil || witness == nil {
		return recovery.ErrClosed
	}
	if err := request.validate(); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	gate := &closedFleetWitness{witness: witness, expected: request.Closed}
	if err := gate.check(ctx); err != nil {
		return err
	}
	if _, err := selectFleetAdoption(ctx, backend, witness, request, options); err != nil {
		return err
	}
	return gate.check(ctx)
}

func fleetAdoptionOptions(ctx context.Context, settings Settings) ([]runtime.Option, func(), error) {
	options, err := settings.starmapOptions()
	if err != nil {
		return nil, nil, err
	}
	providers, err := acquisition.NewAcquirer()
	if err != nil {
		return nil, nil, err
	}
	metadata, err := settings.metadataCollector()
	if err != nil {
		return nil, nil, err
	}
	options = append(options, runtime.WithAcquirer(providers), runtime.WithSourceAcquirer(metadata))
	closeSource := func() {}
	if settings.Source == string(runtime.SourceStarmap) {
		source, err := settings.cascadeSource(ctx)
		if err != nil {
			return nil, nil, err
		}
		closeSource = func() { _ = source.Close() }
		options = append(options, runtime.WithSource(source))
	}
	return options, closeSource, nil
}
