package config

import (
	"context"

	"github.com/agentstation/starport/internal/credentials"
)

// InitializeInferencePolicy binds startup to persistent or ephemeral selection policy.
// Call it before provider resolution and before routing can use credential material.
func (c *Config) InitializeInferencePolicy(ctx context.Context, legacy bool) error {
	base := c.providerCredentialResolver()
	mu := c.prepareCredentialResolver()
	mu.Lock()
	defer mu.Unlock()
	if c.inferencePolicyInitialized {
		return nil
	}
	var store credentials.SelectionPolicyStore
	if path := c.InferenceCredentialPolicyDirectory(); path != "" {
		paths := c.EffectivePaths()
		opened, err := credentials.OpenSelectionPolicyStore(ctx, path, credentials.SelectionPolicyOwner{Product: "starport", Deployment: paths.DeploymentID, Instance: paths.InstanceID}, legacy)
		if err != nil {
			return err
		}
		store = opened
	}
	c.credentialResolver = base.ForSelectionPolicy(store, c.CredentialSources.AllowStarmapFallback)
	c.inferencePolicyInitialized = true
	return nil
}
