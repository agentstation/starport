package view

import (
	starmapcatalogs "github.com/agentstation/starmap/pkg/catalogs"

	runtimecatalog "github.com/agentstation/starport/internal/catalog"
)

// LogoKind selects the catalog namespace a logo belongs to.
type LogoKind string

const (
	// LogoKindProviders selects provider marks.
	LogoKindProviders LogoKind = "providers"
	// LogoKindAuthors selects author marks.
	LogoKindAuthors LogoKind = "authors"
)

// Logo projects the catalog-carried SVG brand mark for one provider or
// author. ok reports whether the snapshot's catalog carries bytes for
// this kind and ID; callers fall back to their own asset set when it is
// false.
func Logo(snapshot *runtimecatalog.RoutableSnapshot, kind LogoKind, id string) (svg []byte, ok bool) {
	if snapshot == nil {
		return nil, false
	}
	switch kind {
	case LogoKindProviders:
		provider, err := snapshot.Catalog().Provider(starmapcatalogs.ProviderID(id))
		if err != nil || len(provider.Logo) == 0 {
			return nil, false
		}
		return provider.Logo, true
	case LogoKindAuthors:
		author, err := snapshot.Catalog().Author(starmapcatalogs.AuthorID(id))
		if err != nil || len(author.Logo) == 0 {
			return nil, false
		}
		return author.Logo, true
	default:
		return nil, false
	}
}
