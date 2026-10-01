package catalog

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/agentstation/starmap/runtime"
	"github.com/agentstation/starport/internal/recovery"
	"github.com/agentstation/starport/internal/storage"
)

// TopologyDirection names the supported catalog ownership changes.
type TopologyDirection string

// Supported topology changes preserve separate source and destination ownership.
const (
	TopologyLocalToFleet      TopologyDirection = "local-to-fleet"
	TopologyFleetToLocal      TopologyDirection = "fleet-to-local"
	TopologyLocalRestore      TopologyDirection = "local-restore"
	TopologyFleetRestore      TopologyDirection = "fleet-restore"
	topologyBatchMaxMutations                   = 128
	topologyBatchMaxBytes                       = 4 << 20
)

// TopologyTransferRequest supplies domain decisions, not catalog storage mutations.
// DestinationBoundary remains closed. Its identity differs from archived source identity.
// A prepared SQL boundary can omit BackendID until final activation.
// The host must verify DestinationIdentity against the destination native incarnation before release.
type TopologyTransferRequest struct {
	Direction              TopologyDirection         `json:"direction"`
	Operation              recovery.RestoreOperation `json:"operation"`
	DestinationBoundary    recovery.Record           `json:"destination_boundary"`
	DestinationIdentity    runtime.FleetIdentity     `json:"destination_identity"`
	AcceptedDecisionSHA256 string                    `json:"accepted_decision_sha256"`
	ClosedImportSHA256     string                    `json:"closed_import_sha256"`
}

// TopologyTarget applies catalog-owned changes under the exact closed native import.
// Reads and mutations must bind the same native cursor. External writer fencing remains required.
// A committed native receipt must survive a later cross-store guard failure for exact retry.
type TopologyTarget interface {
	ReadCatalogTopology(context.Context, string, int) ([]byte, error)
	CompletedCatalogTopology(context.Context, string, int) (bool, error)
	ApplyCatalogTopology(context.Context, string, int, []storage.CompareAndSwapMutation) error
}

type topologyBatch struct {
	mutations []storage.CompareAndSwapMutation
	expiring  []storage.TransferRecord
}

// CompiledTopology privately retains deterministic catalog changes. It grants no admission authority.
type CompiledTopology struct {
	retentionComparisons []*runtime.CatalogRetentionComparison
	preparedCensus       map[string]preparedCatalogRecord
	retiredExpiring      map[string]bool
	recoveryRecord       []byte
	inventory            topologyInventoryData
	request              TopologyTransferRequest
	digest               string
	stages               []topologyBatch
	selection            []storage.CompareAndSwapMutation
	expected             map[string][]byte
	markerKey            string
	marker               []byte
}

// Format omits source records and target mutation preimages.
func (c CompiledTopology) Format(state fmt.State, _ rune) {
	_, _ = fmt.Fprint(state, "compiled catalog topology (private)")
}

// StageCount reports the exact number of bounded native stage transactions.
func (c *CompiledTopology) StageCount() int {
	if c == nil {
		return 0
	}
	return len(c.stages)
}

// CompileTopologyTransfer binds checked source facts and exact imported target preimages.
// It calls no runtime acquisition, serving approval, refresh lease, or ordinary commit method.
func CompileTopologyTransfer(ctx context.Context, inventory *TopologyInventory, request TopologyTransferRequest, target *recovery.KVSnapshotView) (*CompiledTopology, error) {
	if target == nil {
		return nil, errors.New("catalog transfer requires a captured target")
	}
	return compileTopologyTransfer(ctx, inventory, request, target)
}

func compileTopologyTransfer(ctx context.Context, inventory *TopologyInventory, request TopologyTransferRequest, target capturedCatalogRecords) (*CompiledTopology, error) {
	if ctx == nil || inventory == nil || target == nil {
		return nil, errors.New("catalog transfer requires checked source and target facts")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := request.Operation.Validate(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(request.Operation.FencingEvidence) == "" || !validCapturedBoundary(request.DestinationBoundary) || !fleetChunkDigest(request.AcceptedDecisionSHA256) || !fleetChunkDigest(request.ClosedImportSHA256) {
		return nil, recovery.ErrConflict
	}
	if request.DestinationBoundary.Epoch <= 0 {
		return nil, recovery.ErrConflict
	}
	if err := validateTopologyDirection(inventory, request); err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(struct {
		Inventory string
		Request   TopologyTransferRequest
	}{inventory.digest, request}, json.Deterministic(true))
	if err != nil {
		return nil, err
	}
	c := &CompiledTopology{inventory: inventory.data, request: request, digest: payloadDigest(encoded), expected: map[string][]byte{}}
	comparisons, err := inspectTopologyRetentionComparisons(ctx, c.inventory.Capsules)
	if err != nil {
		return nil, err
	}
	c.retentionComparisons = comparisons
	c.markerKey = "catalog:topology:v1:" + payloadDigest([]byte(request.Operation.ID)) + ":stage"
	immutable := map[string][]byte{}
	controls := map[string][]byte{}
	if request.Direction == TopologyLocalToFleet || request.Direction == TopologyFleetRestore {
		err = c.compileFleet(immutable, controls)
	} else {
		err = c.compileLocal(immutable, controls)
	}
	if err != nil {
		return nil, err
	}
	if err = c.compileArchive(immutable); err != nil {
		return nil, err
	}
	if err = c.prepareStages(ctx, target, immutable); err != nil {
		return nil, err
	}
	// Stage the complete historical archive before retiring the old selection namespace.
	// Admission stays closed, so a crash cannot discard source evidence.
	if err = c.prepareRetirement(ctx, target, controls); err != nil {
		return nil, err
	}
	if err = c.prepareSelection(ctx, target, controls); err != nil {
		return nil, err
	}
	return c, nil
}

func (c *CompiledTopology) compileFleet(immutable, controls map[string][]byte) error {
	prefix := "catalog:fleet:{" + payloadDigest([]byte(c.request.DestinationIdentity.DeploymentID)) + "}:v1:"
	inventory := fleetInventory{Version: 1, Readers: map[string]string{}}
	var candidate, accepted runtime.FleetHead
	order := make([]int, 0, len(c.inventory.Capsules))
	for index, capsule := range c.inventory.Capsules {
		ref, _ := capsule.reference()
		if ref != c.inventory.Selection.Accepted && ref != c.inventory.Selection.Candidate {
			order = append(order, index)
		}
	}
	for _, selected := range []TopologyReference{c.inventory.Selection.Accepted, c.inventory.Selection.Candidate} {
		for index, capsule := range c.inventory.Capsules {
			ref, _ := capsule.reference()
			if ref == selected && !slices.Contains(order, index) {
				order = append(order, index)
			}
		}
	}
	var previous runtime.FleetHead
	for index, position := range order {
		capsule := c.inventory.Capsules[position]
		publication := runtime.FleetPublication{Expected: previous, Generation: capsule.Generation, Recovery: capsule.Recovery.Inputs, RecoveryOrigin: &runtime.FleetRecoveryOrigin{
			Version: runtime.FleetRecoveryOriginVersion, Identity: c.request.DestinationIdentity, OperationID: c.request.Operation.ID, AcceptedDecisionSHA256: c.request.AcceptedDecisionSHA256, ClosedImportSHA256: c.request.ClosedImportSHA256, GenerationManifestSHA256: capsule.Recovery.ManifestChecksum, RecoverySHA256: capsule.Recovery.Inputs.Checksum,
		}}
		head := runtime.FleetHead{Identity: c.request.DestinationIdentity, Revision: uint64(index + 1), GenerationID: capsule.Generation.Manifest.GenerationID, RecoveryChecksum: capsule.Recovery.Inputs.Checksum}
		snapshot := runtime.FleetSnapshot{Head: head, Publication: publication}
		if err := snapshot.Validate(); err != nil {
			return err
		}
		payload, err := json.Marshal(snapshot, json.Deterministic(true))
		if err != nil {
			return err
		}
		record, chunks := encodeGenerationPayload(payload)
		logical, err := fleetGenerationBytes(capsule.Generation)
		if err != nil {
			return err
		}
		// A deterministic hexadecimal token satisfies the existing blob ownership format.
		blob := fleetBlob{ID: payloadDigest(payload), Head: head, Record: record, GenerationBytes: logical, RecoveryBytes: int64(len(capsule.Recovery.Inputs.Data))}
		for key, value := range chunks {
			immutable[prefix+"blob:"+blob.ID+":"+strings.TrimPrefix(key, catalogGenerationChunkKeyPrefix)] = value
		}
		descriptor, err := json.Marshal(blob)
		if err != nil {
			return err
		}
		immutable[fleetPublicationKey(prefix, head)] = descriptor
		inventory.Entries = append(inventory.Entries, blob)
		ref, _ := capsule.reference()
		if ref == c.inventory.Selection.Accepted {
			accepted = head
		}
		if ref == c.inventory.Selection.Candidate {
			candidate = head
		}
		previous = head
	}
	if err := inventory.validate(c.request.DestinationIdentity); err != nil {
		return err
	}
	if candidate != previous || accepted == (runtime.FleetHead{}) {
		return recovery.ErrConflict
	}
	for key, value := range map[string]any{fleetInventoryKind: inventory, fleetHeadKind: candidate, string(OperationAccepted): fleetAcceptance{Head: accepted, History: c.inventory.History}} {
		encoded, err := json.Marshal(value)
		if err != nil {
			return err
		}
		if len(encoded) > fleetDescriptorMaxBytes {
			return storage.ErrValueTooLarge
		}
		controls[prefix+key] = encoded
	}
	controls[prefix+"epoch"] = []byte("1")
	controls[prefix+"inventory-initialized"] = []byte("fleet-retention/1")
	controls[prefix+"head-initialized"] = []byte("fleet-head/1")
	// Recovery reserves the fencing floor without inventing a live refresh lease.
	controls[prefix+"lease"] = nil
	controls[catalogCurrentGenerationKey] = nil
	controls[candidateCurrentGenerationKey] = nil
	controls[catalogGenerationIndexKey] = nil
	return nil
}

func (c *CompiledTopology) compileLocal(immutable, controls map[string][]byte) error {
	for _, capsule := range c.inventory.Capsules {
		payload, err := json.Marshal(capsule.Generation)
		if err != nil {
			return err
		}
		record, chunks := encodeGenerationPayload(payload)
		for key, value := range chunks {
			immutable[key] = value
		}
		descriptor, err := json.Marshal(record)
		if err != nil {
			return err
		}
		key := catalogGenerationKey(capsule.Generation.Manifest.GenerationID)
		if prior, found := immutable[key]; found && !bytes.Equal(prior, descriptor) {
			return recovery.ErrConflict
		}
		immutable[key] = descriptor
	}
	controls[catalogCurrentGenerationKey] = []byte(topologyGenerationID(c.inventory, c.inventory.Selection.Accepted))
	controls[candidateCurrentGenerationKey] = []byte(topologyGenerationID(c.inventory, c.inventory.Selection.Candidate))
	history, err := json.Marshal(c.inventory.History)
	if err != nil {
		return err
	}
	controls[catalogGenerationIndexKey] = history
	return nil
}

func (c *CompiledTopology) prepareStages(ctx context.Context, target capturedCatalogRecords, immutable map[string][]byte) error {
	for _, key := range slices.Sorted(maps.Keys(immutable)) {
		value := immutable[key]
		prior, err := topologyPreimage(ctx, target, key)
		if err != nil {
			return err
		}
		if prior != nil && !bytes.Equal(prior, value) {
			return errors.New("catalog immutable target identity already contains different content")
		}
		if err = c.appendStage(storage.CompareAndSwapMutation{Key: key, ExpectedValue: prior, NewValue: bytes.Clone(value)}); err != nil {
			return err
		}
		c.expected[key] = bytes.Clone(value)
	}
	return nil
}

func (c *CompiledTopology) prepareRetirement(ctx context.Context, target capturedCatalogRecords, controls map[string][]byte) error {
	if c.request.Direction != TopologyFleetToLocal && c.request.Direction != TopologyFleetRestore {
		return nil
	}
	var retire []storage.CompareAndSwapMutation
	var expiring []storage.TransferRecord
	if err := target.Enumerate(ctx, func(record storage.TransferRecord) error {
		if !strings.HasPrefix(record.Key, "catalog:fleet:") {
			return nil
		}
		if !strings.HasPrefix(record.Key, c.inventory.Archive.prefix()) {
			return recovery.ErrConflict
		}
		if record.ExpiresAtMillis != 0 {
			if !strings.HasSuffix(record.Key, ":lease") && !strings.HasSuffix(record.Key, ":maintenance") {
				return recovery.ErrConflict
			}
			record.Value = bytes.Clone(record.Value)
			expiring = append(expiring, record)
			return nil
		}
		// Selected control roots change together in the final selection transaction.
		// Preserve newly staged immutable records when source and destination share a namespace.
		if _, selected := controls[record.Key]; selected {
			return nil
		}
		if _, staged := c.expected[record.Key]; staged {
			return nil
		}
		retire = append(retire, storage.CompareAndSwapMutation{Key: record.Key, ExpectedValue: bytes.Clone(record.Value)})
		return nil
	}); err != nil {
		return err
	}
	slices.SortFunc(retire, func(a, b storage.CompareAndSwapMutation) int { return strings.Compare(a.Key, b.Key) })
	for _, mutation := range retire {
		if err := c.appendStage(mutation); err != nil {
			return err
		}
		c.expected[mutation.Key] = nil
	}
	slices.SortFunc(expiring, func(a, b storage.TransferRecord) int { return strings.Compare(a.Key, b.Key) })
	for _, record := range expiring {
		if err := c.appendExpiringRetirement(record); err != nil {
			return err
		}
	}
	if len(expiring) != 0 {
		// The native retirement cursor precedes a separate persistent marker transaction.
		c.stages = append(c.stages, topologyBatch{})
	}
	return nil
}

func (c *CompiledTopology) appendStage(mutation storage.CompareAndSwapMutation) error {
	if mutationBytes(mutation) > topologyBatchMaxBytes-4096 {
		return storage.ErrValueTooLarge
	}
	if len(c.stages) == 0 || len(c.stages[len(c.stages)-1].expiring) != 0 || len(c.stages[len(c.stages)-1].mutations) >= topologyBatchMaxMutations-1 || batchBytes(c.stages[len(c.stages)-1].mutations)+mutationBytes(mutation) > topologyBatchMaxBytes-4096 {
		c.stages = append(c.stages, topologyBatch{})
	}
	last := &c.stages[len(c.stages)-1]
	last.mutations = append(last.mutations, mutation)
	return nil
}

func (c *CompiledTopology) prepareSelection(ctx context.Context, target capturedCatalogRecords, controls map[string][]byte) error {
	prior, err := topologyPreimage(ctx, target, c.markerKey)
	if err != nil {
		return err
	}
	if prior != nil {
		return errors.New("catalog topology operation already has staged evidence; resume its retained compiler")
	}
	for _, key := range slices.Sorted(maps.Keys(controls)) {
		preimage, err := topologyPreimage(ctx, target, key)
		if err != nil {
			return err
		}
		if c.retiredExpiring[key] {
			preimage = nil
		}
		c.selection = append(c.selection, storage.CompareAndSwapMutation{Key: key, ExpectedValue: preimage, NewValue: bytes.Clone(controls[key])})
	}
	stageDigests := make([]string, len(c.stages))
	for index, batch := range c.stages {
		stageDigests[index] = topologyStageDigest(batch)
	}
	plan, err := json.Marshal(struct {
		SourceAndOperation string
		Stages             []string
		Selection          string
	}{c.digest, stageDigests, topologyMutationDigest(c.selection)}, json.Deterministic(true))
	if err != nil {
		return err
	}
	c.digest = payloadDigest(plan)
	previous := []byte(nil)
	for index := range c.stages {
		if len(c.stages[index].expiring) != 0 {
			continue
		}
		encoded, err := json.Marshal(struct {
			Topology string
			Batch    int
			Prior    string
			Content  string
		}{c.digest, index, payloadDigest(previous), stageDigests[index]}, json.Deterministic(true))
		if err != nil {
			return err
		}
		c.stages[index].mutations = append(c.stages[index].mutations, storage.CompareAndSwapMutation{Key: c.markerKey, ExpectedValue: bytes.Clone(previous), NewValue: encoded})
		previous = encoded
	}
	c.marker = previous
	c.selection = append(c.selection, storage.CompareAndSwapMutation{Key: c.markerKey, ExpectedValue: bytes.Clone(previous), NewValue: bytes.Clone(previous)})
	if len(c.selection)+1 > topologyBatchMaxMutations || batchBytes(c.selection) > topologyBatchMaxBytes-4096 {
		return storage.ErrValueTooLarge
	}
	return nil
}

func topologyPreimage(ctx context.Context, target capturedCatalogRecords, key string) ([]byte, error) {
	record, err := target.ReadCaptured(ctx, key, topologyBatchMaxBytes)
	if errors.Is(err, storage.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if record.ExpiresAtMillis != 0 && !strings.HasSuffix(key, ":lease") && !strings.HasSuffix(key, ":maintenance") {
		return nil, recovery.ErrConflict
	}
	return bytes.Clone(record.Value), nil
}

func mutationBytes(m storage.CompareAndSwapMutation) int {
	return len(m.Key) + len(m.ExpectedValue) + len(m.NewValue)
}
func batchBytes(mutations []storage.CompareAndSwapMutation) int {
	total := 0
	for _, m := range mutations {
		total += mutationBytes(m)
	}
	return total
}
func topologyMutationDigest(mutations []storage.CompareAndSwapMutation) string {
	encoded, _ := json.Marshal(mutations, json.Deterministic(true))
	return payloadDigest(encoded)
}

// ApplyStage submits one predetermined transaction through the recovery-owned native guard.
func (c *CompiledTopology) ApplyStage(ctx context.Context, index int, target TopologyTarget) error {
	if c == nil || target == nil || index < 0 || index >= len(c.stages) {
		return recovery.ErrConflict
	}
	if len(c.stages[index].expiring) != 0 {
		owner, ok := target.(TopologyExpiringTarget)
		if !ok {
			return errors.New("catalog retirement requires its native expiring-record owner")
		}
		return owner.ApplyExpiringCatalogTopology(ctx, c.digest, index, cloneTopologyExpiring(c.stages[index].expiring))
	}
	return target.ApplyCatalogTopology(ctx, c.digest, index, cloneTopologyMutations(c.stages[index].mutations))
}

func cloneTopologyMutations(source []storage.CompareAndSwapMutation) []storage.CompareAndSwapMutation {
	result := slices.Clone(source)
	for index := range result {
		result[index].ExpectedValue = bytes.Clone(result[index].ExpectedValue)
		result[index].NewValue = bytes.Clone(result[index].NewValue)
	}
	return result
}

// ApplySelection verifies all staged immutable bytes before the final bounded selection transaction.
// The native guard must refuse a changed replay cursor. The final graph census remains mandatory.
func (c *CompiledTopology) ApplySelection(ctx context.Context, proof *TopologyMaterialization, target TopologyTarget) error {
	if c == nil || proof == nil || target == nil || proof.topology != c.digest {
		return recovery.ErrConflict
	}
	completed, err := target.CompletedCatalogTopology(ctx, c.digest, len(c.stages))
	if err != nil {
		return err
	}
	if completed {
		return nil
	}
	if err := c.checkStaged(ctx, target); err != nil {
		return err
	}
	encoded, err := json.Marshal(struct {
		Version         int
		Topology        string
		Selection       TopologySelection
		Materialization string
	}{1, c.digest, c.inventory.Selection, proof.digest}, json.Deterministic(true))
	if err != nil {
		return err
	}
	mutations := cloneTopologyMutations(c.selection)
	mutations = append(mutations, storage.CompareAndSwapMutation{Key: "catalog:topology:v1:" + payloadDigest([]byte(c.request.Operation.ID)) + ":selection", NewValue: encoded})
	return target.ApplyCatalogTopology(ctx, c.digest, len(c.stages), mutations)
}

// TopologyDigest binds the exact checked transfer without exposing private source records.
func (c *CompiledTopology) TopologyDigest() string {
	if c == nil {
		return ""
	}
	return c.digest
}

func topologyBatchOperation(digest string, index int) string {
	return "catalog-retain-" + digest + "-" + strconv.Itoa(index)
}

func (c *CompiledTopology) checkStaged(ctx context.Context, target TopologyTarget) error {
	for _, key := range slices.Sorted(maps.Keys(c.expected)) {
		actual, err := target.ReadCatalogTopology(ctx, key, topologyBatchMaxBytes)
		if errors.Is(err, storage.ErrNotFound) && c.expected[key] == nil {
			continue
		}
		if err != nil {
			return err
		}
		if !bytes.Equal(actual, c.expected[key]) || (actual == nil) != (c.expected[key] == nil) {
			return errors.New("staged catalog content changed before selection")
		}
	}
	return nil
}

// CheckSelection validates current catalog selection without treating a historical receipt as current permission.
// The target owner must preserve its selected native import identity and writer fence during reads.
func (c *CompiledTopology) CheckSelection(ctx context.Context, proof *TopologyMaterialization, target TopologyTarget) error {
	if c == nil || proof == nil || target == nil || proof.topology != c.digest {
		return recovery.ErrConflict
	}
	if err := c.checkStaged(ctx, target); err != nil {
		return err
	}
	for _, mutation := range c.selection {
		actual, err := target.ReadCatalogTopology(ctx, mutation.Key, topologyBatchMaxBytes)
		if errors.Is(err, storage.ErrNotFound) && mutation.NewValue == nil {
			continue
		}
		if err != nil {
			return err
		}
		if !bytes.Equal(actual, mutation.NewValue) || (actual == nil) != (mutation.NewValue == nil) {
			return recovery.ErrConflict
		}
	}
	expected, err := json.Marshal(struct {
		Version         int
		Topology        string
		Selection       TopologySelection
		Materialization string
	}{1, c.digest, c.inventory.Selection, proof.digest}, json.Deterministic(true))
	if err != nil {
		return err
	}
	actual, err := target.ReadCatalogTopology(ctx, "catalog:topology:v1:"+payloadDigest([]byte(c.request.Operation.ID))+":selection", fleetDescriptorMaxBytes)
	if err != nil {
		return err
	}
	if !bytes.Equal(actual, expected) {
		return recovery.ErrConflict
	}
	return nil
}

// Format omits private accepted decisions and fencing evidence.
func (r TopologyTransferRequest) Format(state fmt.State, _ rune) {
	_, _ = fmt.Fprint(state, "catalog transfer request (private)")
}

func (c *CompiledTopology) compileArchive(immutable map[string][]byte) error {
	if c.inventory.Archive == nil {
		return nil
	}
	for _, original := range c.inventory.Archive.Records {
		_, chunks := encodeGenerationPayload(original.Value)
		for key, value := range chunks {
			immutable[topologyArchivePrefix+"chunk:"+strings.TrimPrefix(key, catalogGenerationChunkKeyPrefix)] = value
		}
	}
	archived, err := json.Marshal(c.inventory.Archive, json.Deterministic(true))
	if err != nil {
		return err
	}
	if len(archived) > topologyArchiveMetadataMaxBytes {
		return storage.ErrValueTooLarge
	}
	record, chunks := encodeGenerationPayload(archived)
	for key, value := range chunks {
		immutable[topologyArchivePrefix+"chunk:"+strings.TrimPrefix(key, catalogGenerationChunkKeyPrefix)] = value
	}
	descriptor, err := json.Marshal(record, json.Deterministic(true))
	if err != nil {
		return err
	}
	immutable[topologyArchivePrefix+"record:"+record.Digest] = descriptor
	return nil
}

func validateTopologyDirection(inventory *TopologyInventory, request TopologyTransferRequest) error {
	if request.DestinationBoundary.Epoch <= 0 {
		return recovery.ErrConflict
	}
	switch request.Direction {
	case TopologyLocalToFleet, TopologyFleetRestore:
		if (request.Direction == TopologyLocalToFleet) != (inventory.data.Archive == nil) || request.DestinationIdentity.Validate() != nil || request.DestinationIdentity.DeploymentID != request.DestinationBoundary.DeploymentID || request.DestinationIdentity.RecoveryEpoch != uint64(request.DestinationBoundary.Epoch) || (request.DestinationBoundary.BackendID != "" && request.DestinationIdentity.BackendID != request.DestinationBoundary.BackendID) {
			return recovery.ErrConflict
		}
	case TopologyFleetToLocal, TopologyLocalRestore:
		if (request.Direction == TopologyFleetToLocal) != (inventory.data.Archive != nil) || request.DestinationIdentity != (runtime.FleetIdentity{}) {
			return recovery.ErrConflict
		}
	default:
		return recovery.ErrConflict
	}
	if request.Direction == TopologyFleetRestore && request.DestinationIdentity == inventory.data.Archive.Identity {
		return recovery.ErrConflict
	}
	return nil
}
