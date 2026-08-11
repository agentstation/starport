// Package providers owns the projection from operator inference settings to
// compiled adapter configuration.
package providers

import (
	"errors"
	"fmt"

	"github.com/agentstation/starmap/pkg/catalogs"

	runtimecatalog "github.com/agentstation/starport/internal/catalog"
	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/credentials"
	"github.com/agentstation/starport/internal/providerauth"
	"github.com/agentstation/starport/internal/providers/connectors"
)

// Configurations projects external settings by exact Starmap provider ID.
func Configurations(
	configs config.ProvidersConfig,
) (map[catalogs.ProviderID]connectors.ProviderConfig, error) {
	result := make(map[catalogs.ProviderID]connectors.ProviderConfig)
	for _, entry := range configs.Entries() {
		providerConfig := connectors.ProviderConfig{
			BaseURL: entry.Config.BaseURL,
			Timeout: entry.Config.Timeout, MaxConnections: entry.Config.MaxConnections,
			Enabled: entry.Config.Enabled, EndpointBindings: cloneStrings(entry.Config.EndpointBindings),
		}
		if err := projectAuthentication(entry.ProviderID, entry.Config, &providerConfig); err != nil {
			return nil, err
		}
		result[entry.ProviderID] = providerConfig
	}
	return result, nil
}

func projectAuthentication(
	providerID catalogs.ProviderID,
	configured config.ProviderConfig,
	projected *connectors.ProviderConfig,
) error {
	profile := configured.Material.Profile()
	switch profile.Primitive {
	case catalogs.ProviderAuthenticationNone:
		return nil
	case catalogs.ProviderAuthenticationAPIKey, catalogs.ProviderAuthenticationBearerToken:
		fieldID, err := placedCredentialField(configured.Material, false)
		if err != nil {
			return fmt.Errorf("project provider %s credentials: %w", providerID, err)
		}
		value, _ := configured.Material.Value(fieldID)
		projected.APIKey = value
		projected.AuthMode = providerauth.ModeStatic
		return nil
	case catalogs.ProviderAuthenticationGoogleDefault,
		catalogs.ProviderAuthenticationAzureDefault,
		catalogs.ProviderAuthenticationAWSDefault:
		fieldID, err := placedCredentialField(configured.Material, true)
		if err != nil {
			return fmt.Errorf("project provider %s credentials: %w", providerID, err)
		}
		source, err := providerauth.NewBearerSource(configured.CredentialSource, fieldID)
		if err != nil {
			return fmt.Errorf("project provider %s credentials: %w", providerID, err)
		}
		projected.AuthMode = providerauth.ModeDefault
		projected.CredentialSource = source
		return nil
	case "":
		return errors.New("provider credential material has no selected profile")
	default:
		return fmt.Errorf("provider %s uses unsupported authentication primitive %s", providerID, profile.Primitive)
	}
}

func placedCredentialField(
	material credentials.Material,
	bearerOnly bool,
) (catalogs.ProviderCredentialFieldID, error) {
	profile := material.Profile()
	var selected catalogs.ProviderCredentialFieldID
	for _, placement := range profile.Placements {
		if bearerOnly && (placement.Kind != catalogs.ProviderCredentialPlacementHeader ||
			placement.Scheme != catalogs.ProviderCredentialSchemeBearer) {
			continue
		}
		value, exists := material.Value(placement.Field)
		if !exists || value == "" {
			continue
		}
		if selected != "" && selected != placement.Field {
			return "", errors.New("selected profile has multiple placed credential fields")
		}
		selected = placement.Field
	}
	if selected == "" {
		return "", errors.New("selected profile has no placed credential field")
	}
	return selected, nil
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
