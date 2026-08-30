package config

import (
	"time"

	"github.com/agentstation/starport/internal/audit"
)

// AuditConfig bounds the admin audit trail.
type AuditConfig struct {
	// Retention is how long an audit record stays before the trail prunes
	// it. The 9600h default is the audit package's 400-day window.
	Retention time.Duration `env:"RETENTION,default=9600h"`
}

// RetentionWindow returns the configured retention, falling back to the
// audit package's default when the value is absent or non-positive.
func (c *AuditConfig) RetentionWindow() time.Duration {
	if c == nil || c.Retention <= 0 {
		return audit.DefaultRetention
	}
	return c.Retention
}
