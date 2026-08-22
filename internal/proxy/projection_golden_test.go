package proxy

import (
	"context"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/agentstation/starmap"
	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/stretchr/testify/require"

	runtimecatalog "github.com/agentstation/starport/internal/catalog"
)

var updateProjectionGolden = flag.Bool(
	"update-projection-golden",
	false,
	"rewrite catalog projection golden files",
)

// goldenProviders pins the fixture surface: two providers with different
// credential contracts and pricing shapes. The golden bytes change only
// when a projection rule or the embedded Starmap catalog changes; on a
// catalog bump, regenerate with -update-projection-golden and review the
// diff as the projection's observable change.
var goldenProviders = []catalogs.ProviderID{"anthropic", "groq"}

func goldenSnapshot(t *testing.T) *runtimecatalog.RoutableSnapshot {
	t.Helper()
	client, err := starmap.New()
	require.NoError(t, err)
	plane, err := runtimecatalog.Open(client)
	require.NoError(t, err)
	for _, providerID := range goldenProviders {
		offerings, err := client.Catalog().ProviderOfferings(providerID)
		require.NoError(t, err)
		require.NotEmpty(t, offerings)
		operations := make(map[catalogs.ProviderOperation]struct{})
		endpointTypes := make(map[catalogs.EndpointType]struct{})
		for _, offering := range offerings {
			for _, operation := range offering.Service.Operations {
				operations[operation] = struct{}{}
			}
			for _, endpoint := range offering.Endpoints {
				endpointTypes[endpoint.Type] = struct{}{}
			}
		}
		availability := runtimecatalog.AdapterAvailability{
			ProviderID: providerID,
			Registered: true,
		}
		for operation := range operations {
			availability.Operations = append(availability.Operations, operation)
		}
		sort.Slice(availability.Operations, func(i, j int) bool {
			return availability.Operations[i] < availability.Operations[j]
		})
		for endpointType := range endpointTypes {
			availability.EndpointTypes = append(availability.EndpointTypes, endpointType)
		}
		sort.Slice(availability.EndpointTypes, func(i, j int) bool {
			return availability.EndpointTypes[i] < availability.EndpointTypes[j]
		})
		require.NoError(t, plane.SetAdapter(availability))
	}
	snapshot := plane.Current()
	require.NotNil(t, snapshot)
	return snapshot
}

func assertProjectionGolden(t *testing.T, name string, value any) {
	t.Helper()
	encoded, err := json.MarshalIndent(value, "", "  ")
	require.NoError(t, err)
	encoded = append(encoded, '\n')
	path := filepath.Join("testdata", name)
	if *updateProjectionGolden {
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, encoded, 0o644))
	}
	expected, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, string(expected), string(encoded))
}

func TestModelsProjectionGolden(t *testing.T) {
	snapshot := goldenSnapshot(t)
	response := modelsResponseFromSnapshot(snapshot)
	require.NotEmpty(t, response.Data)
	assertProjectionGolden(t, "projection_models.golden.json", response)
}

func TestProvidersProjectionGolden(t *testing.T) {
	snapshot := goldenSnapshot(t)
	runtime := &catalogDiscoveryRuntime{snapshot: snapshot}
	providers := providerInfosFromRuntime(runtime)
	require.Len(t, providers, len(goldenProviders))
	assertProjectionGolden(t, "projection_providers.golden.json", providers)
}

func TestEndpointsProjectionGolden(t *testing.T) {
	snapshot := goldenSnapshot(t)
	runtime := &catalogDiscoveryRuntime{snapshot: snapshot}
	service := &proxy{registry: catalogDiscoveryRegistry{runtime: runtime}}
	models := modelsResponseFromSnapshot(snapshot)
	require.NotEmpty(t, models.Data)
	endpoints, err := service.GetModelEndpoints(context.Background(), models.Data[0].ID)
	require.NoError(t, err)
	require.NotEmpty(t, endpoints.Endpoints)
	assertProjectionGolden(t, "projection_endpoints.golden.json", endpoints)
}
