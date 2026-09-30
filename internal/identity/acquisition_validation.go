package identity

import (
	"fmt"
	"strings"
)

// Validate checks acquisition settings without registering providers or reaching a service.
func (c AcquisitionConfig) Validate() error {
	if len(c.OAuthProviders) == 0 && !c.WorkOS.configured() {
		return ErrNoProvidersConfigured
	}
	if strings.TrimRight(strings.TrimSpace(c.CallbackBaseURL), "/") == "" {
		return ErrCallbackBaseRequired
	}
	for _, provider := range c.OAuthProviders {
		if _, known := supportedOAuthProviders[provider.Name]; !known {
			return fmt.Errorf("%w: %s", ErrUnknownProvider, provider.Name)
		}
		if provider.ClientID == "" || provider.ClientSecret == "" {
			return fmt.Errorf("%w: %s", ErrIncompleteOAuthProvider, provider.Name)
		}
	}
	if c.WorkOS.configured() {
		if c.WorkOS.APIKey == "" || c.WorkOS.ClientID == "" {
			return ErrIncompleteWorkOS
		}
		if c.WorkOS.Organization == "" && c.WorkOS.Connection == "" {
			return ErrWorkOSDestinationRequired
		}
	}
	return nil
}
