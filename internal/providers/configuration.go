// Package providers owns the projection from operator inference settings to
// compiled adapter configuration.
package providers

import (
	"github.com/agentstation/starmap/pkg/catalogs"

	runtimecatalog "github.com/agentstation/starport/internal/catalog"
	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/credentials"
	"github.com/agentstation/starport/internal/providers/connectors"
)

// Configuration binds operator settings and one request-time material source to
// an exact catalog provider. It contains no credential value.
type Configuration struct {
	Connector        connectors.ProviderConfig
	CredentialSource credentials.MaterialSource
	Profile          catalogs.ProviderCredentialProfile
}

// Configurations projects external settings by exact Starmap provider ID. The
// connector projection contains operational values only.
func Configurations(
	configs config.ProvidersConfig,
) map[catalogs.ProviderID]Configuration {
	result := make(map[catalogs.ProviderID]Configuration)
	for _, entry := range configs.Entries() {
		providerConfig := connectors.ProviderConfig{
			BaseURL: entry.Config.BaseURL,
			Timeout: entry.Config.Timeout, MaxConnections: entry.Config.MaxConnections,
			Enabled: entry.Config.Enabled, EndpointBindings: cloneStrings(entry.Config.EndpointBindings),
		}
		result[entry.ProviderID] = Configuration{
			Connector:        providerConfig,
			CredentialSource: entry.Config.CredentialSource,
			Profile:          entry.Config.Material.Profile(),
		}
	}
	return result
}

// Availability projects active adapters into the runtime catalog contract.
func Availability(activations []Activation) []runtimecatalog.AdapterAvailability {
	result := make([]runtimecatalog.AdapterAvailability, 0, len(activations))
	for _, activation := range activations {
		result = append(result, runtimecatalog.AdapterAvailability{
			ProviderID: activation.ProviderID, Registered: true, Configured: true,
			Operations:       append([]catalogs.ProviderOperation(nil), activation.Operations...),
			EndpointTypes:    append([]catalogs.EndpointType(nil), activation.EndpointTypes...),
			BaseURL:          activation.Configuration.Connector.BaseURL,
			EndpointBindings: cloneStrings(activation.Configuration.Connector.EndpointBindings),
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
