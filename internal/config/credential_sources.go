package config

import (
	"fmt"
	"time"
)

// CredentialSourcesConfig defines the lifecycle of a direct inference secret
// source. It owns inference credentials alone. The catalog settings in
// catalog.go own catalog acquisition, and the two never share a variable.
type CredentialSourcesConfig struct {
	// RemoteRefreshInterval is the period between reads of a remote secret
	// store that holds an inference credential.
	RemoteRefreshInterval time.Duration `env:"REMOTE_REFRESH_INTERVAL,default=5m"`

	// ReconcileInterval is the period between reconciliations of the direct
	// secret sources.
	ReconcileInterval time.Duration `env:"RECONCILE_INTERVAL,default=1m"`

	// ReconcileTimeout bounds one reconciliation.
	ReconcileTimeout time.Duration `env:"RECONCILE_TIMEOUT,default=10s"`
}

// Validate validates the direct inference secret-source lifecycle.
func (c *CredentialSourcesConfig) Validate() error {
	if c.RemoteRefreshInterval < 0 {
		return fmt.Errorf("credential source remote refresh interval cannot be negative")
	}
	if c.ReconcileInterval < 0 {
		return fmt.Errorf("credential source reconcile interval cannot be negative")
	}
	if c.ReconcileTimeout < 0 {
		return fmt.Errorf("credential source reconcile timeout cannot be negative")
	}
	if c.ReconcileInterval > 0 && c.ReconcileTimeout == 0 {
		return fmt.Errorf("credential source reconcile timeout must be positive when reconciliation is enabled")
	}
	return nil
}
