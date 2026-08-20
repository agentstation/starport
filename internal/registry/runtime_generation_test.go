package registry

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/agentstation/starmap"
	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/stretchr/testify/require"

	runtimecatalog "github.com/agentstation/starport/internal/catalog"
	"github.com/agentstation/starport/internal/credentials"
)

func TestRuntimeGenerationRejectsInvalidCandidates(t *testing.T) {
	client, err := starmap.New()
	require.NoError(t, err)
	plane, err := runtimecatalog.Open(client)
	require.NoError(t, err)
	oldConnector := newCloseTrackingConnector()
	registry, err := Open(plane, []Registration{runtimeRegistration("openai", oldConnector, registryTestMaterialSource{})})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, registry.Close()) })
	before := registry.Snapshot()
	require.NotNil(t, before)

	newConnector := newCloseTrackingConnector()
	candidate, err := registry.Prepare([]Registration{
		runtimeRegistration("openai", newConnector, registryTestMaterialSource{}),
	})
	require.NoError(t, err)
	invalid := client.CurrentCatalogState()
	invalid.GenerationID = ""
	err = plane.ValidateRuntime(invalid, candidate.Availability())
	require.ErrorIs(t, err, runtimecatalog.ErrCatalogGenerationRequired)
	require.NoError(t, candidate.Close())

	require.Same(t, before, registry.Snapshot())
	current, err := registry.Get("openai")
	require.NoError(t, err)
	require.Same(t, oldConnector, current)
	require.Equal(t, int32(0), oldConnector.closeCount.Load())
	require.Equal(t, int32(1), newConnector.closeCount.Load())
}

func TestInvalidRuntimeCandidateRetainsCacheIdentity(t *testing.T) {
	client, err := starmap.New()
	require.NoError(t, err)
	plane, err := runtimecatalog.Open(client)
	require.NoError(t, err)
	oldConnector := newCloseTrackingConnector()
	registry, err := Open(plane, []Registration{
		runtimeRegistration("openai", oldConnector, registryTestMaterialSource{}),
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, registry.Close()) })
	before := registry.Snapshot()
	require.NotNil(t, before)

	newConnector := newCloseTrackingConnector()
	candidate, err := registry.Prepare([]Registration{
		runtimeRegistration("openai", newConnector, registryTestMaterialSource{}),
	})
	require.NoError(t, err)
	invalid := client.CurrentCatalogState()
	invalid.GenerationID = ""
	require.ErrorIs(
		t,
		plane.ValidateRuntime(invalid, candidate.Availability()),
		runtimecatalog.ErrCatalogGenerationRequired,
	)
	require.NoError(t, candidate.Close())

	after := registry.Snapshot()
	require.Same(t, before, after)
	require.Equal(t, before.GenerationID(), after.GenerationID())
	require.Same(t, oldConnector, mustRuntimeConnector(t, registry, "openai"))
}

func TestRuntimeGenerationDrainsConnectors(t *testing.T) {
	client, err := starmap.New()
	require.NoError(t, err)
	plane, err := runtimecatalog.Open(client)
	require.NoError(t, err)
	oldConnector := newCloseTrackingConnector()
	registry, err := Open(plane, []Registration{runtimeRegistration("openai", oldConnector, registryTestMaterialSource{})})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, registry.Close()) })
	oldLease, err := registry.AcquireRuntime()
	require.NoError(t, err)
	oldGenerationID := oldLease.Snapshot().GenerationID()

	newConnector := newCloseTrackingConnector()
	candidate, err := registry.Prepare([]Registration{
		runtimeRegistration("openai", newConnector, registryTestMaterialSource{}),
	})
	require.NoError(t, err)
	state := client.CurrentCatalogState()
	state.GenerationID += "-replacement"
	state.Sequence++
	require.NoError(t, plane.ValidateRuntime(state, candidate.Availability()))
	snapshot, err := plane.ReplaceRuntime(state, candidate.Availability())
	require.NoError(t, err)
	require.Equal(t, oldGenerationID, oldLease.Snapshot().GenerationID())
	require.Equal(t, oldGenerationID, registry.Snapshot().GenerationID())
	require.NoError(t, registry.Publish(candidate, snapshot))
	require.Equal(t, state.GenerationID, registry.Snapshot().GenerationID())

	newLease, err := registry.AcquireRuntime()
	require.NoError(t, err)
	require.Equal(t, state.GenerationID, newLease.Snapshot().GenerationID())
	require.Same(t, oldConnector, oldLease.Get("openai"))
	require.Same(t, newConnector, newLease.Get("openai"))
	require.Equal(t, int32(0), oldConnector.closeCount.Load())

	oldLease.Release()
	require.Equal(t, int32(1), oldConnector.closeCount.Load())
	require.Equal(t, int32(0), newConnector.closeCount.Load())
	newLease.Release()
}

func mustRuntimeConnector(t *testing.T, registry *Registry, provider string) any {
	t.Helper()
	connector, err := registry.Get(provider)
	require.NoError(t, err)
	return connector
}

func TestCredentialRotationDoesNotReplaceRuntimeGeneration(t *testing.T) {
	client, err := starmap.New()
	require.NoError(t, err)
	plane, err := runtimecatalog.Open(client)
	require.NoError(t, err)
	source := &rotatingRegistryMaterialSource{}
	source.value.Store("first")
	connector := newCloseTrackingConnector()
	registry, err := Open(plane, []Registration{runtimeRegistration("openai", connector, source)})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, registry.Close()) })

	lease, err := registry.AcquireRuntime()
	require.NoError(t, err)
	defer lease.Release()
	before := lease.Snapshot()
	first, err := lease.ResolveMaterial(t.Context(), "openai")
	require.NoError(t, err)
	source.value.Store("second")
	second, err := lease.ResolveMaterial(t.Context(), "openai")
	require.NoError(t, err)
	firstValue, _ := first.Value("api-key")
	secondValue, _ := second.Value("api-key")
	require.Equal(t, "first", firstValue)
	require.Equal(t, "second", secondValue)
	require.Same(t, before, lease.Snapshot())
	require.Same(t, connector, lease.Get("openai"))
	require.True(t, lease.RequiresAuthentication("openai"))
	require.False(t, lease.RequiresAuthentication("missing"))
	require.Equal(t, int32(0), connector.closeCount.Load())
}

func runtimeRegistration(
	provider string,
	connector *closeTrackingConnector,
	source credentials.MaterialSource,
) Registration {
	return Registration{
		Provider: provider, Connector: connector, OperatorSource: source,
		Operations:      []catalogs.ProviderOperation{catalogs.ProviderOperationChatCompletions},
		EndpointTypes:   []catalogs.EndpointType{catalogs.EndpointTypeOpenAI},
		OperatorBaseURL: "https://provider.example/v1",
		RequiresAuth:    true,
	}
}

type rotatingRegistryMaterialSource struct{ value atomic.Value }

func (s *rotatingRegistryMaterialSource) ResolveMaterial(context.Context) (credentials.Material, error) {
	profile := catalogs.ProviderCredentialProfile{
		ID: "api-key", Primitive: catalogs.ProviderAuthenticationAPIKey,
		Fields: []catalogs.ProviderCredentialFieldID{"api-key"},
		Placements: []catalogs.ProviderCredentialPlacement{{
			Field: "api-key", Kind: catalogs.ProviderCredentialPlacementHeader,
			Name: "Authorization", Scheme: catalogs.ProviderCredentialSchemeBearer,
		}},
	}
	return credentials.NewMaterial(
		profile,
		map[catalogs.ProviderCredentialFieldID]string{"api-key": s.value.Load().(string)},
		credentials.MaterialMetadata{Version: "test"},
	), nil
}
