package view

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAuthorsNilSnapshot(t *testing.T) {
	require.Nil(t, Authors(nil))
	_, ok := AuthorByID(nil, "anthropic")
	require.False(t, ok)
}

func TestAuthorsProjectionContract(t *testing.T) {
	snapshot := fixtureSnapshot(t, "anthropic")
	authors := Authors(snapshot)
	require.NotEmpty(t, authors)

	ids := make([]string, 0, len(authors))
	var anthropic *AuthorInfo
	for index := range authors {
		require.NotEmpty(t, authors[index].ID)
		require.NotEmpty(t, authors[index].Name)
		ids = append(ids, authors[index].ID)
		if authors[index].ID == "anthropic" {
			anthropic = &authors[index]
		}
	}
	require.IsIncreasing(t, ids, "authors must sort by id")
	require.NotNil(t, anthropic, "catalog must list the anthropic author")
	require.Equal(t, "Anthropic", anthropic.Name)
	require.NotEmpty(t, anthropic.Models, "author models must be listed")
	require.IsIncreasing(t, anthropic.Models, "author models must sort")
}

func TestAuthorByIDUnknown(t *testing.T) {
	snapshot := fixtureSnapshot(t, "anthropic")
	_, ok := AuthorByID(snapshot, "no-such-author")
	require.False(t, ok)

	author, ok := AuthorByID(snapshot, "anthropic")
	require.True(t, ok)
	require.Equal(t, "anthropic", author.ID)
	require.Equal(t, "Anthropic", author.Name)
}
