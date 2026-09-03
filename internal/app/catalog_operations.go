package app

import (
	"context"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/agentstation/starmap"

	runtimecatalog "github.com/agentstation/starport/internal/catalog"
)

// catalogSnapshotMetadata reports the manifest detail of the accepted head.
// A gateway that accepted nothing yet reports the empty record, because the
// admin status states an absent catalog through its own values.
func (a *App) catalogSnapshotMetadata(ctx context.Context) runtimecatalog.SnapshotMetadata {
	if a == nil || a.catalogFreshness == nil {
		return runtimecatalog.SnapshotMetadata{}
	}
	metadata, err := a.catalogFreshness.Metadata(ctx)
	if err != nil {
		log.Warn().
			Str("reason", string(runtimecatalog.ClassifyOperationFailure(err))).
			Msg("the catalog snapshot metadata is unavailable")
		return runtimecatalog.SnapshotMetadata{}
	}
	return metadata
}

// CatalogChanges diffs the previous accepted generation against the current
// one.
func (a *App) CatalogChanges(ctx context.Context) (runtimecatalog.Diff, error) {
	if a == nil || a.catalogFreshness == nil {
		return runtimecatalog.Diff{}, ErrCatalogRequired
	}
	return a.catalogFreshness.Changes(ctx)
}

// CatalogSummary reports the allowlisted catalog view a reader receives. It is
// the admin status passed through the reader projection, so the two surfaces
// cannot drift: one status, one allowlist.
func (a *App) CatalogSummary(ctx context.Context) (runtimecatalog.Summary, error) {
	status, err := a.CatalogStatus(ctx)
	if err != nil {
		return runtimecatalog.Summary{}, err
	}
	return status.Summary(), nil
}

// CatalogStatus reports the operator view of the catalog runtime.
func (a *App) CatalogStatus(ctx context.Context) (runtimecatalog.AdminStatus, error) {
	if a == nil || a.catalogRuntime == nil {
		return runtimecatalog.AdminStatus{}, ErrCatalogRequired
	}
	status := a.catalogRuntime.Status()
	return runtimecatalog.NewAdminStatus(
		status,
		a.catalogRuntime.RouteValidation(),
		a.config.Catalog.AcquisitionEnabled,
		a.catalogCounts(),
		a.catalogSnapshotMetadata(ctx),
		nextSourceRead(status, a.config.Catalog.SourcePollInterval),
		a.catalogOperations.List(),
	), nil
}

// catalogCounts reads how much the accepted head holds. A gateway that
// accepted nothing yet reports zero of each, which is a state and not a
// failure.
func (a *App) catalogCounts() runtimecatalog.Counts {
	if a == nil || a.catalog == nil {
		return runtimecatalog.Counts{}
	}
	snapshot := a.catalog.Current()
	if snapshot == nil {
		return runtimecatalog.Counts{}
	}
	counts := runtimecatalog.Counts{Models: len(snapshot.Definitions())}
	if catalog := snapshot.Catalog(); catalog != nil {
		counts.Providers = len(catalog.Providers().List())
	}
	return counts
}

// nextSourceRead states when this instance next reads its source. A deployment
// with no poll interval reads only when an operator asks, and it reports no
// next read at all.
func nextSourceRead(status starmap.RuntimeStatus, interval time.Duration) time.Time {
	if interval <= 0 || status.ObservedAt.IsZero() {
		return time.Time{}
	}
	remaining := interval - status.SourceCheckAge
	if remaining < 0 {
		remaining = 0
	}
	return status.ObservedAt.Add(remaining)
}

// StartCatalogRefresh accepts one catalog refresh and returns the operation
// that carries it. The second value reports that the request joined the run in
// flight rather than starting a second one.
//
// The work outlives the request. An operator asked for the refresh, not for
// the response, so a client that disconnects does not end the run.
func (a *App) StartCatalogRefresh(context.Context) (runtimecatalog.Operation, bool, error) {
	if a == nil || a.catalogRuntime == nil || a.catalogOperations == nil {
		return runtimecatalog.Operation{}, false, ErrCatalogRequired
	}
	operation, joined := a.catalogOperations.Submit(
		runtimecatalog.KindCatalogUpdate,
		a.runCatalogUpdate,
	)
	if operation.ID == "" {
		return runtimecatalog.Operation{}, false, ErrCatalogRequired
	}
	return operation, joined, nil
}

// runCatalogUpdate performs one catalog update: read the source, observe the
// providers, validate the candidate, and advance the accepted head.
func (a *App) runCatalogUpdate(ctx context.Context) (runtimecatalog.OperationResult, error) {
	before := a.catalog.Current()
	candidate, err := a.syncCatalog(ctx)
	if err != nil {
		return runtimecatalog.OperationResult{}, err
	}
	if err := a.activateRuntimeState(ctx, candidate); err != nil {
		return runtimecatalog.OperationResult{}, err
	}
	after := a.catalog.Current()
	result := runtimecatalog.OperationResult{GenerationID: after.GenerationID()}
	result.Changed = before == nil ||
		before.GenerationID() != after.GenerationID() ||
		before.PayloadChecksum() != after.PayloadChecksum()
	return result, nil
}

// CatalogOperation reports one catalog operation by identifier.
func (a *App) CatalogOperation(_ context.Context, id string) (runtimecatalog.Operation, error) {
	if a == nil || a.catalogOperations == nil {
		return runtimecatalog.Operation{}, ErrCatalogRequired
	}
	return a.catalogOperations.Get(id)
}

// CancelCatalogOperation ends one open catalog operation.
func (a *App) CancelCatalogOperation(
	_ context.Context,
	id string,
) (runtimecatalog.Operation, error) {
	if a == nil || a.catalogOperations == nil {
		return runtimecatalog.Operation{}, ErrCatalogRequired
	}
	operation, err := a.catalogOperations.Cancel(id)
	if err != nil {
		return runtimecatalog.Operation{}, err
	}
	log.Info().
		Str("operation_id", operation.ID).
		Str("kind", string(operation.Kind)).
		Str("state", string(operation.State)).
		Msg("catalog operation canceled")
	return operation, nil
}
