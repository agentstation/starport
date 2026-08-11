package catalog

import (
	"context"
	"fmt"
	"strings"

	"github.com/agentstation/starmap"
	"github.com/agentstation/starmap/acquisition"
	"github.com/agentstation/starmap/pkg/sources"
	pkgsync "github.com/agentstation/starmap/pkg/sync"

	"github.com/agentstation/starport/internal/storage"
)

type acquisitionSyncer interface {
	Sync(context.Context, ...pkgsync.Option) (*pkgsync.Result, error)
	PublishObservations(context.Context, ...sources.Observation) (starmap.Publication, error)
}

// Runtime owns one Starmap client, its acquisition path, and Starport's
// derived immutable routable control plane.
type Runtime struct {
	client      *starmap.Client
	acquisition acquisitionSyncer
	control     *ControlPlane
}

// OpenRuntime constructs the catalog runtime over Starport's durable storage.
// Starmap alone resolves catalog-acquisition credentials.
func OpenRuntime(
	ctx context.Context,
	store storage.KVStore,
	workspacePath string,
	options ...acquisition.Option,
) (*Runtime, error) {
	generationStore, err := NewGenerationStore(store)
	if err != nil {
		return nil, err
	}
	starmapOptions := []starmap.Option{starmap.WithCatalogStore(generationStore)}
	if path := strings.TrimSpace(workspacePath); path != "" {
		starmapOptions = append(starmapOptions, starmap.WithCatalogPath(path))
	}
	client, err := starmap.NewContext(ctx, starmapOptions...)
	if err != nil {
		return nil, fmt.Errorf("open Starmap client: %w", err)
	}
	syncer, err := acquisition.New(client, options...)
	if err != nil {
		return nil, fmt.Errorf("open Starmap acquisition: %w", err)
	}
	return newRuntime(client, syncer)
}

func newRuntime(client *starmap.Client, syncer acquisitionSyncer) (*Runtime, error) {
	if client == nil {
		return nil, ErrCatalogSourceRequired
	}
	if syncer == nil {
		return nil, fmt.Errorf("catalog acquisition is required")
	}
	control, err := Open(client)
	if err != nil {
		return nil, err
	}
	return &Runtime{client: client, acquisition: syncer, control: control}, nil
}

// ControlPlane returns Starport's generation-consistent catalog projection.
func (r *Runtime) ControlPlane() *ControlPlane {
	if r == nil {
		return nil
	}
	return r.control
}

// Refresh runs Starmap acquisition and publishes any new generation into the
// Starport routable view.
func (r *Runtime) Refresh(ctx context.Context, options ...pkgsync.Option) (*pkgsync.Result, error) {
	result, state, err := r.Sync(ctx, options...)
	if err != nil {
		return nil, err
	}
	if err := r.control.Activate(state); err != nil {
		return nil, fmt.Errorf("activate acquired Starmap generation: %w", err)
	}
	return result, nil
}

// Sync runs Starmap acquisition and returns the complete unpublished catalog
// state for runtime-candidate construction.
func (r *Runtime) Sync(
	ctx context.Context,
	options ...pkgsync.Option,
) (*pkgsync.Result, starmap.CatalogState, error) {
	if r == nil || r.acquisition == nil || r.control == nil {
		return nil, starmap.CatalogState{}, ErrCatalogSourceRequired
	}
	result, err := r.acquisition.Sync(ctx, options...)
	if err != nil {
		return nil, starmap.CatalogState{}, err
	}
	state := r.client.CurrentCatalogState()
	if state.Catalog == nil || strings.TrimSpace(state.GenerationID) == "" {
		return nil, starmap.CatalogState{}, ErrCatalogRequired
	}
	return result, state, nil
}

// PublishObservations reconciles tenant or operator observations through
// Starmap, then activates the resulting immutable generation.
func (r *Runtime) PublishObservations(
	ctx context.Context,
	observations ...sources.Observation,
) (starmap.Publication, error) {
	if r == nil || r.acquisition == nil || r.control == nil {
		return starmap.Publication{}, ErrCatalogSourceRequired
	}
	publication, err := r.acquisition.PublishObservations(ctx, observations...)
	if err != nil {
		return starmap.Publication{}, err
	}
	if err := r.control.Refresh(); err != nil {
		return starmap.Publication{}, fmt.Errorf("activate observed Starmap generation: %w", err)
	}
	return publication, nil
}
