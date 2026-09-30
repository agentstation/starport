package catalog

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"

	"github.com/agentstation/starmap/runtime"
	"github.com/agentstation/starport/internal/recovery"
	"github.com/agentstation/starport/internal/storage"
)

// TopologyRuntimeTarget selects the existing stopped producer-owned input directory.
// It does not copy captured target settings or establish new serving authority.
type TopologyRuntimeTarget struct {
	Directory         string
	Owner             runtime.DirectoryOwner
	SchedulerIdentity string
}

// TopologyMaterialization binds actual producer retention and target semantic replay.
// Callers cannot replace its private state with digest-only success reports.
type TopologyMaterialization struct {
	topology string
	digest   string
	receipts []runtime.CatalogRetentionReceipt
	selected runtime.CatalogMaterializationReceipt
}

// Format omits native identities and private receipt evidence.
func (p TopologyMaterialization) Format(state fmt.State, _ rune) {
	_, _ = fmt.Fprint(state, "catalog materialization (private)")
}

// RetainAndMaterialize preserves every original capsule before selecting current producer inputs.
// The producer checks the structure of historical inputs. It replays both selections under target settings.
// This completes before catalog selection and authorization revision replacement.
func (c *CompiledTopology) RetainAndMaterialize(ctx context.Context, target TopologyRuntimeTarget, options ...runtime.Option) (*TopologyMaterialization, error) {
	if c == nil || ctx == nil {
		return nil, recovery.ErrConflict
	}
	if err := c.checkRetentionComparisons(); err != nil {
		return nil, err
	}
	if err := c.validateSelectedReplay(ctx, options, TopologyReference{}); err != nil {
		return nil, err
	}
	manifest := make([]runtime.CatalogRetentionEntry, 0, len(c.inventory.Capsules))
	for _, capsule := range c.inventory.Capsules {
		ref, err := capsule.reference()
		if err != nil {
			return nil, err
		}
		entry := runtime.CatalogRetentionEntry{ManifestSHA256: ref.ManifestSHA256, InputsSHA256: ref.InputsSHA256, SourceOrigin: runtime.CatalogRetentionOrigin(capsule.SourceOrigin), SourceDescriptorSHA256: ref.SourceDescriptorSHA256}
		manifest = append(manifest, entry)
	}
	proof := &TopologyMaterialization{topology: c.digest}
	var batches []runtime.CatalogRetentionBatch
	for start := 0; start < len(c.inventory.Capsules); {
		request := runtime.CatalogRetentionRequest{Directory: target.Directory, Owner: target.Owner, SchedulerIdentity: target.SchedulerIdentity, OperationID: topologyBatchOperation(c.digest, len(batches)), TransferID: c.digest, Manifest: manifest, BatchStart: start}
		var rawBytes, decodedBytes int64
		for position := start; position < len(c.inventory.Capsules); position++ {
			capsule := c.inventory.Capsules[position]
			input := runtime.CatalogRetentionInput{CatalogMaterializationInput: runtime.CatalogMaterializationInput{Generation: capsule.Generation, Recovery: capsule.Recovery}, SourceDescriptor: capsule.SourceDescriptor}
			usage := c.retentionComparisons[position].Usage()
			if len(request.Inputs) > 0 && (len(request.Inputs) >= runtime.MaxCatalogRetentionBatchInputs || usage.RawBytes() > runtime.MaxCatalogRetentionBatchBytes-rawBytes || usage.DecodedBytes() > runtime.MaxCatalogRetentionBatchBytes-decodedBytes) {
				break
			}
			rawBytes += usage.RawBytes()
			decodedBytes += usage.DecodedBytes()
			request.Inputs = append(request.Inputs, input)
		}
		receipt, err := runtime.RetainCatalogRecovery(ctx, request)
		if err != nil {
			return nil, err
		}
		if receipt.TransferID != c.digest || receipt.BatchStart != start || len(receipt.Records) != len(request.Inputs) || receipt.Validation != "structural" {
			return nil, errors.New("producer retained a different catalog capsule range")
		}
		for index, record := range receipt.Records {
			if record.Entry != manifest[start+index] {
				return nil, recovery.ErrConflict
			}
		}
		encoded, err := json.Marshal(receipt, json.Deterministic(true))
		if err != nil {
			return nil, err
		}
		batches = append(batches, runtime.CatalogRetentionBatch{OperationID: receipt.OperationID, ReceiptSHA256: payloadDigest(encoded)})
		proof.receipts = append(proof.receipts, receipt)
		start += len(request.Inputs)
	}
	selectedRef := c.inventory.Selection.Candidate
	if c.request.Direction == TopologyFleetToLocal || c.request.Direction == TopologyLocalRestore {
		selectedRef = c.inventory.Selection.Accepted
	}
	selected := -1
	for index, capsule := range c.inventory.Capsules {
		ref, _ := capsule.reference()
		if ref == selectedRef {
			selected = index
		}
	}
	if selected < 0 {
		return nil, recovery.ErrConflict
	}
	request := runtime.CatalogRetainedMaterializationRequest{Directory: target.Directory, Owner: target.Owner, SchedulerIdentity: target.SchedulerIdentity, OperationID: "catalog-materialize-" + c.digest, TransferID: c.digest, Batches: batches, Selected: selected}
	receipt, err := runtime.MaterializeRetainedCatalogRecovery(ctx, request, options...)
	if err != nil {
		return nil, err
	}
	if receipt.SelectedManifestSHA256 != selectedRef.ManifestSHA256 || receipt.SelectedInputsSHA256 != selectedRef.InputsSHA256 {
		return nil, errors.New("producer materialization differs from selected catalog inputs")
	}
	proof.selected = receipt
	encoded, err := json.Marshal(struct {
		Topology string
		Retained []runtime.CatalogRetentionReceipt
		Selected runtime.CatalogMaterializationReceipt
	}{proof.topology, proof.receipts, proof.selected}, json.Deterministic(true))
	if err != nil {
		return nil, err
	}
	proof.digest = payloadDigest(encoded)
	return proof, nil
}

func (c *CompiledTopology) validateSelectedReplay(ctx context.Context, options []runtime.Option, validated TopologyReference) error {
	// The producer validates its materialized selection. Check each remaining selection once.
	for _, ref := range c.replayReferences(validated) {
		for _, capsule := range c.inventory.Capsules {
			actual, _ := capsule.reference()
			if actual == ref {
				if err := runtime.ValidateCatalogReplay(ctx, capsule.Generation, capsule.Recovery, options...); err != nil {
					return err
				}
				break
			}
		}
	}
	return nil
}

func (c *CompiledTopology) replayReferences(validated TopologyReference) []TopologyReference {
	refs := make([]TopologyReference, 0, 2)
	for _, ref := range []TopologyReference{c.inventory.Selection.Accepted, c.inventory.Selection.Candidate} {
		if ref == validated || len(refs) > 0 && refs[0] == ref {
			continue
		}
		refs = append(refs, ref)
	}
	return refs
}

type topologyMaterializationRecord struct {
	Version  int                                   `json:"version"`
	Topology string                                `json:"topology"`
	Retained []runtime.CatalogRetentionReceipt     `json:"retained"`
	Selected runtime.CatalogMaterializationReceipt `json:"selected"`
}

// Record exports bounded private evidence for the recovery owner's immutable journal.
// It contains no catalog bytes, raw credentials, or storage mutation authority.
func (p *TopologyMaterialization) Record() ([]byte, error) {
	if p == nil {
		return nil, recovery.ErrConflict
	}
	data, err := json.Marshal(topologyMaterializationRecord{1, p.topology, p.receipts, p.selected}, json.Deterministic(true))
	if err != nil {
		return nil, err
	}
	if len(data) > fleetDescriptorMaxBytes {
		return nil, storage.ErrValueTooLarge
	}
	return data, nil
}

// InspectMaterialization reopens journal evidence through actual producer-owned records.
// Missing completion, changed target inputs, or incomplete coverage refuse without repair.
func (c *CompiledTopology) InspectMaterialization(ctx context.Context, target TopologyRuntimeTarget, data []byte, options ...runtime.Option) (*TopologyMaterialization, error) {
	if c == nil || len(data) == 0 || len(data) > fleetDescriptorMaxBytes {
		return nil, recovery.ErrConflict
	}
	var record topologyMaterializationRecord
	if err := json.Unmarshal(data, &record, json.RejectUnknownMembers(true)); err != nil {
		return nil, err
	}
	if record.Version != 1 || record.Topology != c.digest || len(record.Retained) == 0 || len(record.Retained) > fleetRetentionMaxEntries {
		return nil, recovery.ErrConflict
	}
	proof := &TopologyMaterialization{topology: record.Topology, receipts: record.Retained, selected: record.Selected}
	if err := c.CheckMaterialization(ctx, target, proof, options...); err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(struct {
		Topology string
		Retained []runtime.CatalogRetentionReceipt
		Selected runtime.CatalogMaterializationReceipt
	}{proof.topology, proof.receipts, proof.selected}, json.Deterministic(true))
	if err != nil {
		return nil, err
	}
	proof.digest = payloadDigest(encoded)
	return proof, nil
}

// CheckMaterialization validates complete retained original bytes and the active producer selection.
// It does not mutate state, read sources, renew permission, or replace expired receipts.
func (c *CompiledTopology) CheckMaterialization(ctx context.Context, target TopologyRuntimeTarget, proof *TopologyMaterialization, options ...runtime.Option) error {
	batches, err := c.checkRetainedCapsules(ctx, target, proof)
	if err != nil {
		return err
	}
	selectedRef := c.inventory.Selection.Candidate
	if c.request.Direction == TopologyFleetToLocal || c.request.Direction == TopologyLocalRestore {
		selectedRef = c.inventory.Selection.Accepted
	}
	selected := -1
	for index, capsule := range c.inventory.Capsules {
		ref, _ := capsule.reference()
		if ref == selectedRef {
			selected = index
		}
	}
	if selected < 0 {
		return recovery.ErrConflict
	}
	request := runtime.CatalogRetainedMaterializationRequest{Directory: target.Directory, Owner: target.Owner, SchedulerIdentity: target.SchedulerIdentity, OperationID: "catalog-materialize-" + c.digest, TransferID: c.digest, Batches: batches, Selected: selected}
	if err := runtime.InspectRetainedCatalogMaterialization(ctx, request, proof.selected, options...); err != nil {
		return err
	}
	return c.validateSelectedReplay(ctx, options, selectedRef)
}

// Format omits the selected private runtime directory.
func (t TopologyRuntimeTarget) Format(state fmt.State, _ rune) {
	_, _ = fmt.Fprint(state, "catalog runtime target (private)")
}
