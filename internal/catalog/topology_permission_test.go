package catalog

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agentstation/starmap"
	"github.com/agentstation/starmap/pkg/catalogs"
	catalogconfig "github.com/agentstation/starmap/pkg/catalogs/config"
	"github.com/agentstation/starmap/pkg/catalogs/permission"
	catalogstorage "github.com/agentstation/starmap/pkg/catalogs/storage"
	"github.com/agentstation/starmap/runtime"
	"github.com/agentstation/starport/internal/recovery"
	"github.com/stretchr/testify/require"
)

func topologyPermissionFixture(t *testing.T, authority bool) (*CompiledTopology, Settings, permission.ClockReading) {
	t.Helper()
	directory := filepath.Join(t.TempDir(), "runtime")
	settings := identityTestSettings(directory, "", "")
	settings.Values = map[string]string{catalogconfig.NetworkMode: "offline", catalogconfig.SchedulerIdentity: "permission-scheduler"}
	kv := openInMemoryBadger(t)
	var generation catalogs.Generation
	var clock permission.ClockReading
	var capsule TopologyCapsule
	if authority {
		source := authorityAcceptanceSource(t)
		settings = Settings{Source: "starmap", SourceURL: "https://authority.example", SourceStartupPolicy: "require_authority", SourceAuthorityID: "enterprise", SourcePolicyID: "production", StateDirectory: directory, SourceMaxHops: 8, TransferIdleTimeout: time.Minute, TransferMaxDuration: time.Minute}
		r := openAuthorityAcceptance(t, kv, directory, source, "enterprise", "production")
		acceptAuthorityGeneration(t, r)
		var err error
		generation, err = r.AcceptedGeneration(t.Context())
		require.NoError(t, err)
		require.NoError(t, r.Close(t.Context()))
		clock = permission.ClockReading{Time: source.receipt.IssuedAt.Add(time.Second), Known: true}
	} else {
		capsule = topologySmallCapsule(t, "ordinary-permission")
		generation = capsule.Generation
		options, err := settings.starmapOptions()
		require.NoError(t, err)
		baseline := catalogstorage.NewMemory()
		require.NoError(t, baseline.Commit(t.Context(), generation, ""))
		connected, err := runtime.Open(t.Context(), append(options, runtime.WithClientOptions(starmap.WithCatalogStore(baseline)))...)
		require.NoError(t, err)
		_, err = connected.Refresh(t.Context())
		require.NoError(t, err)
		require.NoError(t, connected.Close())
		generation, err = baseline.Current(t.Context())
		require.NoError(t, err)
		accepted, err := NewGenerationStore(kv)
		require.NoError(t, err)
		candidate, err := newCandidateGenerationStore(kv)
		require.NoError(t, err)
		require.NoError(t, accepted.Commit(t.Context(), generation, ""))
		require.NoError(t, candidate.Commit(t.Context(), generation, ""))
	}
	if authority {
		parsed, err := catalogconfig.Parse(settings.catalogValues())
		require.NoError(t, err)
		checksums, err := runtime.CatalogRecoveryChecksums(t.Context(), directory, settings.directoryOwner(), parsed.SchedulerIdentity, generation)
		require.NoError(t, err)
		require.Len(t, checksums, 1)
		input, err := runtime.ReadCatalogRecovery(t.Context(), directory, settings.directoryOwner(), parsed.SchedulerIdentity, generation, checksums[0])
		require.NoError(t, err)
		descriptor, err := os.ReadFile(filepath.Join(directory, "catalog-runtime", "generation-inputs", input.ManifestChecksum+"-"+checksums[0]+".json.gz"))
		require.NoError(t, err)
		capsule = TopologyCapsule{Generation: generation, Recovery: input, SourceOrigin: "local-descriptor", SourceDescriptor: descriptor}
	}
	ref, err := capsule.reference()
	require.NoError(t, err)
	inventory, err := CaptureTopologyInventory(t.Context(), capturedCatalogView(t, kv), recovery.Record{DeploymentID: "source", Epoch: 1}, TopologySelection{Accepted: ref, Candidate: ref, CapsuleInventory: []TopologyReference{ref}}, []TopologyCapsule{capsule})
	require.NoError(t, err)
	request := topologyRequest(TopologyLocalToFleet)
	request.DestinationBoundary.DeploymentID = settings.directoryOwner().Deployment
	request.DestinationIdentity.DeploymentID = request.DestinationBoundary.DeploymentID
	compiled, err := CompileTopologyTransfer(t.Context(), inventory, request, capturedCatalogView(t, openInMemoryBadger(t)))
	require.NoError(t, err)
	return compiled, settings, clock
}

func TestTopologyPermissionReopensOriginalNativeEvidence(t *testing.T) {
	c, settings, clock := topologyPermissionFixture(t, false)
	proof, err := c.InspectPermission(t.Context(), settings, clock)
	require.NoError(t, err, "ordinary permission needs no qualified UTC")
	record, err := proof.Record()
	require.NoError(t, err)
	copy, err := proof.Record()
	require.NoError(t, err)
	copy[0] ^= 1
	require.NotEqual(t, copy, record)
	reopened, err := c.InspectRetainedPermission(t.Context(), settings, clock, record, proof.Digest())
	require.NoError(t, err)
	require.Equal(t, proof.Digest(), reopened.Digest())
	require.NoError(t, c.CheckPermission(t.Context(), settings, clock, reopened))
	for _, format := range []string{"%v", "%+v", "%#v", "%s", "%q"} {
		text := fmt.Sprintf(format, reopened)
		require.NotContains(t, text, settings.StateDirectory)
		require.NotContains(t, text, proof.Digest())
	}
	for _, name := range []string{"source", "owner", "deployment", "scheduler", "directory"} {
		t.Run(name, func(t *testing.T) {
			changed := settings
			switch name {
			case "source":
				changed.Source = "public"
			case "owner":
				changed.InstanceID = "other"
			case "deployment":
				changed.DeploymentID = "other"
			case "scheduler":
				changed.Values = map[string]string{catalogconfig.NetworkMode: "offline", catalogconfig.SchedulerIdentity: "other"}
			case "directory":
				changed.StateDirectory = filepath.Join(t.TempDir(), "absent")
			}
			require.Error(t, c.CheckPermission(t.Context(), changed, clock, proof))
			_, err := c.InspectRetainedPermission(t.Context(), changed, clock, record, proof.Digest())
			require.Error(t, err)
		})
	}
	seed := filepath.Join(settings.StateDirectory, "instance-seed")
	body, err := os.ReadFile(seed)
	require.NoError(t, err)
	require.NoError(t, os.Rename(seed, seed+".original"))
	require.NoError(t, os.WriteFile(seed, body, 0600))
	require.Error(t, c.CheckPermission(t.Context(), settings, clock, proof), "identical bytes with a replacement native identity must refuse")
	still, err := os.ReadFile(seed)
	require.NoError(t, err)
	require.Equal(t, body, still, "passive refusal must not repair evidence")
}

func TestTopologyPermissionAuthorityKeepsOriginalExpiry(t *testing.T) {
	c, settings, clock := topologyPermissionFixture(t, true)
	proof, err := c.InspectPermission(t.Context(), settings, clock)
	require.NoError(t, err)
	record, err := proof.Record()
	require.NoError(t, err)
	clock.Time = clock.Time.Add(time.Minute)
	reopened, err := c.InspectRetainedPermission(t.Context(), settings, clock, record, proof.Digest())
	require.NoError(t, err)
	require.Equal(t, proof.Digest(), reopened.Digest())
	for _, name := range []string{"expired", "unknown", "uncertainty", "different policy", "omitted authority"} {
		t.Run(name, func(t *testing.T) {
			changed, now := settings, clock
			switch name {
			case "expired":
				now.Time = clock.Time.Add(5 * time.Minute)
			case "unknown":
				now.Known = false
			case "uncertainty":
				now.Uncertainty = 5 * time.Minute
			case "different policy":
				changed.SourcePolicyID = "other"
			case "omitted authority":
				changed = identityTestSettings(settings.StateDirectory, "", "")
			}
			_, err := c.InspectRetainedPermission(t.Context(), changed, now, record, proof.Digest())
			require.Error(t, err)
			require.Error(t, c.CheckPermission(t.Context(), changed, now, proof))
		})
	}
}

func TestTopologyPermissionRefusesUnsealedAndUnboundedEvidence(t *testing.T) {
	var missing *CompiledTopology
	_, err := missing.InspectPermission(t.Context(), Settings{}, permission.ClockReading{})
	require.Error(t, err)
	var proof *TopologyPermission
	_, err = proof.Record()
	require.Error(t, err)
	require.Empty(t, proof.Digest())
	c, settings, clock := topologyPermissionFixture(t, false)
	checked, err := c.InspectPermission(t.Context(), settings, clock)
	require.NoError(t, err)
	record, err := checked.Record()
	require.NoError(t, err)
	for _, input := range [][]byte{nil, bytes.Repeat([]byte("x"), topologyPermissionMaxBytes+1), append([]byte(" "), record...), []byte(`{"version":1}`)} {
		_, err := c.InspectRetainedPermission(t.Context(), settings, clock, input, payloadDigest(input))
		require.Error(t, err)
	}
	_, err = c.InspectRetainedPermission(t.Context(), settings, clock, record, strings.Repeat("0", 64))
	require.Error(t, err)
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = c.InspectRetainedPermission(cancelled, settings, clock, record, checked.Digest())
	require.ErrorIs(t, err, context.Canceled)
}
