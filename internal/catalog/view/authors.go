package view

import (
	"sort"

	starmapcatalogs "github.com/agentstation/starmap/pkg/catalogs"
	runtimecatalog "github.com/agentstation/starport/internal/catalog"
)

// AuthorInfo represents one catalog author or organization.
type AuthorInfo struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Description  string   `json:"description,omitempty"`
	Headquarters string   `json:"headquarters,omitempty"`
	IconURL      string   `json:"icon_url,omitempty"`
	Website      string   `json:"website,omitempty"`
	GitHub       string   `json:"github,omitempty"`
	HuggingFace  string   `json:"huggingface,omitempty"`
	Twitter      string   `json:"twitter,omitempty"`
	Models       []string `json:"models"`
}

// Authors projects every catalog author sorted by ID. A nil snapshot
// projects to nil.
func Authors(snapshot *runtimecatalog.RoutableSnapshot) []AuthorInfo {
	if snapshot == nil {
		return nil
	}
	catalog := snapshot.Catalog()
	if catalog == nil {
		return nil
	}
	listed := catalog.Authors().List()
	authors := make([]AuthorInfo, 0, len(listed))
	for _, author := range listed {
		authors = append(authors, authorInfo(catalog, author))
	}
	sort.Slice(authors, func(i, j int) bool { return authors[i].ID < authors[j].ID })
	return authors
}

// AuthorByID projects one catalog author. The second result reports
// whether the author exists.
func AuthorByID(snapshot *runtimecatalog.RoutableSnapshot, id string) (AuthorInfo, bool) {
	if snapshot == nil {
		return AuthorInfo{}, false
	}
	catalog := snapshot.Catalog()
	if catalog == nil {
		return AuthorInfo{}, false
	}
	author, err := catalog.Author(starmapcatalogs.AuthorID(id))
	if err != nil {
		return AuthorInfo{}, false
	}
	return authorInfo(catalog, author), true
}

func authorInfo(catalog *starmapcatalogs.Catalog, author starmapcatalogs.Author) AuthorInfo {
	info := AuthorInfo{
		ID:     string(author.ID),
		Name:   author.Name,
		Models: authorModelIDs(catalog, author.ID),
	}
	if author.Description != nil {
		info.Description = *author.Description
	}
	if author.Headquarters != nil {
		info.Headquarters = *author.Headquarters
	}
	if author.IconURL != nil {
		info.IconURL = *author.IconURL
	}
	if author.Website != nil {
		info.Website = *author.Website
	}
	if author.GitHub != nil {
		info.GitHub = *author.GitHub
	}
	if author.HuggingFace != nil {
		info.HuggingFace = *author.HuggingFace
	}
	if author.Twitter != nil {
		info.Twitter = *author.Twitter
	}
	return info
}

func authorModelIDs(catalog *starmapcatalogs.Catalog, authorID starmapcatalogs.AuthorID) []string {
	definitions, err := catalog.AuthorModels(authorID)
	if err != nil {
		return []string{}
	}
	ids := make([]string, 0, len(definitions))
	for _, definition := range definitions {
		ids = append(ids, string(definition.ID))
	}
	sort.Strings(ids)
	return ids
}
