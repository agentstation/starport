package registry

import (
	"testing"

	"github.com/agentstation/starmap"
	runtimecatalog "github.com/agentstation/starport/internal/catalog"
	"github.com/stretchr/testify/require"
)

func TestAdmissionReadinessRetainsNoProviderLease(t *testing.T) {
	client, err := starmap.New()
	require.NoError(t, err)
	plane, err := runtimecatalog.Open(client)
	require.NoError(t, err)
	connector := newCloseTrackingConnector()
	registry, err := Open(plane, []Registration{runtimeRegistration("openai", connector, registryTestMaterialSource{})})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, registry.Close()) })
	require.True(t, registry.AdmissionReady())
	require.Zero(t, testing.AllocsPerRun(1000, func() { registry.AdmissionReady() }))
	require.Zero(t, connector.closeCount.Load())
	require.NoError(t, registry.Close())
	require.Equal(t, int32(1), connector.closeCount.Load())
	require.False(t, registry.AdmissionReady())
	require.False(t, NewEmpty().AdmissionReady())
	var absent *Registry
	require.False(t, absent.AdmissionReady())
}
