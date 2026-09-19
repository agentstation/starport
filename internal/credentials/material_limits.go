package credentials

import (
	"fmt"
	"time"
)

// MaterialLimits bounds resident secrets and credential refresh work.
type MaterialLimits struct {
	Entries               int
	SecretBytes           int
	ConcurrentLoads       int
	TenantConcurrentLoads int
	Validity              time.Duration
	LoadTimeout           time.Duration
	Idle                  time.Duration
	RefreshInterval       time.Duration
}

// DefaultMaterialLimits returns the initial managed credential profile.
func DefaultMaterialLimits() MaterialLimits {
	return MaterialLimits{Entries: 1024, SecretBytes: 16 << 20, ConcurrentLoads: 4, TenantConcurrentLoads: 2, Validity: 5 * time.Second, LoadTimeout: time.Second, Idle: time.Minute, RefreshInterval: time.Second}
}

// Validate checks resource bounds and the maximum permission-validity window.
func (l MaterialLimits) Validate() error {
	if l.Entries <= 0 || l.SecretBytes <= 0 || l.ConcurrentLoads <= 0 || l.TenantConcurrentLoads <= 0 {
		return fmt.Errorf("managed credential resource limits must be positive")
	}
	if l.TenantConcurrentLoads > l.ConcurrentLoads {
		return fmt.Errorf("managed credential tenant loads exceed the global limit")
	}
	if l.Validity <= 0 || l.Validity > 5*time.Minute {
		return fmt.Errorf("managed credential validity must be positive and at most five minutes")
	}
	if l.LoadTimeout <= 0 || l.LoadTimeout >= l.Validity {
		return fmt.Errorf("managed credential load timeout must be positive and shorter than validity")
	}
	if l.RefreshInterval <= 0 || l.RefreshInterval >= l.Validity {
		return fmt.Errorf("managed credential refresh interval must be positive and shorter than validity")
	}
	if l.Idle < l.Validity {
		return fmt.Errorf("managed credential idle retention must cover validity")
	}
	return nil
}
