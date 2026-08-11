package connectors

import (
	"errors"
	"testing"

	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/stretchr/testify/require"
)

func TestTransportRegistryRejectsUnsupportedPrimitive(t *testing.T) {
	registry, err := ProductionTransportRegistry()
	require.NoError(t, err)
	_, err = registry.NewProviderConnector(
		"acme",
		[]catalogs.EndpointType{"future-wire-protocol"},
		ProviderConfig{BaseURL: "https://provider.example"},
	)
	require.True(t, errors.Is(err, ErrTransportUnsupported), "%v", err)
}
