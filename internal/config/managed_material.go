package config

import (
	"github.com/agentstation/starport/internal/credentials"
	"time"
)

// ManagedMaterialConfig configures account and shared credential memory.
type ManagedMaterialConfig struct {
	Entries               int           `env:"ENTRIES,default=1024"`
	SecretBytes           int           `env:"SECRET_BYTES,default=16777216"`
	ConcurrentLoads       int           `env:"CONCURRENT_LOADS,default=4"`
	TenantConcurrentLoads int           `env:"TENANT_CONCURRENT_LOADS,default=2"`
	Validity              time.Duration `env:"VALIDITY,default=5s"`
	LoadTimeout           time.Duration `env:"LOAD_TIMEOUT,default=1s"`
	Idle                  time.Duration `env:"IDLE,default=1m"`
	RefreshInterval       time.Duration `env:"REFRESH_INTERVAL,default=1s"`
}

// Limits translates configuration into the credential-owned resource contract.
func (c ManagedMaterialConfig) Limits() credentials.MaterialLimits {
	if c == (ManagedMaterialConfig{}) {
		return credentials.DefaultMaterialLimits()
	}
	return credentials.MaterialLimits{Entries: c.Entries, SecretBytes: c.SecretBytes, ConcurrentLoads: c.ConcurrentLoads, TenantConcurrentLoads: c.TenantConcurrentLoads, Validity: c.Validity, LoadTimeout: c.LoadTimeout, Idle: c.Idle, RefreshInterval: c.RefreshInterval}
}
