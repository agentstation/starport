package config

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/joho/godotenv"
	"github.com/stretchr/testify/require"
)

func TestSavedRuntimeMigrationSelection(t *testing.T) {
	for _, mode := range []string{"saved", "changed", "environment-only", "owner-override", "identity-override", "dotenv-only", "deleted"} {
		t.Run(mode, func(t *testing.T) {
			home := t.TempDir()
			file := filepath.Join(home, "config.env")
			target := filepath.Join(home, "replacement")
			identity := "retained-scheduler"
			values := map[string]string{"STARPORT_CATALOG_STATE_DIR": target, "STARPORT_SCHEDULER_IDENTITY": identity, "STARPORT_DEPLOYMENT_ID": "team", "STARPORT_INSTANCE_ID": "gateway"}
			environment := map[string]string{"STARPORT_HOME": home, "STARPORT_CONFIG_FILE": file}
			if mode == "environment-only" {
				for key, value := range values {
					environment[key] = value
				}
				clear(values)
			}
			contents, err := godotenv.Marshal(values)
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(file, []byte(contents+"\n"), 0600))
			if mode == "owner-override" {
				environment["STARPORT_INSTANCE_ID"] = "other"
			}
			if mode == "identity-override" {
				environment["STARPORT_SCHEDULER_IDENTITY"] = "other"
			}
			loader := NewLoader().WithEnvironment(environment)
			if mode == "dotenv-only" {
				loader = loader.WithEnvFiles(file)
			}
			cfg, err := loader.Load(t.Context())
			require.NoError(t, err)
			if mode == "changed" {
				require.NoError(t, os.WriteFile(file, []byte(contents+"\n# changed\n"), 0600))
			}
			if mode == "deleted" {
				require.NoError(t, os.Remove(file))
			}
			err = cfg.VerifySavedRuntimeSelection(t.Context(), target, identity)
			if mode == "saved" {
				require.NoError(t, err)
			} else {
				require.Error(t, err, fmt.Sprintf("mode %s must not complete migration", mode))
			}
			require.NoDirExists(t, target, "selection checks must not create runtime state")
		})
	}
}
