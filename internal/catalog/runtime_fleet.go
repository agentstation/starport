package catalog

import (
	"context"
	"errors"

	"github.com/agentstation/starmap"
	"github.com/agentstation/starport/internal/recovery"
	"github.com/agentstation/starport/internal/sqlstore"
	"github.com/agentstation/starport/internal/storage"
)

func (r *Runtime) candidateFromState(ctx context.Context, state starmap.CatalogState) (Candidate, error) {
	candidate := Candidate{State: state}
	if r.fleet == nil {
		epoch, err := r.leases.CurrentEpoch(ctx)
		candidate.Epoch = epoch
		return candidate, err
	}
	status, ok := r.runtime.FleetStatus()
	if !ok || status.Head.GenerationID != state.GenerationID {
		return Candidate{}, fleetStoreConflict("the runtime state differs from its durable fleet publication")
	}
	snapshot, err := r.fleet.Publication(ctx, status.Head)
	if err != nil {
		return Candidate{}, err
	}
	if snapshot.Publication.Generation.Manifest.Payload.Checksum != state.PayloadChecksum {
		return Candidate{}, fleetStoreConflict("the candidate bytes differ from its original publication")
	}
	candidate.FleetHead = status.Head
	candidate.Epoch = snapshot.Publication.Grant.Epoch
	return candidate, nil
}

func (r *Runtime) acceptFleetCandidate(ctx context.Context, candidate Candidate) error {
	if candidate.FleetHead.GenerationID != candidate.State.GenerationID {
		return fleetStoreConflict("the route candidate has no matching fleet publication")
	}
	current, _, err := r.fleet.readAcceptance(ctx)
	if err != nil && !errors.Is(err, storage.ErrNotFound) {
		return err
	}
	if err := r.fleet.AcceptPublication(ctx, candidate.FleetHead, current.Head); err != nil {
		return err
	}
	r.validation.accept(candidate)
	return nil
}

// AcceptedStore exposes the runtime's exact storage selection to diagnostics and history.
func (r *Runtime) AcceptedStore() *GenerationStore {
	if r == nil {
		return nil
	}
	return r.accepted
}

// RefreshFleet recovers a missed shared head without source or provider acquisition.
func (r *Runtime) RefreshFleet(ctx context.Context) error {
	if r == nil || r.runtime == nil || r.fleet == nil {
		return ErrCatalogSourceRequired
	}
	return r.runtime.RefreshFleet(ctx)
}

func openRecoveryFleet(ctx context.Context, store storage.KVStore, db *sqlstore.DB, deployment string) (*FleetStore, error) {
	provider, ok := store.(storage.IncarnationProvider)
	if !ok {
		return nil, nil
	}
	if db == nil || db.Dialect() != sqlstore.TypePostgres {
		return nil, errors.New("shared catalog recovery requires the qualified PostgreSQL witness")
	}
	witness, err := recovery.New(db)
	if err != nil {
		return nil, err
	}
	return NewFleetStore(ctx, provider, witness, deployment)
}

// OpenAcceptedStore selects the accepted head without starting a catalog runtime.
// Shared storage requires independent recovery approval. This function never creates it.
func OpenAcceptedStore(ctx context.Context, store storage.KVStore, db *sqlstore.DB, deployment string) (*GenerationStore, error) {
	fleet, err := openRecoveryFleet(ctx, store, db, deployment)
	if err != nil {
		return nil, err
	}
	accepted, err := NewGenerationStore(store)
	if err != nil {
		return nil, err
	}
	accepted.fleet, accepted.fleetAccepted = fleet, fleet != nil
	return accepted, nil
}
