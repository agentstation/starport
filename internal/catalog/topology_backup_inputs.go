package catalog

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"maps"
	"path"
	"slices"
	"strings"

	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/agentstation/starmap/runtime"
	"github.com/agentstation/starport/internal/recovery"
	"github.com/agentstation/starport/internal/storage"
)

func (c *topologyBackupCensus) inventory(ctx context.Context, view *recovery.KVSnapshotView, boundary recovery.Record) (*TopologyInventory, error) {
	if err := InspectCapturedCatalog(ctx, view, boundary); err != nil {
		return nil, fmt.Errorf("inspect original catalog: %w", err)
	}
	archive, err := captureFleetHistoricalArchive(ctx, view, boundary)
	if err != nil {
		return nil, err
	}
	if archive != nil {
		return c.fleetInventory(ctx, view, boundary, archive)
	}
	return c.localInventory(ctx, view, boundary)
}

func (c *topologyBackupCensus) fleetInventory(ctx context.Context, view *recovery.KVSnapshotView, boundary recovery.Record, archive *FleetHistoricalArchive) (*TopologyInventory, error) {
	captured := capturedCatalog{view}
	var capsules []TopologyCapsule
	selection := TopologySelection{}
	for _, entry := range archive.data.Inventory.Entries {
		raw, err := readFleetBlob(ctx, archive.data.prefix(), entry, captured.read)
		if err != nil {
			return nil, err
		}
		snapshot, err := decodeFleetBlob(raw, entry)
		if err != nil {
			return nil, err
		}
		inputs, err := runtime.CaptureFleetCatalogRecovery(ctx, snapshot)
		if err != nil {
			return nil, err
		}
		descriptor, err := captured.read(ctx, fleetPublicationKey(archive.data.prefix(), entry.Head), fleetDescriptorMaxBytes)
		if err != nil {
			return nil, err
		}
		capsule := TopologyCapsule{Generation: snapshot.Publication.Generation, Recovery: inputs, SourceOrigin: string(runtime.CatalogRetentionFleetDescriptor), SourceDescriptor: descriptor}
		ref, err := capsule.reference()
		if err != nil {
			return nil, err
		}
		capsules = append(capsules, capsule)
		selection.CapsuleInventory = append(selection.CapsuleInventory, ref)
		if entry.Head == archive.data.Accepted.Head {
			selection.Accepted = ref
		}
		if entry.Head == archive.data.Head {
			selection.Candidate = ref
		}
	}
	// Private source runtime records remain inactive history in a fleet backup.
	// The original fleet publication alone selects its complete catalog inputs.
	return CaptureTopologyInventory(ctx, view, boundary, selection, capsules)
}

func (c *topologyBackupCensus) localInventory(ctx context.Context, view *recovery.KVSnapshotView, boundary recovery.Record) (*TopologyInventory, error) {
	generations, err := readTopologyBackupGenerations(ctx, view)
	if err != nil {
		return nil, fmt.Errorf("read original generations: %w", err)
	}
	capsules := map[TopologyReference]TopologyCapsule{}
	for _, relative := range slices.Sorted(maps.Keys(c.runtime)) {
		file := c.runtime[relative]
		switch {
		case strings.Contains(relative, "/.record-publications/"):
			c.disposition(relative, "inactive-publication-history")
		case strings.HasPrefix(relative, "catalog-runtime/generation-inputs/"):
			capsule, err := c.localDescriptorCapsule(ctx, relative, file, generations)
			if err != nil {
				return nil, err
			}
			if capsule != nil {
				if err := appendTopologyBackupCapsule(capsules, *capsule); err != nil {
					return nil, err
				}
			}
		case strings.HasPrefix(relative, "catalog-runtime/retained-catalog/") && strings.HasSuffix(relative, ".json.gz"):
			capsule, err := c.retainedBackupCapsule(ctx, relative, file, generations)
			if err != nil {
				return nil, err
			}
			if capsule != nil {
				if err := appendTopologyBackupCapsule(capsules, *capsule); err != nil {
					return nil, err
				}
			}
		case strings.HasPrefix(relative, "catalog-runtime/generation-baselines/"):
			if path.Dir(relative) != "catalog-runtime/generation-baselines" || !fleetChunkDigest(strings.TrimSuffix(path.Base(relative), ".json.gz")) || !strings.HasSuffix(relative, ".json.gz") {
				return nil, errors.New("catalog retained baseline has an invalid original name")
			}
		}
	}
	if len(capsules) == 0 {
		return nil, errors.New("catalog backup has no original reconstruction inputs; recapture from its persistent source owner")
	}
	ordered := make([]TopologyCapsule, 0, len(capsules))
	refs := make([]TopologyReference, 0, len(capsules))
	for _, capsule := range capsules {
		ordered = append(ordered, capsule)
	}
	slices.SortFunc(ordered, func(a, b TopologyCapsule) int {
		ar, _ := a.reference()
		br, _ := b.reference()
		return strings.Compare(topologyReferenceKey(ar), topologyReferenceKey(br))
	})
	for _, capsule := range ordered {
		ref, _ := capsule.reference()
		refs = append(refs, ref)
	}
	captured := capturedCatalog{view}
	accepted, err := captured.read(ctx, catalogCurrentGenerationKey, 4096)
	if err != nil {
		return nil, err
	}
	candidate, err := captured.read(ctx, candidateCurrentGenerationKey, 4096)
	if err != nil {
		return nil, err
	}
	selected := TopologySelection{CapsuleInventory: refs}
	selected.Accepted, err = uniqueTopologyBackupReference(ordered, string(accepted))
	if err != nil {
		return nil, err
	}
	selected.Candidate, err = uniqueTopologyBackupReference(ordered, string(candidate))
	if err != nil {
		return nil, err
	}
	return CaptureTopologyInventory(ctx, view, boundary, selected, ordered)
}

func topologyReferenceKey(ref TopologyReference) string {
	return ref.ManifestSHA256 + "/" + ref.InputsSHA256 + "/" + ref.SourceDescriptorSHA256
}
func appendTopologyBackupCapsule(capsules map[TopologyReference]TopologyCapsule, capsule TopologyCapsule) error {
	ref, err := capsule.reference()
	if err != nil {
		return err
	}
	if original, found := capsules[ref]; found {
		if original.SourceOrigin != capsule.SourceOrigin || !sameTopologyGeneration(original.Generation, capsule.Generation) {
			return recovery.ErrConflict
		}
		return nil
	}
	if len(capsules) >= fleetRetentionMaxEntries {
		return storage.ErrValueTooLarge
	}
	capsules[ref] = capsule
	return nil
}
func uniqueTopologyBackupReference(capsules []TopologyCapsule, id string) (TopologyReference, error) {
	var selected TopologyReference
	count := 0
	for _, capsule := range capsules {
		if capsule.Generation.Manifest.GenerationID == id {
			selected, _ = capsule.reference()
			count++
		}
	}
	if count != 1 {
		return TopologyReference{}, errors.New("catalog backup selection has missing or ambiguous original reconstruction inputs")
	}
	return selected, nil
}
func readTopologyBackupGenerations(ctx context.Context, view *recovery.KVSnapshotView) (map[string]catalogs.Generation, error) {
	captured := capturedCatalog{view}
	generations := map[string]catalogs.Generation{}
	err := view.Enumerate(ctx, func(record storage.TransferRecord) error {
		if !stringsHasGenerationPrefix(record.Key) {
			return nil
		}
		id := strings.TrimPrefix(record.Key, catalogGenerationKeyPrefix)
		descriptor, err := decodeGenerationRecord(record.Value, id)
		if err != nil {
			return err
		}
		raw, err := readGenerationPayload(ctx, captured, descriptor, id)
		if err != nil {
			return err
		}
		var generation catalogs.Generation
		if err := json.Unmarshal(raw, &generation, json.RejectUnknownMembers(true)); err != nil {
			return err
		}
		if prior, found := generations[id]; found && !sameTopologyGeneration(prior, generation) {
			return recovery.ErrConflict
		}
		generations[id] = generation
		return nil
	})
	return generations, err
}

func (c *topologyBackupCensus) localDescriptorCapsule(ctx context.Context, relative string, file recovery.BackupFile, generations map[string]catalogs.Generation) (*TopologyCapsule, error) {
	if path.Dir(relative) != "catalog-runtime/generation-inputs" {
		return nil, recovery.ErrConflict
	}
	raw, err := c.source.SelectedPayload(ctx, file.ArtifactID, topologyBackupPayloadLimit(relative))
	if err != nil {
		return nil, err
	}
	proof, err := runtime.InspectCapturedCatalogRecovery(ctx, path.Base(relative), raw, func(ctx context.Context, name string, limit int64) ([]byte, error) {
		baseline, found := c.runtime[path.Join("catalog-runtime/generation-baselines", name)]
		if !found {
			return nil, errors.New("catalog backup omits the original generation baseline")
		}
		c.disposition(baseline.Relative, "original-reconstruction-baseline")
		return c.source.SelectedPayload(ctx, baseline.ArtifactID, limit)
	})
	if err != nil {
		return nil, err
	}
	generation, found := generations[proof.Binding().GenerationID]
	if !found {
		c.disposition(relative, "inactive-orphan-inputs")
		return nil, nil
	}
	inputs, err := proof.RecoveryFor(ctx, generation)
	if err != nil {
		return nil, err
	}
	capsule := TopologyCapsule{Generation: generation, Recovery: inputs, SourceOrigin: string(runtime.CatalogRetentionLocalDescriptor), SourceDescriptor: raw}
	c.disposition(relative, "original-reconstruction-inputs")
	return &capsule, nil
}
func (c *topologyBackupCensus) retainedBackupCapsule(ctx context.Context, relative string, file recovery.BackupFile, generations map[string]catalogs.Generation) (*TopologyCapsule, error) {
	parts := strings.Split(relative, "/")
	if len(parts) != 4 || !fleetChunkDigest(parts[2]) {
		return nil, errors.New("catalog retained envelope has an invalid original name")
	}
	raw, err := c.source.SelectedPayload(ctx, file.ArtifactID, topologyBackupPayloadLimit(relative))
	if err != nil {
		return nil, err
	}
	entry, input, err := runtime.InspectCapturedCatalogRetention(ctx, path.Base(relative), raw)
	if err != nil {
		return nil, err
	}
	generation, found := generations[input.Generation.Manifest.GenerationID]
	if !found {
		c.disposition(relative, "inactive-orphan-retained-capsule")
		return nil, nil
	}
	if !sameTopologyGeneration(generation, input.Generation) {
		return nil, recovery.ErrConflict
	}
	capsule := TopologyCapsule{Generation: generation, Recovery: input.Recovery, SourceOrigin: string(entry.SourceOrigin), SourceDescriptor: input.SourceDescriptor}
	c.disposition(relative, "original-retained-capsule")
	return &capsule, nil
}
