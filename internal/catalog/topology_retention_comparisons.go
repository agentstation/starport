package catalog

import (
	"context"
	"encoding/json/v2"

	"github.com/agentstation/starmap/runtime"
	"github.com/agentstation/starport/internal/recovery"
)

// inspectTopologyRetentionComparisons binds each checked original capsule during compilation.
// The private vector is rebuilt from original source bytes when recovery restarts.
func inspectTopologyRetentionComparisons(ctx context.Context, capsules []TopologyCapsule) ([]*runtime.CatalogRetentionComparison, error) {
	if ctx == nil || len(capsules) == 0 || len(capsules) > fleetRetentionMaxEntries {
		return nil, recovery.ErrConflict
	}
	comparisons := make([]*runtime.CatalogRetentionComparison, len(capsules))
	for index, capsule := range capsules {
		ref, err := capsule.reference()
		if err != nil {
			return nil, err
		}
		entry := runtime.CatalogRetentionEntry{ManifestSHA256: ref.ManifestSHA256, InputsSHA256: ref.InputsSHA256, SourceOrigin: runtime.CatalogRetentionOrigin(capsule.SourceOrigin), SourceDescriptorSHA256: ref.SourceDescriptorSHA256}
		input := runtime.CatalogRetentionInput{CatalogMaterializationInput: runtime.CatalogMaterializationInput{Generation: capsule.Generation, Recovery: capsule.Recovery}, SourceDescriptor: capsule.SourceDescriptor}
		comparison, err := runtime.InspectCatalogRetentionComparison(ctx, entry, input)
		if err != nil {
			return nil, err
		}
		comparisons[index] = comparison
	}
	return comparisons, nil
}

func (c *CompiledTopology) checkRetentionComparisons() error {
	if c == nil || len(c.inventory.Capsules) == 0 || len(c.inventory.Capsules) > fleetRetentionMaxEntries || len(c.retentionComparisons) != len(c.inventory.Capsules) {
		return recovery.ErrConflict
	}
	for _, comparison := range c.retentionComparisons {
		usage := comparison.Usage()
		if usage.Inputs() != 1 || usage.RawBytes() <= 0 || usage.RawBytes() > runtime.MaxCatalogRetentionBatchBytes || usage.DecodedBytes() <= 0 || usage.DecodedBytes() > runtime.MaxCatalogRetentionBatchBytes {
			return recovery.ErrConflict
		}
	}
	return nil
}

// checkRetainedCapsules validates historical bytes without selecting or replaying them.
func (c *CompiledTopology) checkRetainedCapsules(ctx context.Context, target TopologyRuntimeTarget, proof *TopologyMaterialization) ([]runtime.CatalogRetentionBatch, error) {
	if c == nil || ctx == nil || proof == nil || proof.topology != c.digest {
		return nil, recovery.ErrConflict
	}
	if err := c.checkRetentionComparisons(); err != nil {
		return nil, err
	}
	var batches []runtime.CatalogRetentionBatch
	position := 0
	for index, receipt := range proof.receipts {
		if receipt.TransferID != c.digest || receipt.OperationID != topologyBatchOperation(c.digest, index) || receipt.BatchStart != position || len(receipt.Records) == 0 || len(receipt.Records) > len(c.inventory.Capsules)-position {
			return nil, recovery.ErrConflict
		}
		encoded, err := json.Marshal(receipt, json.Deterministic(true))
		if err != nil {
			return nil, err
		}
		batch := runtime.CatalogRetentionBatch{OperationID: receipt.OperationID, ReceiptSHA256: payloadDigest(encoded)}
		for range receipt.Records {
			request := runtime.CatalogRetainedReadRequest{Directory: target.Directory, Owner: target.Owner, SchedulerIdentity: target.SchedulerIdentity, TransferID: c.digest, Batch: batch, Index: position}
			if err := runtime.CheckRetainedCatalogRecovery(ctx, request, c.retentionComparisons[position]); err != nil {
				return nil, err
			}
			position++
		}
		batches = append(batches, batch)
	}
	if position != len(c.inventory.Capsules) {
		return nil, recovery.ErrConflict
	}
	return batches, nil
}
