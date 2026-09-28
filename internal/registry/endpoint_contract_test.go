package registry

import (
	"testing"

	"github.com/agentstation/starmap"
	"github.com/agentstation/starmap/pkg/catalogs"
	runtimecatalog "github.com/agentstation/starport/internal/catalog"
	"github.com/agentstation/starport/internal/credentials"
	"github.com/agentstation/starport/internal/providers/connectors"
	"github.com/stretchr/testify/require"
)

func TestRuntimeEndpointBindingAllocationBound(t *testing.T) {
	client, err := starmap.New()
	require.NoError(t, err)
	plane, err := runtimecatalog.Open(client)
	require.NoError(t, err)
	registry, err := Open(plane, []Registration{runtimeRegistration("openai", newCloseTrackingConnector(), registryTestMaterialSource{})})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, registry.Close()) })
	lease, err := registry.AcquireRuntime()
	require.NoError(t, err)
	defer lease.Release()
	binder := lease.(connectors.EndpointBinder)
	provider, err := lease.Snapshot().Catalog().Provider("openai")
	require.NoError(t, err)
	endpoint := catalogs.ProviderOfferingEndpoint{Operation: catalogs.ProviderOperationChatCompletions, URL: provider.Inference.BaseURL + "/chat/completions"}
	var material credentials.Material
	var bound catalogs.ProviderOfferingEndpoint
	direct := testing.AllocsPerRun(20, func() {
		bound, err = provider.Inference.BindOfferingEndpoint(endpoint, "https://operator.example/v1", material.EndpointBindings())
		if err != nil {
			t.Fatal(err)
		}
	})
	expected := bound
	leased := testing.AllocsPerRun(20, func() {
		bound, err = binder.BindEndpoint("openai", endpoint, material, true)
		if err != nil {
			t.Fatal(err)
		}
	})
	expected.URL = "https://provider.example/v1/chat/completions"
	require.Equal(t, expected, bound)
	t.Logf("endpoint allocations: direct=%.0f leased=%.0f", direct, leased)
	require.LessOrEqual(t, leased, direct+16, "binding must not copy the provider model inventory")
}

func TestRuntimeEndpointBindingRetainsGeneration(t *testing.T) {
	for _, construction := range []string{"open", "register"} {
		t.Run(construction, func(t *testing.T) {
			client, err := starmap.New()
			require.NoError(t, err)
			plane, err := runtimecatalog.Open(client)
			require.NoError(t, err)
			var registry *Registry
			if construction == "open" {
				registry, err = Open(plane, []Registration{runtimeRegistration("openai", newCloseTrackingConnector(), registryTestMaterialSource{})})
				require.NoError(t, err)
			} else {
				registry = NewEmptyWithCatalog(plane)
				require.NoError(t, registry.Register("openai", newCloseTrackingConnector()))
			}
			t.Cleanup(func() { require.NoError(t, registry.Close()) })
			oldLease, err := registry.AcquireRuntime()
			require.NoError(t, err)
			defer oldLease.Release()
			oldProvider, err := oldLease.Snapshot().Catalog().Provider("openai")
			require.NoError(t, err)
			oldURL := oldProvider.Inference.BaseURL + "/chat/completions"
			state := client.CurrentCatalogState()
			builder, err := catalogs.NewBuilderFrom(state.Catalog)
			require.NoError(t, err)
			oldProvider.Inference.BaseURL = "https://replacement.example/v2"
			require.NoError(t, builder.SetProvider(oldProvider))
			state.Catalog, err = builder.Build()
			require.NoError(t, err)
			state.GenerationID += "-replacement"
			state.Sequence++
			candidate, err := registry.Prepare([]Registration{runtimeRegistration("openai", newCloseTrackingConnector(), registryTestMaterialSource{})})
			require.NoError(t, err)
			snapshot, err := plane.ReplaceRuntime(state, candidate.Availability())
			require.NoError(t, err)
			require.NoError(t, registry.Publish(candidate, snapshot))
			newLease, err := registry.AcquireRuntime()
			require.NoError(t, err)
			defer newLease.Release()
			bind := func(lease connectors.RuntimeLease, url string, override bool) (catalogs.ProviderOfferingEndpoint, error) {
				return lease.(connectors.EndpointBinder).BindEndpoint("openai", catalogs.ProviderOfferingEndpoint{
					Operation: catalogs.ProviderOperationChatCompletions, URL: url,
				}, credentials.Material{}, override)
			}
			oldBound, err := bind(oldLease, oldURL, construction == "open")
			require.NoError(t, err)
			if construction == "open" {
				require.Equal(t, "https://provider.example/v1/chat/completions", oldBound.URL)
			} else {
				require.Equal(t, oldURL, oldBound.URL)
			}
			newURL := "https://replacement.example/v2/chat/completions"
			newBound, err := bind(newLease, newURL, true)
			require.NoError(t, err)
			require.Equal(t, "https://provider.example/v1/chat/completions", newBound.URL)
			_, err = bind(newLease, oldURL, true)
			require.Error(t, err, "the replacement must not bind through the previous inference contract")
			newBound, err = bind(newLease, newURL, false)
			require.NoError(t, err)
			require.Equal(t, newURL, newBound.URL, "account credentials must not inherit an operator override")
			_, err = newLease.(connectors.EndpointBinder).BindEndpoint("missing", newBound, credentials.Material{}, false)
			require.Error(t, err)
		})
	}
}
