package config

import (
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"slices"
	"strings"

	"github.com/agentstation/starmap/pkg/catalogs"
)

// inferenceBaseURLSetting derives STARPORT_<PROVIDER>_INFERENCE_BASE_URL
// through the same catalog name rule as the credential fields.
const inferenceBaseURLSetting catalogs.ProviderCredentialFieldID = "inference-base-url"

// ErrInferenceBaseURLRefused reports an operator inference origin that
// Starport does not approve. The setting is the operator's destination
// approval, so a refused value stops startup instead of one provider.
var ErrInferenceBaseURLRefused = errors.New("inference base URL is refused")

func inferenceBaseURLEnvironmentName(providerID catalogs.ProviderID) (string, error) {
	return catalogs.DerivedCredentialEnvironmentName(
		starportCredentialProduct,
		providerID,
		inferenceBaseURLSetting,
	)
}

// providerInferenceBaseURL reads the replacement inference origin that the
// operator approves for deployment-owned material. An empty result means the
// setting is absent. Errors name the setting and never repeat its value.
func providerInferenceBaseURL(provider catalogs.Provider, lookup environmentLookup) (string, error) {
	if lookup == nil {
		return "", nil
	}
	name, err := inferenceBaseURLEnvironmentName(provider.ID)
	if err != nil {
		return "", err
	}
	value, found := lookup.Lookup(name)
	if !found || value == "" {
		return "", nil
	}
	refuse := func(reason string) error {
		return fmt.Errorf("%w: %s %s", ErrInferenceBaseURLRefused, name, reason)
	}
	if provider.Inference == nil {
		return "", refuse("names a provider without an inference service")
	}
	if parameterizedInference(provider) {
		return "", refuse("names a provider whose endpoints use catalog bindings")
	}
	if strings.ContainsAny(value, "{}") {
		return "", refuse("contains a template variable")
	}
	parsed, err := url.Parse(value)
	if err != nil || !parsed.IsAbs() || parsed.Opaque != "" {
		return "", refuse("is not an absolute URL")
	}
	switch {
	case parsed.User != nil:
		return "", refuse("contains user information")
	case parsed.RawQuery != "" || parsed.ForceQuery:
		return "", refuse("contains a query")
	case parsed.Fragment != "" || strings.Contains(value, "#"):
		return "", refuse("contains a fragment")
	case parsed.Hostname() == "":
		return "", refuse("has no host")
	}
	switch parsed.Scheme {
	case "https":
	case "http":
		if !localInferenceHost(parsed.Hostname()) {
			return "", refuse("uses http for a host that is not local or private")
		}
	default:
		return "", refuse("must use https, or http for a local or private host")
	}
	return strings.TrimSuffix(value, "/"), nil
}

// parameterizedInference reports a provider whose inference endpoints come
// from catalog binding variables. Those providers keep their binding settings.
func parameterizedInference(provider catalogs.Provider) bool {
	if strings.ContainsAny(provider.Inference.BaseURL, "{}") {
		return true
	}
	if provider.Credentials == nil {
		return false
	}
	return slices.ContainsFunc(provider.Credentials.Profiles, func(profile catalogs.ProviderCredentialProfile) bool {
		return len(profile.EndpointBindings) != 0 &&
			slices.Contains(provider.Credentials.Inference.Alternatives, profile.ID)
	})
}

// localInferenceHost accepts plain http only where no public network carries
// the credential: localhost and loopback, link-local, or private addresses.
// Other names can resolve anywhere, so they require https.
func localInferenceHost(host string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	if host == "localhost" {
		return true
	}
	address, err := netip.ParseAddr(host)
	if err != nil {
		return false
	}
	address = address.Unmap()
	return address.IsLoopback() || address.IsPrivate() || address.IsLinkLocalUnicast()
}
