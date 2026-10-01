package catalog

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/agentstation/starmap/pkg/productfiles"
	"github.com/agentstation/starmap/runtime"
	"github.com/agentstation/starport/internal/recovery"
	"github.com/stretchr/testify/require"
)

func TestTopologyRetentionComparisonsBindOriginalInventory(t *testing.T) {
	inventory, view, original := topologyLocalFixture(t)
	compiled, err := CompileTopologyTransfer(t.Context(), inventory, topologyRequest(TopologyLocalToFleet), view)
	require.NoError(t, err)
	require.NoError(t, compiled.checkRetentionComparisons())
	require.Len(t, compiled.retentionComparisons, len(original))
	original[0].Recovery.Inputs.Data[0] ^= 1
	original[0].Generation.Payload[0] ^= 1
	rebuilt, err := CompileTopologyTransfer(t.Context(), inventory, topologyRequest(TopologyLocalToFleet), view)
	require.NoError(t, err)
	for index, comparison := range compiled.retentionComparisons {
		require.Equal(t, 1, comparison.Usage().Inputs())
		require.Equal(t, comparison.Usage(), rebuilt.retentionComparisons[index].Usage())
		require.NotSame(t, comparison, rebuilt.retentionComparisons[index])
	}
}

func TestTopologyRetentionComparisonsRefuseIncompletePrivateCoverage(t *testing.T) {
	inventory, view, _ := topologyLocalFixture(t)
	compiled, err := CompileTopologyTransfer(t.Context(), inventory, topologyRequest(TopologyLocalToFleet), view)
	require.NoError(t, err)
	for _, change := range []string{"nil", "short", "long", "nil-entry", "empty-entry"} {
		t.Run(change, func(t *testing.T) {
			copy := *compiled
			copy.retentionComparisons = slices.Clone(compiled.retentionComparisons)
			switch change {
			case "nil":
				copy.retentionComparisons = nil
			case "short":
				copy.retentionComparisons = copy.retentionComparisons[:1]
			case "long":
				copy.retentionComparisons = append(copy.retentionComparisons, copy.retentionComparisons[0])
			case "nil-entry":
				copy.retentionComparisons[0] = nil
			case "empty-entry":
				copy.retentionComparisons[0] = &runtime.CatalogRetentionComparison{}
			}
			target := TopologyRuntimeTarget{Directory: filepath.Join(t.TempDir(), "must-remain-absent")}
			_, err := copy.RetainAndMaterialize(t.Context(), target)
			require.ErrorIs(t, err, recovery.ErrConflict)
			err = copy.CheckMaterialization(t.Context(), target, &TopologyMaterialization{topology: copy.digest})
			require.ErrorIs(t, err, recovery.ErrConflict)
			_, err = os.Stat(target.Directory)
			require.ErrorIs(t, err, os.ErrNotExist)
		})
	}
}

func TestTopologyRetentionComparisonsRefuseMalformedOriginalInput(t *testing.T) {
	capsule := topologySmallCapsule(t, "malformed")
	capsule.Recovery.Inputs.Data = []byte("not a gzip recovery record")
	capsule.Recovery.Inputs.Checksum = payloadDigest(capsule.Recovery.Inputs.Data)
	_, err := inspectTopologyRetentionComparisons(t.Context(), []TopologyCapsule{capsule})
	require.Error(t, err)
	_, err = inspectTopologyRetentionComparisons(t.Context(), nil)
	require.ErrorIs(t, err, recovery.ErrConflict)
	_, err = inspectTopologyRetentionComparisons(nil, []TopologyCapsule{capsule})
	require.ErrorIs(t, err, recovery.ErrConflict)
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = inspectTopologyRetentionComparisons(canceled, []TopologyCapsule{topologySmallCapsule(t, "canceled")})
	require.ErrorIs(t, err, context.Canceled)
}

// topologyRetainedComparisonFixture creates private stopped-owner metadata and real native retention.
// It creates no serving runtime, selection, provider acquisition, or permission checkpoint.
func topologyRetainedComparisonFixture(t *testing.T) (*CompiledTopology, TopologyRuntimeTarget, *TopologyMaterialization) {
	t.Helper()
	inventory, view, _ := topologyLocalFixture(t)
	compiled, err := CompileTopologyTransfer(t.Context(), inventory, topologyRequest(TopologyLocalToFleet), view)
	require.NoError(t, err)
	path := filepath.Join(t.TempDir(), "stopped-owner")
	directory, err := productfiles.CreateDirectory(path)
	require.NoError(t, err)
	owner := runtime.DirectoryOwner{Product: "starport", Deployment: "destination", Instance: "default"}
	encoded, err := json.Marshal(struct {
		SchemaVersion int `json:"schema_version"`
		runtime.DirectoryOwner
	}{1, owner}, jsontext.WithIndent("  "))
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(path, "owner.json"), append(encoded, '\n'), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(path, "instance-seed"), []byte(strings.Repeat("a", 32)), 0600))
	_, err = directory.CreateChild("catalog-runtime")
	require.NoError(t, err)
	target := TopologyRuntimeTarget{Directory: path, Owner: owner}
	request := runtime.CatalogRetentionRequest{Directory: path, Owner: owner, TransferID: compiled.digest, OperationID: topologyBatchOperation(compiled.digest, 0)}
	for _, capsule := range compiled.inventory.Capsules {
		ref, err := capsule.reference()
		require.NoError(t, err)
		request.Manifest = append(request.Manifest, runtime.CatalogRetentionEntry{ManifestSHA256: ref.ManifestSHA256, InputsSHA256: ref.InputsSHA256, SourceOrigin: runtime.CatalogRetentionOrigin(capsule.SourceOrigin), SourceDescriptorSHA256: ref.SourceDescriptorSHA256})
		request.Inputs = append(request.Inputs, runtime.CatalogRetentionInput{CatalogMaterializationInput: runtime.CatalogMaterializationInput{Generation: capsule.Generation, Recovery: capsule.Recovery}, SourceDescriptor: capsule.SourceDescriptor})
	}
	receipt, err := runtime.RetainCatalogRecovery(t.Context(), request)
	require.NoError(t, err)
	return compiled, target, &TopologyMaterialization{topology: compiled.digest, receipts: []runtime.CatalogRetentionReceipt{receipt}}
}

func TestTopologyRetentionComparisonsCheckActualNativeRetention(t *testing.T) {
	compiled, target, proof := topologyRetainedComparisonFixture(t)
	batches, err := compiled.checkRetainedCapsules(t.Context(), target, proof)
	require.NoError(t, err)
	require.Len(t, batches, 1)
	// Historical input evidence cannot replace an actual completed selected replay.
	require.Error(t, compiled.CheckMaterialization(t.Context(), target, proof))
	_, err = os.Stat(filepath.Join(target.Directory, "catalog-runtime", "recovery-baseline.json"))
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestTopologyRetentionComparisonsRefuseChangedNativeEvidence(t *testing.T) {
	for _, change := range []string{"reordered-proof", "seed", "envelope", "receipt", "owner"} {
		t.Run(change, func(t *testing.T) {
			compiled, target, proof := topologyRetainedComparisonFixture(t)
			_, err := compiled.checkRetainedCapsules(t.Context(), target, proof)
			require.NoError(t, err)
			var changedPath string
			var changedBytes []byte
			switch change {
			case "reordered-proof":
				compiled.retentionComparisons[0], compiled.retentionComparisons[1] = compiled.retentionComparisons[1], compiled.retentionComparisons[0]
			case "seed":
				changedPath = filepath.Join(target.Directory, "instance-seed")
				changedBytes = []byte(strings.Repeat("b", 32))
			case "envelope":
				changedPath = filepath.Join(target.Directory, "catalog-runtime", "retained-catalog", payloadDigest([]byte(compiled.digest)), proof.receipts[0].Records[0].RecordSHA256+".json.gz")
				changedBytes, err = os.ReadFile(changedPath)
				require.NoError(t, err)
				changedBytes[0] ^= 1
			case "receipt":
				proof.receipts[0].Validation = "changed"
			case "owner":
				target.Owner.Deployment += "-changed"
			}
			if changedPath != "" {
				require.NoError(t, os.WriteFile(changedPath, changedBytes, 0600))
			}
			_, err = compiled.checkRetainedCapsules(t.Context(), target, proof)
			require.Error(t, err)
			if changedPath != "" {
				actual, err := os.ReadFile(changedPath)
				require.NoError(t, err)
				require.Equal(t, changedBytes, actual, "passive validation cannot repair changed native evidence")
			}
		})
	}
}
