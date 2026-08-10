// Package providers owns the projection from operator inference settings to
// compiled adapter configuration.
package providers

import (
	"github.com/agentstation/starmap/pkg/catalogs"

	runtimecatalog "github.com/agentstation/starport/internal/catalog"
	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/providers/connectors"
)

// Configurations projects external settings by exact Starmap provider ID.
func Configurations(configs config.ProvidersConfig) map[catalogs.ProviderID]connectors.ProviderConfig {
	result := make(map[catalogs.ProviderID]connectors.ProviderConfig)
	for _, entry := range configs.Entries() {
		providerConfig := connectors.ProviderConfig{
			BaseURL: entry.Config.BaseURL, APIKey: entry.Config.APIKey,
			Timeout: entry.Config.Timeout, MaxConnections: entry.Config.MaxConnections,
			Enabled: entry.Config.Enabled,
		}
		if entry.Config.ProjectID != "" || entry.Config.Location != "" {
			providerConfig.EndpointBindings = make(map[string]string, 2)
			if entry.Config.ProjectID != "" {
				providerConfig.EndpointBindings["project"] = entry.Config.ProjectID
			}
			if entry.Config.Location != "" {
				providerConfig.EndpointBindings["location"] = entry.Config.Location
			}
		}
		result[entry.ProviderID] = providerConfig
	}
	return result
}

// Availability projects active adapters into the runtime catalog contract.
func Availability(activations []connectors.AdapterActivation) []runtimecatalog.AdapterAvailability {
	result := make([]runtimecatalog.AdapterAvailability, 0, len(activations))
	for _, activation := range activations {
		result = append(result, runtimecatalog.AdapterAvailability{
			ProviderID: activation.ProviderID, Registered: true, Configured: true,
			Operations:       append([]catalogs.ProviderOperation(nil), activation.Descriptor.Operations...),
			EndpointTypes:    append([]catalogs.EndpointType(nil), activation.Descriptor.EndpointTypes...),
			BaseURL:          activation.Config.BaseURL,
			EndpointBindings: cloneStrings(activation.Config.EndpointBindings),
		})
	}
	return result
}

func cloneStrings(source map[string]string) map[string]string {
	if source == nil {
		return nil
	}
	result := make(map[string]string, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}
