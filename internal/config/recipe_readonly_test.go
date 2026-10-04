package config

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestContainerRecipeReadOnlyMounts creates both Compose recipes from the
// recipe image. It proves a read-only root file system, tmpfs scratch, and
// only the declared durable mounts. A deployment that moves its data directory
// outside the declared mounts stops at initialization and at start.
func TestContainerRecipeReadOnlyMounts(t *testing.T) {
	image := os.Getenv("STARPORT_RECIPE_IMAGE")
	if image == "" {
		t.Skip("STARPORT_RECIPE_IMAGE is required for read-only recipe qualification")
	}
	repository, err := filepath.Abs("../..")
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(t.Context(), 6*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "python3", filepath.Join(repository, "scripts/test-storage-recipes.py"),
		"--image", image, "--mode", "readonly")
	output, err := cmd.Output()
	require.NoError(t, err, "%s", exitDetail(err))
	var result struct {
		Status       string   `json:"status"`
		Observations []string `json:"observations"`
	}
	require.NoError(t, json.Unmarshal(output, &result))
	require.Equal(t, "PASS", result.Status)
	require.Equal(t, []string{
		"readonly_root_filesystem",
		"declared_writable_mounts_only",
		"scratch_tmpfs_only",
		"write_outside_mounts_refused",
		"undeclared_write_path_stops_start",
		"declared_write_path_starts",
	}, result.Observations)
	t.Log(string(output))
}
