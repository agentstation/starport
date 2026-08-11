package config

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/agentstation/starmap/pkg/catalogs"

	"github.com/agentstation/starport/internal/providerauth"
)

const starportCredentialProduct = "STARPORT"

var (
	// ErrCredentialAliasCollision reports an ambiguous catalog-derived
	// environment name. Validation completes before any value lookup.
	ErrCredentialAliasCollision = errors.New("provider credential environment alias is ambiguous")
	// ErrProviderCredentialInvalid reports invalid selected ambient material.
	ErrProviderCredentialInvalid = errors.New("provider credential environment value is invalid")
)

type environmentLookup interface {
	Lookup(string) (string, bool)
}

type credentialFieldOwner struct {
	providerID catalogs.ProviderID
	fieldID    catalogs.ProviderCredentialFieldID
}

// ResolveProviders resolves inference settings from the active Starmap
// provider collection. It validates the complete alias namespace before the
// first environment read.
func (c *Config) ResolveProviders(providers catalogs.ProvidersReader) error {
	if c == nil {
		return errors.New("configuration is required")
	}
	if providers == nil {
		return errors.New("catalog providers are required")
	}
	providerRecords := providers.List()
	if err := validateCredentialAliases(providerRecords); err != nil {
		return err
	}
	if c.providerEnvironment == nil {
		return c.Providers.Validate()
	}

	resolved := make(ProvidersConfig)
	for _, provider := range providerRecords {
		explicit := c.Providers[provider.ID]
		ambient, configured, err := resolveProviderEnvironment(
			provider,
			c.providerEnvironment,
			providerConfigurationPresent(explicit),
		)
		if err != nil {
			return err
		}
		if configured {
			resolved[provider.ID] = mergeProviderConfig(ambient, explicit)
		} else if providerConfigurationPresent(explicit) {
			resolved[provider.ID] = explicit
		}
	}
	for providerID, explicit := range c.Providers {
		if _, found := resolved[providerID]; !found && providerConfigurationPresent(explicit) {
			resolved[providerID] = explicit
		}
	}
	if err := resolved.Validate(); err != nil {
		return err
	}
	c.Providers = resolved
	return nil
}

func validateCredentialAliases(providers []catalogs.Provider) error {
	aliases := make(map[string]credentialFieldOwner)
	for _, provider := range providers {
		if err := provider.ValidateContract(); err != nil {
			return fmt.Errorf("validate provider %s credential contract: %w", provider.ID, err)
		}
		if provider.Credentials == nil {
			continue
		}
		for _, field := range provider.Credentials.Fields {
			owner := credentialFieldOwner{providerID: provider.ID, fieldID: field.ID}
			candidates := append([]string(nil), field.Environment...)
			derived, err := catalogs.DerivedCredentialEnvironmentName(
				starportCredentialProduct,
				provider.ID,
				field.ID,
			)
			if err != nil {
				return err
			}
			candidates = append(candidates, derived)
			for _, candidate := range candidates {
				prior, exists := aliases[candidate]
				if exists && prior != owner {
					return fmt.Errorf(
						"%s is owned by %s/%s and %s/%s: %w",
						candidate,
						prior.providerID,
						prior.fieldID,
						owner.providerID,
						owner.fieldID,
						ErrCredentialAliasCollision,
					)
				}
				aliases[candidate] = owner
			}
		}
	}
	return nil
}

func resolveProviderEnvironment(
	provider catalogs.Provider,
	lookup environmentLookup,
	forced bool,
) (ProviderConfig, bool, error) {
	credentials := provider.Credentials
	if credentials == nil || len(credentials.Inference.Alternatives) == 0 {
		return ProviderConfig{}, false, nil
	}
	fields := make(map[catalogs.ProviderCredentialFieldID]catalogs.ProviderCredentialField, len(credentials.Fields))
	for _, field := range credentials.Fields {
		fields[field.ID] = field
	}
	profiles := make(map[catalogs.ProviderCredentialProfileID]catalogs.ProviderCredentialProfile, len(credentials.Profiles))
	for _, profile := range credentials.Profiles {
		profiles[profile.ID] = profile
	}
	for _, profileID := range credentials.Inference.Alternatives {
		profile := profiles[profileID]
		values := make(map[catalogs.ProviderCredentialFieldID]string, len(profile.Fields))
		explicit := false
		complete := true
		for _, fieldID := range profile.Fields {
			field := fields[fieldID]
			value, selected, selectedExplicitly, err := resolveAmbientCredentialField(provider.ID, field, lookup)
			if err != nil {
				return ProviderConfig{}, false, err
			}
			explicit = explicit || selectedExplicitly
			if selected {
				values[fieldID] = value
				continue
			}
			if field.Required && !defaultChainDefersField(profile.Primitive, field) {
				complete = false
				break
			}
		}
		if !complete || (!explicit && !forced) {
			continue
		}
		configured, err := projectProviderProfile(provider, profile, fields, values)
		if err != nil {
			return ProviderConfig{}, false, err
		}
		return configured, true, nil
	}
	return ProviderConfig{}, false, nil
}

func resolveAmbientCredentialField(
	providerID catalogs.ProviderID,
	field catalogs.ProviderCredentialField,
	lookup environmentLookup,
) (string, bool, bool, error) {
	candidates := append([]string(nil), field.Environment...)
	derived, err := catalogs.DerivedCredentialEnvironmentName(
		starportCredentialProduct,
		providerID,
		field.ID,
	)
	if err != nil {
		return "", false, false, err
	}
	candidates = append(candidates, derived)
	for _, candidate := range candidates {
		value, found := lookup.Lookup(candidate)
		if !found || value == "" {
			continue
		}
		if field.Pattern != "" {
			matched, matchErr := regexp.MatchString(field.Pattern, value)
			if matchErr != nil || !matched {
				return "", false, false, fmt.Errorf(
					"%s selected an invalid value for %s/%s: %w",
					candidate,
					providerID,
					field.ID,
					ErrProviderCredentialInvalid,
				)
			}
		}
		return value, true, true, nil
	}
	if field.Default != "" {
		return field.Default, true, false, nil
	}
	return "", false, false, nil
}

func defaultChainDefersField(
	primitive catalogs.ProviderAuthenticationPrimitive,
	field catalogs.ProviderCredentialField,
) bool {
	if field.Kind != catalogs.ProviderCredentialFieldSecret {
		return false
	}
	switch primitive {
	case catalogs.ProviderAuthenticationGoogleDefault,
		catalogs.ProviderAuthenticationAzureDefault,
		catalogs.ProviderAuthenticationAWSDefault:
		return true
	default:
		return false
	}
}

func projectProviderProfile(
	provider catalogs.Provider,
	profile catalogs.ProviderCredentialProfile,
	fields map[catalogs.ProviderCredentialFieldID]catalogs.ProviderCredentialField,
	values map[catalogs.ProviderCredentialFieldID]string,
) (ProviderConfig, error) {
	result := ProviderConfig{
		ProfileID: profile.ID, Primitive: profile.Primitive,
		Timeout: 30 * time.Second, MaxConnections: 100, Enabled: true,
	}
	switch profile.Primitive {
	case catalogs.ProviderAuthenticationNone:
	case catalogs.ProviderAuthenticationAPIKey, catalogs.ProviderAuthenticationBearerToken:
		result.AuthMode = providerauth.ModeStatic
		for _, fieldID := range profile.Fields {
			if fields[fieldID].Kind != catalogs.ProviderCredentialFieldSecret {
				continue
			}
			if result.APIKey != "" {
				return ProviderConfig{}, fmt.Errorf(
					"provider %s profile %s contains multiple secret fields",
					provider.ID,
					profile.ID,
				)
			}
			result.APIKey = values[fieldID]
		}
	case catalogs.ProviderAuthenticationGoogleDefault,
		catalogs.ProviderAuthenticationAzureDefault,
		catalogs.ProviderAuthenticationAWSDefault:
		result.AuthMode = providerauth.ModeDefault
	default:
		return ProviderConfig{}, fmt.Errorf(
			"provider %s profile %s uses unsupported authentication primitive %s",
			provider.ID,
			profile.ID,
			profile.Primitive,
		)
	}
	for _, binding := range profile.EndpointBindings {
		value := values[binding.Field]
		if value == "" {
			continue
		}
		if result.EndpointBindings == nil {
			result.EndpointBindings = make(map[string]string)
		}
		result.EndpointBindings[binding.Variable] = value
		if binding.Format == catalogs.ProviderCredentialEndpointBindingURL &&
			provider.Inference != nil &&
			strings.TrimSpace(provider.Inference.BaseURL) == "{"+binding.Variable+"}" {
			result.BaseURL = value
		}
	}
	return result, nil
}

func mergeProviderConfig(resolved, explicit ProviderConfig) ProviderConfig {
	if explicit.BaseURL != "" {
		resolved.BaseURL = explicit.BaseURL
	}
	if explicit.APIKey != "" {
		resolved.APIKey = explicit.APIKey
	}
	if explicit.AuthMode != "" {
		resolved.AuthMode = explicit.AuthMode
	}
	if explicit.ProfileID != "" {
		resolved.ProfileID = explicit.ProfileID
	}
	if explicit.Primitive != "" {
		resolved.Primitive = explicit.Primitive
	}
	if explicit.Timeout > 0 {
		resolved.Timeout = explicit.Timeout
	}
	if explicit.MaxConnections > 0 {
		resolved.MaxConnections = explicit.MaxConnections
	}
	resolved.Enabled = resolved.Enabled || explicit.Enabled
	if len(explicit.EndpointBindings) > 0 {
		if resolved.EndpointBindings == nil {
			resolved.EndpointBindings = make(map[string]string)
		}
		for key, value := range explicit.EndpointBindings {
			resolved.EndpointBindings[key] = value
		}
	}
	return resolved
}

func providerConfigurationPresent(provider ProviderConfig) bool {
	return provider.Enabled || provider.APIKey != "" || provider.AuthMode != "" ||
		provider.ProfileID != "" || provider.Primitive != "" || provider.BaseURL != "" ||
		len(provider.EndpointBindings) > 0
}
