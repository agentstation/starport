package app

import (
	"context"

	"github.com/agentstation/starport/internal/audit"
	runtimecatalog "github.com/agentstation/starport/internal/catalog"
	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/configops"
)

// configurationOperations composes the configuration admin surface. A local
// save records its audit entry on the relational trail. A shared save uses the
// revision store that startup opened, and its audit entry commits with it.
func (b *runtimeBuilder) configurationOperations() (*configops.Service, error) {
	options := configops.Options{Store: b.application.configuration, Probe: probeCatalogSource}
	if b.application.configuration != nil {
		options.Observe = b.application.observeConfigurationRevision
	}
	if trail := b.audit; trail != nil {
		options.LocalAudit = func(ctx context.Context, actor, action, subject string) error {
			return trail.Record(ctx, audit.Record{Actor: actor, Action: action, Subject: subject, Outcome: audit.OutcomeOK})
		}
	}
	return configops.New(b.config, options)
}

// probeCatalogSource tests the source of catalog settings. It reads only the
// source selection and its credentials.
func probeCatalogSource(ctx context.Context, catalog config.CatalogConfig) error {
	return runtimecatalog.ProbeSource(ctx, runtimecatalog.Settings{
		Source: catalog.Source, SourceURL: catalog.SourceURL, SourceAPIKey: catalog.SourceAPIKey,
		SourceRepository: catalog.SourceRepository, SourceToken: catalog.SourceToken,
	})
}
