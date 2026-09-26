package credentials

import (
	"net/http"
	"testing"

	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/stretchr/testify/require"
)

func TestDestinationTemplateApprovesModelPaths(t *testing.T) {
	identity, material, _, _ := destinationFixture(t)
	grant, err := NewDestinationGrant(identity, material.Profile(), []Destination{{Operation: catalogs.ProviderOperationChatCompletions, Method: http.MethodPost, URL: "https://provider.example/v1/{model...}:generateContent", PathTemplate: true}})
	require.NoError(t, err)
	for _, model := range []string{"gemini-2.5", "models/gemini-2.5", "organization/model.v2", "model+variant"} {
		request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, "https://provider.example/v1/"+model+":generateContent", nil)
		require.NoError(t, err)
		authorized, err := grant.Authorize(identity, material, catalogs.ProviderOperationChatCompletions, request)
		require.NoError(t, err, model)
		require.NoError(t, authorized.Check(request))
		changed := request.Clone(t.Context())
		changed.URL.Path = "/v1/another:generateContent"
		changed.URL.RawPath = ""
		require.ErrorIs(t, authorized.Check(changed), ErrDestinationUnapproved)
	}
}

func TestDestinationTemplateRefusesEscapes(t *testing.T) {
	identity, material, _, _ := destinationFixture(t)
	grant, err := NewDestinationGrant(identity, material.Profile(), []Destination{{Operation: catalogs.ProviderOperationChatCompletions, Method: http.MethodPost, URL: "https://provider.example/v1/{model...}:generateContent?version=1", PathTemplate: true}})
	require.NoError(t, err)
	for _, target := range []string{
		"https://other.example/v1/model:generateContent?version=1",
		"http://provider.example/v1/model:generateContent?version=1",
		"https://provider.example:8443/v1/model:generateContent?version=1",
		"https://provider.example/v1/model:generateContent?version=2",
		"https://provider.example/v1/model:generateContent?version=1#other",
		"https://provider.example/v1/:generateContent?version=1",
		"https://provider.example/v1/model:streamGenerateContent?version=1",
		"https://provider.example/v1/../admin:generateContent?version=1",
		"https://provider.example/v1/%2e%2e/admin:generateContent?version=1",
		"https://provider.example/v1/%252e%252e/admin:generateContent?version=1",
		"https://provider.example/v1/model%2f..%2fadmin:generateContent?version=1",
		"https://provider.example/v1/model%5cadmin:generateContent?version=1",
		"https://provider.example/v1/model//admin:generateContent?version=1",
		"https://provider.example/v1/model%3fadmin:generateContent?version=1",
		"https://provider.example/v1/model%23admin:generateContent?version=1",
		"https://provider.example/v1/model%00admin:generateContent?version=1",
		"https://provider.example/v1/model%0d%0aadmin:generateContent?version=1",
		"https://provider.example/v1/model%20admin:generateContent?version=1",
	} {
		t.Run(target, func(t *testing.T) {
			request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, target, nil)
			require.NoError(t, err)
			_, err = grant.Authorize(identity, material, catalogs.ProviderOperationChatCompletions, request)
			require.ErrorIs(t, err, ErrDestinationUnapproved)
		})
	}
}

func TestDestinationTemplateRejectsMalformedApproval(t *testing.T) {
	identity, material, _, _ := destinationFixture(t)
	for _, target := range []string{
		"https://provider.example/v1/exact", "https://provider.example/v1/{model", "https://provider.example/v1/model}",
		"https://provider.example/v1/{}", "https://provider.example/v1/{Model}", "https://provider.example/v1/{model-name}",
		"https://provider.example/v1/{model}{other}", "https://provider.example/v1/{model}/{model}",
		"https://provider.example/v1/{model}?selector={query}", "https://provider.example/v1/../{model}",
	} {
		_, err := NewDestinationGrant(identity, material.Profile(), []Destination{{Operation: catalogs.ProviderOperationChatCompletions, Method: http.MethodPost, URL: target, PathTemplate: true}})
		require.ErrorIs(t, err, ErrDestinationUnapproved, target)
	}
}

func TestDestinationTemplateBindsJobMethodAndTarget(t *testing.T) {
	identity, material, _, _ := destinationFixture(t)
	grant, err := NewDestinationGrant(identity, material.Profile(), []Destination{
		{Operation: catalogs.ProviderOperationVideosGenerations, Method: http.MethodGet, URL: "https://provider.example/v1/jobs/{job}/content", PathTemplate: true},
		{Operation: catalogs.ProviderOperationVideosGenerations, Method: http.MethodDelete, URL: "https://provider.example/v1/jobs/{job}", PathTemplate: true},
	})
	require.NoError(t, err)
	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "https://provider.example/v1/jobs/job-123/content", nil)
	require.NoError(t, err)
	authorization, err := grant.Authorize(identity, material, catalogs.ProviderOperationVideosGenerations, request)
	require.NoError(t, err)
	final, err := authorization.AfterPlacement(request)
	require.NoError(t, err)
	changed := request.Clone(t.Context())
	changed.URL.Path = "/v1/jobs/job-456/content"
	require.ErrorIs(t, final.Check(changed), ErrDestinationUnapproved)
	changed = request.Clone(t.Context())
	changed.Method = http.MethodPost
	_, err = grant.Authorize(identity, material, catalogs.ProviderOperationVideosGenerations, changed)
	require.ErrorIs(t, err, ErrDestinationUnapproved)
	changed = request.Clone(t.Context())
	changed.URL.Path = "/v1/jobs/job-123/other/content"
	_, err = grant.Authorize(identity, material, catalogs.ProviderOperationVideosGenerations, changed)
	require.ErrorIs(t, err, ErrDestinationUnapproved)
	grant.Revoke()
	require.ErrorIs(t, final.Check(request), ErrDestinationUnapproved)
}
