package app

import (
	"errors"
	"fmt"

	"github.com/agentstation/starmap/pkg/catalogs"

	runtimecatalog "github.com/agentstation/starport/internal/catalog"
	"github.com/agentstation/starport/internal/providerauth"
	"github.com/agentstation/starport/internal/providers"
	"github.com/agentstation/starport/internal/providers/connectors"
	"github.com/agentstation/starport/internal/registry"
)

var (
	// ErrProvidersRequired reports an empty production provider set.
	ErrProvidersRequired = errors.New("at least one production provider is required")
)

func buildRegistrations(
	catalog *catalogs.Catalog,
	transportRegistry *connectors.TransportRegistry,
	authenticationRegistry *providerauth.Registry,
	configurations map[catalogs.ProviderID]providers.Configuration,
	newConnector func(string, []catalogs.EndpointType, connectors.ProviderConfig) (connectors.Connector, error),
) ([]registry.Registration, error) {
	if catalog == nil {
		return nil, runtimecatalog.ErrCatalogRequired
	}
	active, err := providers.Activate(
		catalog,
		transportRegistry,
		authenticationRegistry,
		configurations,
	)
	if err != nil {
		return nil, err
	}
	if len(active) == 0 {
		return nil, ErrProvidersRequired
	}

	registrations := make([]registry.Registration, 0, len(active))
	for _, activation := range active {
		providerID := string(activation.ProviderID)
		connector, err := newConnector(
			providerID,
			activation.EndpointTypes,
			activation.Configuration.Connector,
		)
		if err != nil {
			if connector != nil {
				if closeErr := connector.Close(); closeErr != nil {
					err = errors.Join(err, fmt.Errorf("close failed %s connector: %w", providerID, closeErr))
				}
			}
			closeRegistrations(registrations)
			return nil, fmt.Errorf("create %s connector: %w", providerID, err)
		}
		if connector == nil {
			closeRegistrations(registrations)
			return nil, fmt.Errorf("create %s connector: connector is nil", providerID)
		}
		registrations = append(registrations, registry.Registration{
			Provider: providerID, Connector: connector,
			Operations:       activation.Operations,
			EndpointTypes:    activation.EndpointTypes,
			BaseURL:          activation.Configuration.Connector.BaseURL,
			EndpointBindings: activation.Configuration.Connector.EndpointBindings,
			RequiresAuth:     activation.RequiresAuth,
			CredentialSource: activation.Configuration.CredentialSource,
		})
	}
	return registrations, nil
}

func closeRegistrations(registrations []registry.Registration) {
	for index := len(registrations) - 1; index >= 0; index-- {
		_ = registrations[index].Connector.Close()
	}
}
