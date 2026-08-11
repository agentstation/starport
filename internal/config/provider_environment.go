package config

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/agentstation/starmap/pkg/catalogs"

	"github.com/agentstation/starport/internal/credentials"
)

const starportCredentialProduct = "STARPORT"

var (
	// ErrCredentialAliasCollision reports an ambiguous catalog-derived
	// environment name. Validation completes before any value lookup.
	ErrCredentialAliasCollision = errors.New("provider credential environment alias is ambiguous")
)

type environmentLookup interface {
	Lookup(string) (string, bool)
}

type credentialFieldOwner struct {
	providerID catalogs.ProviderID
	fieldID    catalogs.ProviderCredentialFieldID
}

// ResolveProviders resolves named inference material from the active Starmap
// provider collection. It validates the complete alias namespace before the
// first environment read.
func (c *Config) ResolveProviders(ctx context.Context, providers catalogs.ProvidersReader) error {
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
	resolver := c.credentialResolver
	if resolver == nil {
		options := []credentials.ResolverOption{}
		if c.providerEnvironment != nil {
			options = append(options, credentials.WithEnvironmentLookup(c.providerEnvironment.Lookup))
		}
		resolver = credentials.NewResolver(options...)
		c.credentialResolver = resolver
	}

	resolved := make(ProvidersConfig)
	for _, provider := range providerRecords {
		explicit := c.Providers[provider.ID]
		policies, err := credentialReferencePolicies(explicit.CredentialReferences)
		if err != nil {
			return fmt.Errorf("provider %s credential reference: %w", provider.ID, err)
		}
		handle, err := resolver.Provider(provider, policies, providerConfigurationPresent(explicit))
		if err != nil {
			return err
		}
		material, configured, err := handle.Resolve(ctx)
		if err != nil {
			return err
		}
		if !configured {
			continue
		}
		catalogConfig := projectResolvedProvider(provider, material, handle)
		resolved[provider.ID] = mergeProviderConfig(catalogConfig, explicit)
	}
	if err := resolved.Validate(); err != nil {
		return err
	}
	c.Providers = resolved
	return nil
}

func credentialReferencePolicies(
	references map[catalogs.ProviderCredentialFieldID]CredentialReference,
) (map[catalogs.ProviderCredentialFieldID]credentials.ReferencePolicy, error) {
	policies := make(map[catalogs.ProviderCredentialFieldID]credentials.ReferencePolicy, len(references))
	for fieldID, configured := range references {
		reference, err := credentials.ParseReference(configured.Reference)
		if err != nil {
			return nil, err
		}
		policies[fieldID] = credentials.ReferencePolicy{
			Reference: reference, FallbackAmbient: configured.FallbackAmbient,
		}
	}
	return policies, nil
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

func projectResolvedProvider(
	provider catalogs.Provider,
	material credentials.Material,
	source credentials.MaterialSource,
) ProviderConfig {
	result := ProviderConfig{
		Material: material, CredentialSource: source,
		Timeout: 30 * time.Second, MaxConnections: 100, Enabled: true,
		EndpointBindings: material.EndpointBindings(),
	}
	for variable, value := range result.EndpointBindings {
		if provider.Inference != nil &&
			strings.TrimSpace(provider.Inference.BaseURL) == "{"+variable+"}" {
			result.BaseURL = value
		}
	}
	return result
}

func mergeProviderConfig(resolved, explicit ProviderConfig) ProviderConfig {
	if explicit.BaseURL != "" {
		resolved.BaseURL = explicit.BaseURL
	}
	if explicit.Timeout > 0 {
		resolved.Timeout = explicit.Timeout
	}
	if explicit.MaxConnections > 0 {
		resolved.MaxConnections = explicit.MaxConnections
	}
	resolved.Enabled = resolved.Enabled || explicit.Enabled
	resolved.CredentialReferences = cloneCredentialReferences(explicit.CredentialReferences)
	return resolved
}

func cloneCredentialReferences(
	references map[catalogs.ProviderCredentialFieldID]CredentialReference,
) map[catalogs.ProviderCredentialFieldID]CredentialReference {
	if references == nil {
		return nil
	}
	cloned := make(map[catalogs.ProviderCredentialFieldID]CredentialReference, len(references))
	for fieldID, reference := range references {
		cloned[fieldID] = reference
	}
	return cloned
}

func providerConfigurationPresent(provider ProviderConfig) bool {
	return provider.Enabled || provider.BaseURL != "" || len(provider.CredentialReferences) > 0
}
