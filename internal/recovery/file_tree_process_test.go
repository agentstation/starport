package recovery

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/credentials"
	"github.com/stretchr/testify/require"
)

func TestRestoreFileTreeProcessLoss(t *testing.T) {
	if phase := os.Getenv("STARPORT_TEST_TREE_LOSS"); phase != "" {
		encryption, err := credentials.NewEncryptionService([]byte(strings.Repeat("k", 32)))
		require.NoError(t, err)
		source, err := InspectRestoreSource(t.Context(), VerifyRequest{
			Directory: os.Getenv("STARPORT_TEST_TREE_BACKUP"), ManifestSHA256: os.Getenv("STARPORT_TEST_TREE_DIGEST"),
		}, encryption)
		require.NoError(t, err)
		request := FileTreeRequest{Destination: os.Getenv("STARPORT_TEST_TREE_TARGET"), Files: []FileTreeFile{{ArtifactID: "configuration/config.env", Relative: "config.env"}}}
		_, err = source.publishFileTree(t.Context(), request, acceptTestTree, func(current string) error {
			if current == phase {
				os.Exit(77)
			}
			return nil
		})
		t.Fatalf("publication did not reach process-loss boundary: %v", err)
	}
	for _, phase := range []string{"validated", "published"} {
		t.Run(phase, func(t *testing.T) {
			source, request := restoreFileTreeFixture(t)
			ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestRestoreFileTreeProcessLoss$")
			cmd.Env = append(os.Environ(),
				"STARPORT_TEST_TREE_LOSS="+phase,
				"STARPORT_TEST_TREE_BACKUP="+source.request.Directory,
				"STARPORT_TEST_TREE_DIGEST="+source.request.ManifestSHA256,
				"STARPORT_TEST_TREE_TARGET="+request.Destination,
			)
			output, err := cmd.CombinedOutput()
			require.Error(t, err, "%s", output)
			require.NoError(t, ctx.Err(), "%s", output)
			require.NotNil(t, cmd.ProcessState)
			require.Equal(t, 77, cmd.ProcessState.ExitCode(), "%s", output)
			if phase == "validated" {
				require.NoDirExists(t, request.Destination)
			} else {
				require.DirExists(t, request.Destination)
			}
			before, err := filepath.Glob(filepath.Join(filepath.Dir(request.Destination), ".restore-tree-*"))
			require.NoError(t, err)
			if phase == "validated" {
				require.Len(t, before, 1, "process exit must bypass deferred staging cleanup")
			} else {
				require.Empty(t, before)
			}
			result, err := source.PublishFileTree(t.Context(), request, acceptTestTree)
			require.NoError(t, err)
			require.Equal(t, phase == "published", result.Reused)
			require.Equal(t, phase == "validated", result.Published)
			body, err := os.ReadFile(filepath.Join(request.Destination, "config.env"))
			require.NoError(t, err)
			require.Equal(t, "STARPORT_CATALOG_SOURCE=embedded\n", string(body))
			after, err := filepath.Glob(filepath.Join(filepath.Dir(request.Destination), ".restore-tree-*"))
			require.NoError(t, err)
			require.Equal(t, before, after, "retry must preserve abandoned state without an ownership receipt")
		})
	}
}
