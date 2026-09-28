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
// production loader. It uses no operator dotenv file or service connection.
func TestComposeStorageRecipes(t *testing.T) {
	required := os.Getenv("STARPORT_REQUIRE_COMPOSE") == "1" || os.Getenv("STARPORT_RECIPE_IMAGE") != ""
	docker, err := exec.LookPath("docker")
	if err != nil {
		if required {
			t.Fatalf("recipe qualification requires Docker: %v", err)
		}
		t.Skip("Docker Compose is required for recipe qualification")
	}
	// Preserve host paths for Docker plugin discovery. Operator configuration
	// stays outside the subprocess environment and comes from the fixture.
	var environment []string
	for _, key := range []string{"PATH", "HOME", "USERPROFILE", "SystemRoot", "SystemDrive", "ProgramFiles", "ProgramFiles(x86)", "ProgramData", "APPDATA", "LOCALAPPDATA", "DOCKER_CONFIG", "DOCKER_CLI_PLUGIN_EXTRA_DIRS", "TMP", "TEMP"} {
		if value, exists := os.LookupEnv(key); exists {
			environment = append(environment, key+"="+value)
		}
	}
	probeContext, cancelProbe := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancelProbe()
	probe := exec.CommandContext(probeContext, docker, "compose", "version")
	probe.Env = environment
	if output, err := probe.CombinedOutput(); err != nil {
		if required || probeContext.Err() != nil {
			t.Fatalf("recipe qualification requires Docker Compose: %v: %s", err, output)
		}
		t.Skipf("UNVERIFIED: Docker Compose is unavailable: %v: %s", err, output)
	}
	t.Setenv("STARPORT_DEPLOYMENT_ID", "ambient-deployment-must-not-override-fixture")
	t.Setenv("STARPORT_SECURITY_MASTER_KEY", "ambient-key-must-not-override-fixture")
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
			cmd.Env = environment
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
