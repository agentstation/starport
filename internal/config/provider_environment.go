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
	role       string
}

// ResolveProviders resolves named inference material from the active Starmap
// provider collection. It validates the complete alias namespace before the
// first environment read.
func (c *Config) ResolveProviders(ctx context.Context, providers catalogs.ProvidersReader) error {
	resolved, err := c.ResolveProviderSet(ctx, providers, c.Providers)
	if err != nil {
		return err
	}
	c.Providers = resolved
	return nil
}

// ResolveProviderSet resolves one deployment-owned provider configuration
// against an exact catalog without changing the supplied settings.
func (c *Config) ResolveProviderSet(
	ctx context.Context,
	providers catalogs.ProvidersReader,
	settings ProvidersConfig,
) (ProvidersConfig, error) {
	if c == nil {
		return nil, errors.New("configuration is required")
	}
	if providers == nil {
		return nil, errors.New("catalog providers are required")
	}
	providerRecords := providers.List()
	if err := validateCredentialAliases(providerRecords); err != nil {
		return nil, err
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
		explicit := settings[provider.ID]
		references, err := providerCredentialReferences(
			provider,
			explicit.CredentialReferences,
			c.providerEnvironment,
		)
		if err != nil {
			return nil, fmt.Errorf("provider %s credential reference: %w", provider.ID, err)
		}
		policies, err := credentialReferencePolicies(references)
		if err != nil {
			return nil, fmt.Errorf("provider %s credential reference: %w", provider.ID, err)
		}
		handle, err := resolver.Provider(provider, policies, providerConfigurationPresent(explicit))
		if err != nil {
			return nil, err
		}
		material, configured, err := handle.Resolve(ctx)
		if err != nil {
			return nil, err
		}
		if !configured {
			continue
		}
		catalogConfig := projectResolvedProvider(provider, material, handle)
		resolved[provider.ID] = mergeProviderConfig(catalogConfig, explicit, references)
	}
	if err := resolved.Validate(); err != nil {
		return nil, err
	}
	return resolved, nil
}

func providerCredentialReferences(
	provider catalogs.Provider,
	configured map[catalogs.ProviderCredentialFieldID]CredentialReference,
	lookup environmentLookup,
) (map[catalogs.ProviderCredentialFieldID]CredentialReference, error) {
	references := cloneCredentialReferences(configured)
	if references == nil {
		references = make(map[catalogs.ProviderCredentialFieldID]CredentialReference)
	}
	if lookup == nil || provider.Credentials == nil {
		return references, nil
	}
	for _, field := range provider.Credentials.Fields {
		if _, exists := references[field.ID]; exists {
			continue
		}
		base, err := catalogs.DerivedCredentialEnvironmentName(
			starportCredentialProduct,
			provider.ID,
			field.ID,
		)
		if err != nil {
			return nil, err
		}
		referenceName := base + "_REFERENCE"
		fallbackName := referenceName + "_FALLBACK_AMBIENT"
		referenceValue, referenceFound := lookup.Lookup(referenceName)
		fallbackValue, fallbackFound := lookup.Lookup(fallbackName)
		if !referenceFound || referenceValue == "" {
			if fallbackFound && fallbackValue != "" {
				return nil, fmt.Errorf("%s requires %s", fallbackName, referenceName)
			}
			continue
		}
		fallback, err := parseReferenceFallback(fallbackName, fallbackValue, fallbackFound)
		if err != nil {
			return nil, err
		}
		references[field.ID] = CredentialReference{
			Reference: referenceValue, FallbackAmbient: fallback,
		}
	}
	return references, nil
}

func parseReferenceFallback(name, value string, found bool) (bool, error) {
	if !found || value == "" || value == "false" {
		return false, nil
	}
	if value == "true" {
		return true, nil
	}
	return false, fmt.Errorf("%s must be true or false", name)
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
				owner := credentialFieldOwner{
					providerID: provider.ID, fieldID: field.ID, role: "value",
				}
				if err := claimCredentialAlias(aliases, candidate, owner); err != nil {
					return err
				}
			}
			for _, alias := range []struct {
				name string
				role string
			}{
				{name: derived + "_REFERENCE", role: "reference"},
				{name: derived + "_REFERENCE_FALLBACK_AMBIENT", role: "fallback"},
			} {
				owner := credentialFieldOwner{
					providerID: provider.ID, fieldID: field.ID, role: alias.role,
				}
				if err := claimCredentialAlias(aliases, alias.name, owner); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func claimCredentialAlias(
	aliases map[string]credentialFieldOwner,
	name string,
	owner credentialFieldOwner,
) error {
	prior, exists := aliases[name]
	if !exists || prior == owner {
		aliases[name] = owner
		return nil
	}
	return fmt.Errorf(
		"%s is owned by %s/%s %s and %s/%s %s: %w",
		name,
		prior.providerID,
		prior.fieldID,
		prior.role,
		owner.providerID,
		owner.fieldID,
		owner.role,
		ErrCredentialAliasCollision,
	)
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

func mergeProviderConfig(
	resolved ProviderConfig,
	explicit ProviderConfig,
	references map[catalogs.ProviderCredentialFieldID]CredentialReference,
) ProviderConfig {
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
	resolved.CredentialReferences = cloneCredentialReferences(references)
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
