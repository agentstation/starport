package providers

import (
	"net/netip"
	"net/url"
	"slices"
	"strings"

	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/agentstation/starport/internal/credentials"
	"github.com/agentstation/starport/internal/providers/keyring"
)

// InstallationDestinationApprovals derives conventional-key defaults from the pinned catalog.
// The caller must supply the bundled catalog, never an accepted runtime catalog.
// Parameterized and private destinations require explicit deployment approval.
func InstallationDestinationApprovals(bundled *catalogs.Catalog) (*credentials.DestinationApprovals, error) {
	var policies []*credentials.DestinationPolicy
	if bundled == nil {
		return nil, credentials.ErrDestinationUnapproved
	}
	for _, provider := range bundled.Providers().List() {
		if provider.Inference == nil || provider.Credentials == nil || !publicInstallationOrigin(provider.Inference.BaseURL) {
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
				policy, err := CompileDestinationPolicy(provider, string(role), profile.ID, "", nil)
				if err != nil {
					return nil, err
				}
				policies = append(policies, policy)
			}
		}
	}
	return credentials.NewDestinationApprovals(nil, policies...)
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
