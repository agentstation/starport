package config

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestComposeStorageRecipes loads the actual Compose environments through the
// production loader. No operator dotenv file or service connection is used.
func TestComposeStorageRecipes(t *testing.T) {
	docker, err := exec.LookPath("docker")
	if err != nil {
		t.Skip("Docker Compose is required for recipe qualification")
	}
	probeContext, cancelProbe := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancelProbe()
	if err := exec.CommandContext(probeContext, docker, "compose", "version").Run(); err != nil {
		if os.Getenv("STARPORT_RECIPE_IMAGE") != "" {
			t.Fatalf("container qualification requires Docker Compose: %v", err)
		}
		t.Skipf("UNVERIFIED: Docker Compose is unavailable: %v", err)
	}
	repository, err := filepath.Abs("../..")
	require.NoError(t, err)
	for _, tc := range []struct {
		name, file, dotenv, kv, sql, files string
	}{
		{"local", "docker-compose.yml", ".env", "badger", "sqlite", "filesystem"},
		{"fleet", "docker-compose.fleet.yml", ".env.fleet", "valkey", "postgres", "objectstore"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			directory := t.TempDir()
			fixture := strings.Join([]string{
				"STARPORT_DEPLOYMENT_ID=recipe-fixture",
				"STARPORT_SECURITY_MASTER_KEY=recipe-fixture-not-a-production-secret",
				"STARPORT_STORAGE_VALKEY_URL=valkeys://valkey.example.test:6379/0",
				"STARPORT_STORAGE_SQL_POSTGRES_URL=postgres://fixture@postgres.example.test/starport?sslmode=verify-full",
				"STARPORT_FILES_OBJECT_STORE_BUCKET=recipe-fixture",
				"STARPORT_FILES_OBJECT_STORE_REGION=us-east-1",
				"STARPORT_CATALOG_SOURCE=embedded",
				"STARPORT_CATALOG_ACQUISITION_ENABLED=false",
				"", // Terminate the dotenv file.
			}, "\n")
			file := filepath.Join(directory, tc.dotenv)
			require.NoError(t, os.WriteFile(file, []byte(fixture), 0o600))
			ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, docker, "compose", "--project-directory", directory,
				"--env-file", file, "-f", filepath.Join(repository, tc.file), "config", "--format", "json")
			cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + os.Getenv("HOME")}
			output, err := cmd.CombinedOutput()
			require.NoError(t, err, "%s", output)
			var rendered struct {
				Services map[string]struct {
					Environment map[string]string `json:"environment"`
				} `json:"services"`
			}
			require.NoError(t, json.Unmarshal(output, &rendered))
			values := rendered.Services["starport"].Environment
			cfg, err := NewLoader().WithPaths(PathsForConfigDir(t.TempDir())).WithEnvFiles().WithEnvironment(values).Load(t.Context())
			require.NoError(t, err)
			require.Equal(t, tc.kv, cfg.Storage.Mode)
			require.Equal(t, tc.sql, cfg.Storage.SQL.Mode)
			require.Equal(t, tc.files, cfg.Files.SelectedBackend())
			if tc.name == "local" {
				require.True(t, cfg.RuntimeStorage().Badger.SyncWrites)
			} else {
				require.False(t, cfg.Storage.Valkey.AllowInsecure)
				require.Equal(t, "recipe-fixture", cfg.RuntimeStorage().Valkey.DeploymentID)
				values["STARPORT_STORAGE_SQL_MODE"] = "sqlite"
				_, err := NewLoader().WithPaths(PathsForConfigDir(t.TempDir())).WithEnvFiles().WithEnvironment(values).Load(t.Context())
				require.ErrorContains(t, err, "requires PostgreSQL")
			}
		})
	}
}

// TestContainerRecipePersistence requires a prebuilt image from the same tree.
// CI builds it once and runs the fresh local recipe against real volumes.
func TestContainerRecipePersistence(t *testing.T) {
	image := os.Getenv("STARPORT_RECIPE_IMAGE")
	if image == "" {
		t.Skip("STARPORT_RECIPE_IMAGE is required for container qualification")
	}
	repository, err := filepath.Abs("../..")
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(t.Context(), 4*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "python3", filepath.Join(repository, "scripts/test-storage-recipes.py"), "--image", image)
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, "%s", output)
	var result struct {
		Status       string   `json:"status"`
		Observations []string `json:"observations"`
	}
	require.NoError(t, json.Unmarshal(output, &result))
	require.Equal(t, "PASS", result.Status)
	require.ElementsMatch(t, []string{
		"effective_container_paths", "fresh_start_kv_sql_file_catalog",
		"container_recreation_preserves_records", "cold_backup_restores_into_fresh_volumes",
		"fleet_replica_rotation_survives_replacement", "fleet_replica_local_state_isolated",
	}, result.Observations)
	t.Log(string(output))
}
