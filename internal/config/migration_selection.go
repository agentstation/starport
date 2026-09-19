package config

import (
	"context"
	"crypto/sha256"
	"fmt"
	"path/filepath"

	catalogconfig "github.com/agentstation/starmap/pkg/catalogs/config"
	"github.com/agentstation/starmap/pkg/productpaths"
	"github.com/joho/godotenv"
)

// VerifySavedRuntimeSelection checks the primary file before migration completion.
// The host repeats this check after opening the replacement runtime.
func (c *Config) VerifySavedRuntimeSelection(ctx context.Context, target, identity string) error {
	if c == nil || !filepath.IsAbs(target) || identity == "" {
		return fmt.Errorf("migration requires an absolute runtime target and scheduler identity")
	}
	paths := c.EffectivePaths()
	if paths.RuntimeDir != target || c.Catalog.CatalogValues()[catalogconfig.SchedulerIdentity] != identity {
		return fmt.Errorf("effective configuration does not select the migration target and scheduler identity")
	}
	var primary map[string]string
	for _, input := range c.fileInputs {
		if !input.loaded {
			continue
		}
		var data []byte
		var err error
		if input.primary {
			data, err = productpaths.ReadConfiguration(ctx, productpaths.ConfigurationInput{Path: input.location.Path, AccessPolicy: input.access, Explicit: c.paths.configExplicit, MaxBytes: 1 << 20})
		} else {
			data, err = productpaths.ReadDotenv(ctx, input.location.Path, 1<<20)
		}
		if err != nil {
			return fmt.Errorf("verify migration configuration input: %w", err)
		}
		if sha256.Sum256(data) != input.digest {
			return fmt.Errorf("migration configuration file changed after selection")
		}
		if input.primary {
			primary, err = godotenv.Unmarshal(string(data))
			if err != nil {
				return fmt.Errorf("parse saved migration configuration: %w", err)
			}
		}
	}
	if primary == nil {
		return fmt.Errorf("save migration selection in the primary configuration file before completion")
	}
	savedTarget := primary[stateDirectoryEnvironment]
	if !filepath.IsAbs(savedTarget) || filepath.Clean(savedTarget) != target || primary["STARPORT_SCHEDULER_IDENTITY"] != identity {
		return fmt.Errorf("primary configuration must save the absolute runtime target and scheduler identity")
	}
	deployment, instance := primary["STARPORT_DEPLOYMENT_ID"], primary["STARPORT_INSTANCE_ID"]
	if deployment == "" {
		deployment = "local"
	}
	if instance == "" {
		instance = "default"
	}
	if deployment != paths.DeploymentID || instance != paths.InstanceID {
		return fmt.Errorf("saved migration owner differs from effective deployment or instance")
	}
	return c.verifySavedMigrationStorage(primary)
}
