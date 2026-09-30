package catalog

import (
	"testing"

	"github.com/agentstation/starmap/pkg/productpaths"
	"github.com/agentstation/starport/internal/recovery"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

func TestRecoveryTopologyRuntimeObservationsRequireCompletePortableCensus(t *testing.T) {
	for _, root := range []string{"/original/runtime", `C:\original\runtime`} {
		t.Run(root, func(t *testing.T) {
			role := recovery.BackupRole{Entry: productpaths.FileEntry{ID: topologyRuntimeEvidenceRole, Location: productpaths.Path{Path: root}}, Capture: "files"}
			role.Observations = []productpaths.FileObservation{{Path: root, State: "present", Kind: "directory"}, {Path: root + "/catalog-runtime/input.json.gz", State: "present", Kind: "file", AccessStatus: "ok"}}
			files := map[string]recovery.BackupFile{"catalog-runtime/input.json.gz": {Relative: "catalog-runtime/input.json.gz"}}
			require.NoError(t, checkTopologyRuntimeObservations(role, files))
			require.ErrorContains(t, checkTopologyRuntimeObservations(role, nil), "omits an observed")
			absent := role
			absent.Observations = absent.Observations[:1]
			require.ErrorContains(t, checkTopologyRuntimeObservations(absent, files), "omit captured")
			duplicate := role
			duplicate.Observations = append(duplicate.Observations[:len(duplicate.Observations):len(duplicate.Observations)], duplicate.Observations[1])
			require.Error(t, checkTopologyRuntimeObservations(duplicate, files))
			for _, changed := range []productpaths.FileObservation{
				{Path: root + "/../input.json.gz", State: "present", Kind: "file"},
				{Path: root + "/catalog-runtime/input.json.gz", State: "present", Kind: "symlink"},
				{Path: root + "/catalog-runtime/input.json.gz", State: "present", Kind: "file", AccessStatus: "conflict"},
			} {
				modified := role
				modified.Observations = []productpaths.FileObservation{changed}
				require.Error(t, checkTopologyRuntimeObservations(modified, files))
			}
		})
	}
}

func TestRecoveryTopologyStageSealRemainsSmallAndBindsOrder(t *testing.T) {
	compiled := &CompiledTopology{stages: make([]topologyBatch, 20000)}
	original := compiled.recoveryStagesDigest()
	require.Len(t, original, 64)
	compiled.stages[0].mutations = []storage.CompareAndSwapMutation{{Key: "one"}}
	changed := compiled.recoveryStagesDigest()
	require.NotEqual(t, original, changed)
	compiled.stages[0], compiled.stages[1] = compiled.stages[1], compiled.stages[0]
	require.NotEqual(t, changed, compiled.recoveryStagesDigest())
	compiled.stages = compiled.stages[:19999]
	require.NotEqual(t, original, compiled.recoveryStagesDigest())
}
