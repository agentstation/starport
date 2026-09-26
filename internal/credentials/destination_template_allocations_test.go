//go:build !race

package credentials

import (
	"net/http"
	"testing"

	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/stretchr/testify/require"
)

// Race instrumentation discards pooled regexp state. Native CI measures this
// allocation contract separately with the production build on every platform.
func TestDestinationTemplateAuthorizationHasNoAllocations(t *testing.T) {
	identity, material, _, _ := destinationFixture(t)
	grant, err := NewDestinationGrant(identity, material.Profile(), []Destination{{Operation: catalogs.ProviderOperationChatCompletions, Method: http.MethodPost, URL: "https://provider.example/v1/{model...}:generateContent", PathTemplate: true}})
	require.NoError(t, err)
	request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, "https://provider.example/v1/models/model-v2:generateContent", nil)
	require.NoError(t, err)
	allocations := testing.AllocsPerRun(100, func() {
		authorized, err := grant.Authorize(identity, material, catalogs.ProviderOperationChatCompletions, request)
		if err != nil {
			panic(err)
		}
		if err := authorized.Check(request); err != nil {
			panic(err)
		}
	})
	require.Zero(t, allocations)
}
