package config

import (
	"fmt"

	"github.com/agentstation/starmap/pkg/catalogs"
)

func (c CatalogConfig) validateAuthority() error {
	if c.SourceStartupPolicy != CatalogStartupRequireAuthority {
		if c.SourceAuthorityID != "" || c.SourcePolicyID != "" {
			return fmt.Errorf("catalog authority and policy IDs require the require_authority startup policy")
		}
		return nil
	}
	if c.Source != CatalogSourceStarmap {
		return fmt.Errorf("catalog require_authority startup policy requires a starmap source")
	}
	if err := catalogs.ValidateCatalogAuthorityIdentity(c.SourceAuthorityID, c.SourcePolicyID); err != nil {
		return fmt.Errorf("catalog authority configuration: %w", err)
	}
	if c.AcquisitionEnabled {
		return fmt.Errorf("catalog require_authority startup policy requires local acquisition to be disabled")
	}
	return nil
}
