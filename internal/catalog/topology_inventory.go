package catalog

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/v2"
	"errors"
	"fmt"
	"hash"
	"slices"

	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/agentstation/starmap/runtime"
	"github.com/agentstation/starport/internal/recovery"
	"github.com/agentstation/starport/internal/storage"
)

// TopologyReference names exact catalog and reconstruction evidence, including its original descriptor.
type TopologyReference struct {
	ManifestSHA256         string `json:"manifest_sha256"`
	InputsSHA256           string `json:"inputs_sha256"`
	SourceDescriptorSHA256 string `json:"source_descriptor_sha256"`
}

// TopologyCapsule preserves original reconstruction settings inside Recovery.
// SourceDescriptor contains the exact source descriptor, never a target configuration substitute.
type TopologyCapsule struct {
	Generation       catalogs.Generation     `json:"generation"`
	Recovery         runtime.CatalogRecovery `json:"recovery"`
	SourceOrigin     string                  `json:"source_origin"`
	SourceDescriptor []byte                  `json:"source_descriptor,omitempty"`
}

// TopologySelection distinguishes accepted serving facts from the latest candidate.
// CapsuleInventory must come from the complete original producer-owned retained-input census.
// Callers cannot substitute an arbitrary operator-supplied census.
type TopologySelection struct {
	Accepted         TopologyReference   `json:"accepted"`
	Candidate        TopologyReference   `json:"candidate"`
	CapsuleInventory []TopologyReference `json:"capsule_inventory"`
}

type topologyInventoryData struct {
	Boundary  recovery.Record        `json:"boundary"`
	Selection TopologySelection      `json:"selection"`
	Capsules  []TopologyCapsule      `json:"capsules"`
	History   []GenerationIndexEntry `json:"history"`
	Archive   *fleetHistoricalData   `json:"archive,omitempty"`
}

// TopologyInventory retains checked source facts privately. It grants no serving permission.
type TopologyInventory struct {
	data   topologyInventoryData
	digest string
}

// Format omits private catalog inputs from diagnostics.
func (i TopologyInventory) Format(state fmt.State, _ rune) {
	_, _ = fmt.Fprint(state, "catalog topology inventory (private)")
}

// CaptureTopologyInventory checks source selection and every retained generation against exact capsules.
// It opens no runtime. Callers must supply the exact original producer-owned descriptor census.
// A KV snapshot proves generation coverage. It cannot prove that a local descriptor census is complete.
func CaptureTopologyInventory(ctx context.Context, source *recovery.KVSnapshotView, boundary recovery.Record, selection TopologySelection, capsules []TopologyCapsule) (*TopologyInventory, error) {
	if source == nil {
		return nil, errors.New("catalog topology requires a captured source")
	}
	return captureTopologyInventory(ctx, source, boundary, selection, capsules)
}

func captureTopologyInventory(ctx context.Context, source capturedCatalogRecords, boundary recovery.Record, selection TopologySelection, capsules []TopologyCapsule) (*TopologyInventory, error) {
	return captureTopologyInventoryLimit(ctx, source, boundary, selection, capsules, fleetRetentionMaxBytes)
}

func captureTopologyInventoryLimit(ctx context.Context, source capturedCatalogRecords, boundary recovery.Record, selection TopologySelection, capsules []TopologyCapsule, limit int64) (*TopologyInventory, error) {
	if err := inspectCapturedCatalog(ctx, source, boundary); err != nil {
		return nil, err
	}
	if len(capsules) == 0 || len(capsules) > fleetRetentionMaxEntries || len(selection.CapsuleInventory) != len(capsules) {
		return nil, errors.New("catalog topology requires its complete bounded capsule inventory")
	}
	data := topologyInventoryData{Boundary: boundary, Selection: selection, Capsules: capsules}
	encoded, err := json.Marshal(data, json.Deterministic(true))
	if err != nil {
		return nil, err
	}
	if int64(len(encoded)) > limit {
		return nil, storage.ErrValueTooLarge
	}
	// A round trip detaches all caller-owned slices, maps and nested catalog records.
	data = topologyInventoryData{}
	if err = json.Unmarshal(encoded, &data); err != nil {
		return nil, err
	}
	refs := make(map[TopologyReference]bool, len(capsules))
	generations := map[string]catalogs.Generation{}
	for index, capsule := range data.Capsules {
		ref, err := capsule.reference()
		if err != nil {
			return nil, err
		}
		if ref != data.Selection.CapsuleInventory[index] || refs[ref] {
			return nil, errors.New("catalog capsule census is incomplete, duplicated or reordered")
		}
		refs[ref] = true
		if prior, found := generations[capsule.Generation.Manifest.GenerationID]; found && !sameTopologyGeneration(prior, capsule.Generation) {
			return nil, errors.New("catalog generation identity contains conflicting content")
		}
		generations[capsule.Generation.Manifest.GenerationID] = capsule.Generation
	}
	if !refs[selection.Accepted] || !refs[selection.Candidate] {
		return nil, errors.New("catalog selections require their original reconstruction capsule")
	}
	captured := capturedCatalog{source}
	archive, err := captureFleetHistoricalArchive(ctx, source, boundary)
	if err != nil {
		return nil, err
	}
	if archive != nil {
		data.Archive = archive.data
		data.History = slices.Clone(archive.data.Accepted.History)
		if err = checkFleetTopology(ctx, captured, &data, generations); err != nil {
			return nil, err
		}
	} else if err = readLocalTopology(ctx, captured, &data, generations); err != nil {
		return nil, err
	}

	entries := map[string]GenerationIndexEntry{}
	for id, generation := range generations {
		entry, err := fleetIndexEntry(generation)
		if err != nil {
			return nil, err
		}
		entries[id] = entry
	}
	if err = validateCapturedHistory(data.History, entries); err != nil {
		return nil, err
	}
	digest, err := topologyIdentityDigest(data, limit)
	if err != nil {
		return nil, err
	}
	return &TopologyInventory{data: data, digest: digest}, nil
}

func topologyIdentityDigest(data topologyInventoryData, limit int64) (string, error) {
	digest := sha256.New()
	output := &topologyIdentityWriter{digest: digest, remaining: limit}
	if err := json.MarshalWrite(output, data, json.Deterministic(true)); err != nil {
		return "", err
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}

type topologyIdentityWriter struct {
	digest    hash.Hash
	remaining int64
}

func (w *topologyIdentityWriter) Write(data []byte) (int, error) {
	if int64(len(data)) > w.remaining {
		return 0, storage.ErrValueTooLarge
	}
	n, err := w.digest.Write(data)
	w.remaining -= int64(n)
	return n, err
}

func readLocalTopology(ctx context.Context, c capturedCatalog, data *topologyInventoryData, generations map[string]catalogs.Generation) error {
	accepted, err := c.read(ctx, catalogCurrentGenerationKey, 4096)
	if err != nil {
		return err
	}
	candidate, err := c.read(ctx, candidateCurrentGenerationKey, 4096)
	if err != nil {
		return err
	}
	if string(accepted) != topologyGenerationID(*data, data.Selection.Accepted) || string(candidate) != topologyGenerationID(*data, data.Selection.Candidate) {
		return errors.New("catalog source selection differs from the captured local catalog")
	}
	history, err := c.read(ctx, catalogGenerationIndexKey, fleetDescriptorMaxBytes)
	if err != nil {
		return err
	}
	if err = json.Unmarshal(history, &data.History, json.RejectUnknownMembers(true)); err != nil {
		return err
	}
	seen := map[string]bool{}
	err = c.records.Enumerate(ctx, func(record storage.TransferRecord) error {
		if !stringsHasGenerationPrefix(record.Key) {
			return nil
		}
		entry, err := c.generation(ctx, record)
		if err != nil {
			return err
		}
		generation, found := generations[entry.GenerationID]
		if !found {
			return errors.New("retained generation has no original reconstruction capsule")
		}
		descriptor, err := decodeGenerationRecord(record.Value, entry.GenerationID)
		if err != nil {
			return err
		}
		payload, err := readGenerationPayload(ctx, c, descriptor, entry.GenerationID)
		if err != nil {
			return err
		}
		var original catalogs.Generation
		if err = json.Unmarshal(payload, &original); err != nil {
			return err
		}
		if !sameTopologyGeneration(original, generation) {
			return errors.New("catalog capsule manifest differs from original retained generation")
		}
		seen[entry.GenerationID] = true
		expected, err := fleetIndexEntry(generation)
		if err != nil {
			return err
		}
		if !sameCapturedEntry(entry, expected) {
			return errors.New("catalog capsule differs from original retained generation")
		}
		return nil
	})
	if err != nil {
		return err
	}
	if len(seen) != len(generations) {
		return errors.New("catalog capsule contains a generation absent from its source")
	}
	return nil
}

func stringsHasGenerationPrefix(key string) bool {
	return len(key) >= len(catalogGenerationKeyPrefix) && key[:len(catalogGenerationKeyPrefix)] == catalogGenerationKeyPrefix
}

func (c TopologyCapsule) reference() (TopologyReference, error) {
	if err := c.Recovery.Validate(c.Generation); err != nil {
		return TopologyReference{}, err
	}
	switch c.SourceOrigin {
	case "local-descriptor", "fleet-publication":
		if len(c.SourceDescriptor) == 0 || len(c.SourceDescriptor) > runtime.MaxFleetRecoveryBytes {
			return TopologyReference{}, errors.New("catalog capsule requires its original bounded descriptor")
		}
	case "no-descriptor":
		if len(c.SourceDescriptor) != 0 {
			return TopologyReference{}, errors.New("descriptor-free capsule contains a descriptor")
		}
	default:
		return TopologyReference{}, errors.New("catalog capsule has an unsupported original source")
	}
	ref := TopologyReference{ManifestSHA256: c.Recovery.ManifestChecksum, InputsSHA256: c.Recovery.Inputs.Checksum}
	if len(c.SourceDescriptor) > 0 {
		ref.SourceDescriptorSHA256 = payloadDigest(c.SourceDescriptor)
	}
	return ref, nil
}

func topologyGenerationID(data topologyInventoryData, ref TopologyReference) string {
	for _, capsule := range data.Capsules {
		actual, _ := capsule.reference()
		if actual == ref {
			return capsule.Generation.Manifest.GenerationID
		}
	}
	return ""
}

func sameTopologyGeneration(a, b catalogs.Generation) bool {
	aa, err := json.Marshal(a, json.Deterministic(true))
	if err != nil {
		return false
	}
	bb, err := json.Marshal(b, json.Deterministic(true))
	return err == nil && bytes.Equal(aa, bb)
}

func hasTopologyRecovery(capsules []TopologyCapsule, snapshot runtime.FleetSnapshot) bool {
	for _, capsule := range capsules {
		if capsule.Generation.Manifest.GenerationID == snapshot.Head.GenerationID && capsule.Recovery.Inputs.Checksum == snapshot.Publication.Recovery.Checksum && bytes.Equal(capsule.Recovery.Inputs.Data, snapshot.Publication.Recovery.Data) {
			return true
		}
	}
	return false
}

func checkFleetTopology(ctx context.Context, captured capturedCatalog, data *topologyInventoryData, generations map[string]catalogs.Generation) error {

	matched := map[TopologyReference]bool{}
	for _, blob := range data.Archive.Inventory.Entries {
		payload, err := readFleetBlob(ctx, data.Archive.prefix(), blob, captured.read)
		if err != nil {
			return err
		}
		snapshot, err := decodeFleetBlob(payload, blob)
		if err != nil {
			return err
		}
		actual, found := generations[blob.Head.GenerationID]
		if !found || !sameTopologyGeneration(actual, snapshot.Publication.Generation) || !hasTopologyRecovery(data.Capsules, snapshot) {
			return errors.New("fleet publication lacks its original catalog capsule")
		}
		descriptor, err := captured.read(ctx, fleetPublicationKey(data.Archive.prefix(), blob.Head), fleetDescriptorMaxBytes)
		if err != nil {
			return err
		}
		for _, capsule := range data.Capsules {
			if capsule.Generation.Manifest.GenerationID == blob.Head.GenerationID && capsule.Recovery.Inputs.Checksum == snapshot.Publication.Recovery.Checksum && capsule.SourceOrigin == "fleet-publication" && bytes.Equal(capsule.SourceDescriptor, descriptor) {
				ref, _ := capsule.reference()
				matched[ref] = true
			}
		}
	}
	if len(matched) != len(data.Capsules) {
		return errors.New("fleet capsule lacks its exact original publication descriptor")
	}
	if err := checkFleetSelectionReference(ctx, captured, data, data.Selection.Accepted, data.Archive.Accepted.Head); err != nil {
		return err
	}
	return checkFleetSelectionReference(ctx, captured, data, data.Selection.Candidate, data.Archive.Head)
}

func checkFleetSelectionReference(ctx context.Context, captured capturedCatalog, data *topologyInventoryData, ref TopologyReference, head runtime.FleetHead) error {
	if topologyGenerationID(*data, ref) != head.GenerationID || ref.InputsSHA256 != head.RecoveryChecksum {
		return errors.New("catalog source selection differs from the captured fleet publication")
	}
	descriptor, err := captured.read(ctx, fleetPublicationKey(data.Archive.prefix(), head), fleetDescriptorMaxBytes)
	if err != nil {
		return err
	}
	if ref.SourceDescriptorSHA256 != payloadDigest(descriptor) {
		return errors.New("catalog source selection differs from its original publication descriptor")
	}
	return nil
}

// Format omits the private descriptor and reconstruction bytes.
func (c TopologyCapsule) Format(state fmt.State, _ rune) {
	_, _ = fmt.Fprint(state, "catalog capsule (private)")
}

// Format omits private reconstruction identities.
func (r TopologyReference) Format(state fmt.State, _ rune) {
	_, _ = fmt.Fprint(state, "catalog reference (private)")
}

// Format omits private selection and inventory evidence.
func (s TopologySelection) Format(state fmt.State, _ rune) {
	_, _ = fmt.Fprint(state, "catalog selection (private)")
}
