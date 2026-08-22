package view

import (
	"sort"

	starmapcatalogs "github.com/agentstation/starmap/pkg/catalogs"

	runtimecatalog "github.com/agentstation/starport/internal/catalog"
)

// Providers projects every provider with a routable offering in the
// snapshot. requiresAuth reports the gateway's credential requirement for
// a provider id; it is injected so this package stays free of runtime
// registry types. A nil snapshot projects to nil.
func Providers(
	snapshot *runtimecatalog.RoutableSnapshot,
	requiresAuth func(providerID string) bool,
) []ProviderInfo {
	if snapshot == nil {
		return nil
	}
	seen := make(map[starmapcatalogs.ProviderID]struct{})
	providers := make([]ProviderInfo, 0)
	for _, route := range snapshot.Routes() {
		if _, exists := seen[route.ProviderID]; exists {
			continue
		}
		provider, err := snapshot.Catalog().Provider(route.ProviderID)
		if err != nil {
			continue
		}
		info := ProviderInfo{
			ID: string(provider.ID), Name: provider.Name,
			CredentialFields: inferenceCredentialFields(provider),
		}
		if requiresAuth != nil {
			info.RequiresAuth = requiresAuth(string(provider.ID))
		}
		if provider.Description != nil {
			info.Description = *provider.Description
		}
		if provider.DocsURL != nil {
			info.DocsURL = *provider.DocsURL
		}
		if provider.StatusPageURL != nil {
			info.URL = *provider.StatusPageURL
		}
		if provider.Headquarters != nil {
			info.Headquarters = *provider.Headquarters
		}
		info.Policies = providerPolicies(provider)
		capabilities := make(map[string]struct{})
		for _, providerRoute := range snapshot.RoutesForProvider(route.ProviderID) {
			info.Models = append(info.Models, providerRoute.ID())
			for _, operation := range providerRoute.Operations {
				capabilities[string(operation)] = struct{}{}
			}
		}
		for capability := range capabilities {
			info.Capabilities = append(info.Capabilities, capability)
		}
		sort.Strings(info.Capabilities)
		seen[route.ProviderID] = struct{}{}
		providers = append(providers, info)
	}
	return providers
}

// providerPolicies summarizes the provider's published privacy,
// retention, and governance policies. A provider with no policy facts
// projects to nil.
func providerPolicies(provider starmapcatalogs.Provider) *ProviderPolicyInfo {
	if provider.PrivacyPolicy == nil &&
		provider.RetentionPolicy == nil &&
		provider.GovernancePolicy == nil {
		return nil
	}
	info := &ProviderPolicyInfo{}
	if privacy := provider.PrivacyPolicy; privacy != nil {
		if privacy.PrivacyPolicyURL != nil {
			info.PrivacyPolicyURL = *privacy.PrivacyPolicyURL
		}
		if privacy.TermsOfServiceURL != nil {
			info.TermsOfServiceURL = *privacy.TermsOfServiceURL
		}
		info.RetainsData = copyBool(privacy.RetainsData)
		info.TrainsOnData = copyBool(privacy.TrainsOnData)
	}
	if retention := provider.RetentionPolicy; retention != nil && retention.Details != nil {
		info.Retention = *retention.Details
	}
	if governance := provider.GovernancePolicy; governance != nil {
		info.Moderated = copyBool(governance.Moderated)
	}
	return info
}

func copyBool(value *bool) *bool {
	if value == nil {
		return nil
	}
	copied := *value
	return &copied
}

// inferenceCredentialFields projects the catalog's inference credential
// contract in profile order, deduplicated across alternatives.
func inferenceCredentialFields(provider starmapcatalogs.Provider) []CredentialFieldInfo {
	contract := provider.Credentials
	if contract == nil {
		return nil
	}
	fields := make(map[starmapcatalogs.ProviderCredentialFieldID]starmapcatalogs.ProviderCredentialField, len(contract.Fields))
	for _, field := range contract.Fields {
		fields[field.ID] = field
	}
	profiles := make(map[starmapcatalogs.ProviderCredentialProfileID]starmapcatalogs.ProviderCredentialProfile, len(contract.Profiles))
	for _, profile := range contract.Profiles {
		profiles[profile.ID] = profile
	}
	seen := make(map[starmapcatalogs.ProviderCredentialFieldID]struct{})
	infos := make([]CredentialFieldInfo, 0)
	for _, profileID := range contract.Inference.Alternatives {
		profile, exists := profiles[profileID]
		if !exists {
			continue
		}
		for _, fieldID := range profile.Fields {
			if _, exists := seen[fieldID]; exists {
				continue
			}
			field, exists := fields[fieldID]
			if !exists {
				continue
			}
			seen[fieldID] = struct{}{}
			infos = append(infos, CredentialFieldInfo{
				ID:          string(field.ID),
				Kind:        string(field.Kind),
				Required:    field.Required,
				Default:     field.Default,
				Description: field.Description,
			})
		}
	}
	return infos
}
