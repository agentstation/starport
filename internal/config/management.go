package config

import (
	"fmt"

	"github.com/agentstation/starport/internal/deployment"
)

// Configuration management modes. They select the one authority that supplies
// the deployment-scope catalog settings of a process.
const (
	// ManagementLocal reads deployment settings from the product file and the
	// environment of this process.
	ManagementLocal = "local"
	// ManagementShared reads deployment settings from the shared revision in
	// the relational store. Local deployment values are ignored.
	ManagementShared = "shared"
	// ManagementExternal reads settings like local management. An external
	// controller owns the file, and the effective report names it.
	ManagementExternal = "external"
)

// managementEnvironment names the bootstrap management setting.
const managementEnvironment = "STARPORT_CONFIG_MANAGEMENT"

// ManagementConfig selects the deployment configuration authority. Every
// field is a bootstrap input. A shared revision never supplies one.
type ManagementConfig struct {
	// Mode is local, shared, or external. The loader selects shared for a
	// distributed storage recipe and local otherwise. A Config built in code
	// with an empty mode uses local management.
	Mode string `env:"MANAGEMENT"`
}

// selectDefaultManagement applies the storage-recipe default to an unset mode.
func (c *Config) selectDefaultManagement() {
	if c.Management.Mode != "" {
		return
	}
	c.Management.Mode = ManagementLocal
	if c.Storage.Distributed() {
		c.Management.Mode = ManagementShared
	}
}

// ManagementMode returns the selected configuration authority.
func (c *Config) ManagementMode() string {
	if c == nil || c.Management.Mode == "" {
		return ManagementLocal
	}
	return c.Management.Mode
}

// SharedManagement reports whether the shared revision supplies the
// deployment-scope catalog settings.
func (c *Config) SharedManagement() bool {
	return c.ManagementMode() == ManagementShared
}

// ConfigNamespace returns the shared configuration namespace: the key prefix
// that STARPORT_DEPLOYMENT_ID derives. It is empty for an invalid deployment ID.
func (c *Config) ConfigNamespace() string {
	if c == nil {
		return ""
	}
	namespace, err := deployment.KeyPrefix(c.paths.DeploymentID)
	if err != nil {
		return ""
	}
	return namespace
}

// Validate refuses an unknown management mode.
func (c ManagementConfig) Validate() error {
	switch c.Mode {
	case "", ManagementLocal, ManagementShared, ManagementExternal:
		return nil
	default:
		return fmt.Errorf("%s %q is not one of local, shared, external", managementEnvironment, c.Mode)
	}
}
