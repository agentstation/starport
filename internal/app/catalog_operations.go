package app

import (
	"context"

	"github.com/rs/zerolog/log"

	runtimecatalog "github.com/agentstation/starport/internal/catalog"
)

// CatalogMetadata reports the active snapshot's identity, age, and manifest
// facts for the catalog freshness endpoint.
func (a *App) CatalogMetadata(ctx context.Context) (runtimecatalog.SnapshotMetadata, error) {
	if a == nil || a.catalogFreshness == nil {
		return runtimecatalog.SnapshotMetadata{}, ErrCatalogRequired
	}
	return a.catalogFreshness.Metadata(ctx)
}

// CatalogChanges diffs the previous accepted generation against the current
// one.
func (a *App) CatalogChanges(ctx context.Context) (runtimecatalog.Diff, error) {
	if a == nil || a.catalogFreshness == nil {
		return runtimecatalog.Diff{}, ErrCatalogRequired
	}
	return a.catalogFreshness.Changes(ctx)
}

// RefreshCatalog forces one catalog acquisition and activation cycle, then
// reports the generation delta.
func (a *App) RefreshCatalog(ctx context.Context) (runtimecatalog.RefreshReport, error) {
	if a == nil || a.catalog == nil {
		return runtimecatalog.RefreshReport{}, ErrCatalogRequired
	}
	before := a.catalog.Current()
	if err := a.refreshRuntime(ctx); err != nil {
		return runtimecatalog.RefreshReport{}, err
	}
	after := a.catalog.Current()
	report := runtimecatalog.RefreshReport{
		PreviousGenerationID: before.GenerationID(),
		GenerationID:         after.GenerationID(),
		GeneratedAt:          after.GeneratedAt(),
		Changed: before.GenerationID() != after.GenerationID() ||
			before.PayloadChecksum() != after.PayloadChecksum(),
	}
	log.Info().
		Str("previous_generation_id", report.PreviousGenerationID).
		Str("generation_id", report.GenerationID).
		Bool("changed", report.Changed).
		Msg("catalog refresh completed")
	return report, nil
}
