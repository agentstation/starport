package providers

import (
	"net/netip"
	"net/url"
	"slices"
	"strings"

	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/credentials"
	"github.com/agentstation/starport/internal/providers/keyring"
)

// InstallationDestinationApprovals derives conventional-key defaults from the pinned catalog.
// The caller must supply the bundled catalog, never an accepted runtime catalog.
// Parameterized and private destinations require explicit deployment approval.
func InstallationDestinationApprovals(bundled *catalogs.Catalog) (*credentials.DestinationApprovals, error) {
	return DeploymentDestinationApprovals(bundled, nil)
}

// DeploymentDestinationApprovals adds operator-approved inference origins to the installation defaults.
// The settings must be the startup resolution, so a catalog refresh never changes an approval.
// Only ProviderConfig.InferenceOrigin is an approval. An explicit BaseURL approves nothing.
// The approved origin replaces the catalog origin only for the environment role, because the
// router binds the operator override only to environment material. Shared, BYOK, and
// anonymous material keep the public catalog origin.
func DeploymentDestinationApprovals(bundled *catalogs.Catalog, settings config.ProvidersConfig) (*credentials.DestinationApprovals, error) {
	var policies []*credentials.DestinationPolicy
	if bundled == nil {
		return nil, credentials.ErrDestinationUnapproved
	}
	for _, provider := range bundled.Providers().List() {
		if provider.Inference == nil || provider.Credentials == nil {
			continue
		}
		override := ""
		// A parameterized provider's base URL comes from its catalog bindings,
		// which still require explicit deployment approval.
		if !parameterizedDestination(provider) {
			override = strings.TrimRight(strings.TrimSpace(settings[provider.ID].InferenceOrigin), "/")
		}
		public := publicInstallationOrigin(provider.Inference.BaseURL)
		if override == "" && !public {
			continue
		}
		for _, profile := range provider.Credentials.Profiles {
			if len(profile.EndpointBindings) != 0 || !slices.Contains(provider.Credentials.Inference.Alternatives, profile.ID) {
				continue
			}
			roles := []keyring.CredentialSource{keyring.SourceEnvironment, keyring.SourceShared, keyring.SourceBYOK}
			if profile.Primitive == catalogs.ProviderAuthenticationNone {
				roles = append(roles, keyring.SourceAnonymous)
			}
			for _, role := range roles {
				baseURL := ""
				if role == keyring.SourceEnvironment {
					baseURL = override
				}
				if baseURL == "" && !public {
					continue
				}
				policy, err := CompileDestinationPolicy(provider, string(role), profile.ID, baseURL, nil)
				if err != nil {
					return nil, err
				}
				policies = append(policies, policy)
			}
		}
	}
	return credentials.NewDestinationApprovals(nil, policies...)
}

func parameterizedDestination(provider catalogs.Provider) bool {
	if strings.ContainsAny(provider.Inference.BaseURL, "{}") {
		return true
	}
	return slices.ContainsFunc(provider.Credentials.Profiles, func(profile catalogs.ProviderCredentialProfile) bool {
		return len(profile.EndpointBindings) != 0 && slices.Contains(provider.Credentials.Inference.Alternatives, profile.ID)
	})
}

func publicInstallationOrigin(value string) bool {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "https" || parsed.User != nil || parsed.Hostname() == "" {
		return false
	}
	host := strings.ToLower(strings.TrimSuffix(parsed.Hostname(), "."))
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return false
	}
	if address, err := netip.ParseAddr(host); err == nil {
		return address.IsGlobalUnicast() && !address.IsPrivate() && !address.IsLoopback() && !address.IsLinkLocalUnicast()
	}
	return true
}
