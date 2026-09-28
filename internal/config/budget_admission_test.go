package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBudgetAdmissionAtomicSources(t *testing.T) {
	for _, source := range []string{"default", "environment", "file"} {
		t.Run(source, func(t *testing.T) {
			values := map[string]string{}
			loader := NewLoader().WithPaths(PathsForConfigDir(t.TempDir())).WithEnvFiles()
			if source == "environment" {
				values["STARPORT_BUDGET_ADMISSION_MODE"] = "atomic"
			}
			if source == "file" {
				path := filepath.Join(t.TempDir(), "config.env")
				require.NoError(t, os.WriteFile(path, []byte("STARPORT_BUDGET_ADMISSION_MODE=atomic\n"), 0600))
				loader.WithEnvFiles(path)
			}
			cfg, err := loader.WithEnvironment(values).Load(t.Context())
			require.NoError(t, err)
			require.Equal(t, "atomic", cfg.BudgetAdmission.Mode)
			cfg.BudgetAdmission.Mode = "local-lease"
			require.ErrorIs(t, cfg.Validate(), ErrBudgetAdmissionMode)
		})
	}
}

func TestBudgetAdmissionRejectsUnsupportedModes(t *testing.T) {
	for _, shared := range []bool{false, true} {
		for _, mode := range []string{"local-lease", "cached", "disabled", "unknown"} {
			name := "standalone/"
			if shared {
				name = "shared/"
			}
			t.Run(name+mode, func(t *testing.T) {
				values := map[string]string{"STARPORT_BUDGET_ADMISSION_MODE": mode}
				if shared {
					values["STARPORT_DEPLOYMENT_ID"] = "budget-mode-test"
					values["STARPORT_STORAGE_MODE"] = "valkey"
					values["STARPORT_STORAGE_VALKEY_URL"] = "redis://127.0.0.1:6379"
					values["STARPORT_STORAGE_SQL_MODE"] = "postgres"
					values["STARPORT_STORAGE_SQL_POSTGRES_URL"] = "postgres://localhost/starport"
					values["STARPORT_FILES_BACKEND"] = "objectstore"
					values["STARPORT_FILES_OBJECT_STORE_BUCKET"] = "fixture-bucket"
					values["STARPORT_FILES_OBJECT_STORE_REGION"] = "fixture-region"
				}
				_, err := NewLoader().WithPaths(PathsForConfigDir(t.TempDir())).WithEnvFiles().WithEnvironment(values).Load(t.Context())
				require.ErrorIs(t, err, ErrBudgetAdmissionMode)
			})
		}
	}
}
