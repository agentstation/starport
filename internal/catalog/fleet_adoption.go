package catalog

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json/v2"
	"errors"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/agentstation/starmap/runtime"
	"github.com/agentstation/starport/internal/recovery"
	"github.com/agentstation/starport/internal/storage"
)

// FleetAdoptionRequest names a fenced recovery operation and its selected source head.
// ResolveReaders explicitly abandons claims from the externally fenced processes.
// Evidence must identify the operator's fencing and cross-store reconciliation record.
type FleetAdoptionRequest struct {
	SourceApproval recovery.Record   `json:"source_approval"`
	Closed         recovery.Record   `json:"closed"`
	Head           runtime.FleetHead `json:"head"`
	BackendID      string            `json:"backend_id"`
	OperationID    string            `json:"operation_id"`
	Evidence       string            `json:"evidence"`
	ResolveReaders bool              `json:"resolve_readers"`
}

type fleetAdoptionReceipt struct {
	Version            int                  `json:"version"`
	Request            FleetAdoptionRequest `json:"request"`
	InventoryChecksum  string               `json:"inventory_checksum"`
	AcceptanceChecksum string               `json:"acceptance_checksum"`
}

func (r fleetAdoptionReceipt) validate() error {
	if r.Version != 1 || !fleetChunkDigest(r.InventoryChecksum) || !fleetChunkDigest(r.AcceptanceChecksum) {
		return recovery.ErrConflict
	}
	return r.Request.validate()
}

func (r FleetAdoptionRequest) validate() error {
	if err := (recovery.FreshRequest{OperationID: r.OperationID, Evidence: r.Evidence}).Validate(); err != nil {
		return err
	}
	if err := r.Head.Validate(); err != nil {
		return err
	}
	for _, record := range []recovery.Record{r.SourceApproval, r.Closed} {
		if strings.TrimSpace(record.DeploymentID) == "" || len(record.DeploymentID) > 128 ||
			len(record.BackendID) > 256 || (record.BackendID != "" && strings.TrimSpace(record.BackendID) == "") ||
			strings.TrimSpace(record.Evidence) == "" || len(record.Evidence) > 4096 {
			return recovery.ErrConflict
		}
	}
	// SQL restore clears the closed record's former backend identity. The source
	// approval and selected replacement still require their own identities.
	if r.SourceApproval.BackendID == "" {
		return recovery.ErrConflict
	}
	if r.Closed.Open || r.Closed.Epoch <= 0 || r.Head == (runtime.FleetHead{}) || strings.TrimSpace(r.BackendID) == "" || len(r.BackendID) > 256 ||
		r.Head.Identity.DeploymentID != r.Closed.DeploymentID || !r.SourceApproval.Open || r.SourceApproval.DeploymentID != r.Head.Identity.DeploymentID ||
		r.SourceApproval.BackendID != r.Head.Identity.BackendID || r.SourceApproval.Epoch <= 0 || uint64(r.SourceApproval.Epoch) != r.Head.Identity.RecoveryEpoch ||
		uint64(r.Closed.Epoch) <= r.Head.Identity.RecoveryEpoch {
		return recovery.ErrConflict
	}
	return nil
}

func (r FleetAdoptionRequest) approval() recovery.Record {
	next := r.Closed
	next.Open, next.BackendID, next.Evidence = true, r.BackendID, r.Evidence
	return next
}

// AdoptFleet validates copied catalog state before approving the replacement budget authority.
// The operator must first close recovery, fence all writers, and reconcile missing history.
// This operation does not infer those external actions from copied records.
func AdoptFleet(ctx context.Context, backend storage.IncarnationProvider, witness *recovery.Witness, request FleetAdoptionRequest, settings Settings) (recovery.Record, error) {
	if ctx == nil || backend == nil || witness == nil {
		return recovery.Record{}, recovery.ErrClosed
	}
	if err := ctx.Err(); err != nil {
		return recovery.Record{}, err
	}
	if err := request.validate(); err != nil {
		return recovery.Record{}, err
	}
	if settings.DeploymentID != request.Closed.DeploymentID {
		return recovery.Record{}, recovery.ErrConflict
	}
	options, closeSource, err := fleetAdoptionOptions(ctx, settings)
	if err != nil {
		return recovery.Record{}, err
	}
	defer closeSource()
	return adoptFleet(ctx, backend, witness, request, options)
}

func adoptFleet(ctx context.Context, backend storage.IncarnationProvider, witness *recovery.Witness, request FleetAdoptionRequest, options []runtime.Option) (recovery.Record, error) {
	if ctx == nil || backend == nil || witness == nil {
		return recovery.Record{}, recovery.ErrClosed
	}
	if err := request.validate(); err != nil {
		return recovery.Record{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	if _, err := selectFleetAdoption(ctx, backend, witness, request, options); err != nil {
		return recovery.Record{}, err
	}
	return witness.ApproveAuthority(ctx, backend, request.Closed, request.BackendID, request.Evidence, request.OperationID)
}

// selectFleetAdoption changes catalog selection without opening recovery authority.
// Exact retries on an already approved deployment retain the existing selection.
func selectFleetAdoption(ctx context.Context, backend storage.IncarnationProvider, witness *recovery.Witness, request FleetAdoptionRequest, options []runtime.Option) (recovery.Record, error) {
	bound, err := backend.BindIncarnation(ctx, request.BackendID)
	if err != nil {
		return recovery.Record{}, err
	}
	current, err := witness.Current(ctx, request.Closed.DeploymentID)
	if err != nil {
		return recovery.Record{}, err
	}
	if current != request.Closed && current != request.approval() {
		return recovery.Record{}, recovery.ErrConflict
	}
	prefix := "catalog:fleet:{" + payloadDigest([]byte(request.Closed.DeploymentID)) + "}:v1:"
	operationKey := prefix + "adoption-operation:" + payloadDigest([]byte(request.OperationID))
	completed, receiptTTL, readErr := bound.ReadWithLifetime(ctx, operationKey, fleetDescriptorMaxBytes)
	if readErr == nil {
		var receipt fleetAdoptionReceipt
		if receiptTTL != 0 || json.Unmarshal(completed, &receipt) != nil || receipt.validate() != nil || receipt.Request != request {
			return recovery.Record{}, recovery.ErrConflict
		}
		durable, ttl, err := bound.ReadWithLifetime(ctx, prefix+"adoption-receipt:"+payloadDigest(completed), fleetDescriptorMaxBytes)
		if err != nil {
			return recovery.Record{}, err
		}
		if ttl != 0 || !bytes.Equal(durable, completed) {
			return recovery.Record{}, recovery.ErrConflict
		}
		if !current.Open {
			gate := &closedFleetWitness{witness: witness, expected: request.Closed, approval: request.approval()}
			guarded := &closedFleetStore{IncarnationStore: bound, gate: gate}
			identity, err := adoptionFleetIdentity(request.approval())
			if err != nil {
				return recovery.Record{}, err
			}
			fleet := &FleetStore{store: guarded, witness: gate, approval: gate.approval, identity: identity, prefix: prefix, session: rand.Text()}
			if err := verifyAdoptedFleet(ctx, fleet, request, options); err != nil {
				return recovery.Record{}, err
			}
		}
		return request.approval(), nil
	}
	if !errors.Is(readErr, storage.ErrNotFound) {
		return recovery.Record{}, readErr
	}
	if current != request.Closed {
		return recovery.Record{}, recovery.ErrConflict
	}
	return commitFleetAdoption(ctx, witness, request, options, bound, prefix, operationKey)
}

func commitFleetAdoption(ctx context.Context, witness *recovery.Witness, request FleetAdoptionRequest, options []runtime.Option, bound storage.IncarnationStore, prefix, operationKey string) (recovery.Record, error) {
	gate := &closedFleetWitness{witness: witness, expected: request.Closed, approval: request.SourceApproval}
	guarded := &closedFleetStore{IncarnationStore: bound, gate: gate}
	source := &FleetStore{store: guarded, witness: gate, approval: gate.approval, identity: request.Head.Identity, prefix: prefix, session: rand.Text()}
	head, err := source.CurrentHead(ctx)
	if err != nil {
		return recovery.Record{}, err
	}
	if head != request.Head {
		return recovery.Record{}, recovery.ErrConflict
	}
	maintenance, err := source.maintain(ctx)
	if err != nil {
		return recovery.Record{}, err
	}
	defer maintenance.finish()
	workCtx, abort := context.WithCancel(ctx)
	defer abort()
	stopRenewal := renewFleetAdoption(workCtx, maintenance, abort)
	defer func() { _ = stopRenewal() }()
	ctx = workCtx
	selected, headBytes, err := maintenance.readHead(ctx)
	if err != nil {
		return recovery.Record{}, err
	}
	if selected != request.Head {
		return recovery.Record{}, recovery.ErrConflict
	}
	accepted, acceptanceBytes, err := source.readAcceptance(ctx)
	if err != nil {
		return recovery.Record{}, err
	}
	if len(maintenance.inventory.Readers) > 0 && !request.ResolveReaders {
		return recovery.Record{}, errors.New("catalog recovery requires explicit resolution of abandoned reader claims")
	}
	if err := validateAdoptionInputs(ctx, maintenance, selected, accepted, options); err != nil {
		return recovery.Record{}, err
	}
	receipt := fleetAdoptionReceipt{Version: 1, Request: request, InventoryChecksum: payloadDigest(maintenance.encoded), AcceptanceChecksum: payloadDigest(acceptanceBytes)}
	encoded, err := json.Marshal(receipt)
	if err != nil {
		return recovery.Record{}, err
	}
	digest := payloadDigest(encoded)
	target := request.approval()
	identity, err := adoptionFleetIdentity(target)
	if err != nil {
		return recovery.Record{}, err
	}
	mutations := []storage.CompareAndSwapMutation{
		{Key: operationKey, NewValue: encoded},
		{Key: prefix + "adoption-receipt:" + digest, NewValue: encoded},
	}
	inventory, remapped, err := adoptionPublications(source, maintenance.inventory.Entries, identity, digest)
	if err != nil {
		return recovery.Record{}, err
	}
	mutations = append(mutations, remapped...)
	selected.Identity, accepted.Head.Identity = identity, identity
	nextHead, _ := json.Marshal(selected)
	nextAccepted, _ := json.Marshal(accepted)
	nextInventory, err := json.Marshal(inventory)
	if err != nil {
		return recovery.Record{}, err
	}
	if len(nextInventory) > fleetRetentionRecordBytes {
		return recovery.Record{}, errors.New("fleet retention inventory exceeds its byte limit")
	}
	mutations = append(mutations,
		storage.CompareAndSwapMutation{Key: prefix + "head", ExpectedValue: headBytes, NewValue: nextHead},
		storage.CompareAndSwapMutation{Key: prefix + "accepted", ExpectedValue: acceptanceBytes, NewValue: nextAccepted},
		storage.CompareAndSwapMutation{Key: prefix + "inventory", ExpectedValue: maintenance.encoded, NewValue: nextInventory},
		storage.CompareAndSwapMutation{Key: prefix + "maintenance", ExpectedValue: maintenance.token},
	)
	for _, marker := range []struct{ key, value string }{{"head-initialized", "fleet-head/1"}, {"inventory-initialized", "fleet-retention/1"}} {
		mutations = append(mutations, storage.CompareAndSwapMutation{Key: prefix + marker.key, ExpectedValue: []byte(marker.value), NewValue: []byte(marker.value)})
	}
	lease, _, err := bound.ReadWithLifetime(ctx, prefix+"lease", 4096)
	if err != nil && !errors.Is(err, storage.ErrNotFound) {
		return recovery.Record{}, err
	}
	mutations = append(mutations, storage.CompareAndSwapMutation{Key: prefix + "lease", ExpectedValue: lease})
	epochBytes, ttl, err := bound.ReadWithLifetime(ctx, prefix+"epoch", 32)
	if err != nil {
		return recovery.Record{}, err
	}
	epoch, err := strconv.ParseUint(string(epochBytes), 10, 64)
	if err != nil || ttl != 0 || epoch == 0 || epoch == math.MaxUint64 {
		return recovery.Record{}, errors.New("catalog recovery lease epoch is missing or exhausted")
	}
	mutations = append(mutations, storage.CompareAndSwapMutation{Key: prefix + "epoch", ExpectedValue: epochBytes, NewValue: epochBytes})
	if err := stopRenewal(); err != nil {
		return recovery.Record{}, err
	}
	if err := guarded.CompareAndSwap(ctx, mutations, prefix+"maintenance"); err != nil {
		return recovery.Record{}, err
	}
	return request.approval(), nil
}

func validateAdoptionInputs(ctx context.Context, maintenance *fleetMaintenance, selected runtime.FleetHead, accepted fleetAcceptance, options []runtime.Option) error {
	found := map[runtime.FleetHead]bool{}
	history := map[string]GenerationIndexEntry{}
	for _, entry := range accepted.History {
		history[entry.GenerationID] = entry
	}
	for _, blob := range maintenance.inventory.Entries {
		if err := maintenance.mutate(ctx, nil); err != nil {
			return err
		}
		snapshot, err := maintenance.read(ctx, blob)
		if err != nil {
			return err
		}
		if blob.Head == selected || blob.Head == accepted.Head {
			if err := runtime.ValidateFleetReplay(ctx, snapshot, options...); err != nil {
				return err
			}
		} else if err := runtime.ValidateFleetRecovery(ctx, snapshot); err != nil {
			return err
		}
		found[blob.Head] = true
		if expected, ok := history[blob.Head.GenerationID]; ok {
			actual, err := fleetIndexEntry(snapshot.Publication.Generation)
			if err != nil {
				return err
			}
			if actual != expected {
				return errors.New("catalog recovery history differs from retained generation")
			}
			delete(history, blob.Head.GenerationID)
		}
	}
	if !found[selected] || !found[accepted.Head] || len(history) != 0 {
		return errors.New("catalog recovery lacks a selected or retained history publication")
	}
	return nil
}

func verifyAdoptedFleet(ctx context.Context, fleet *FleetStore, request FleetAdoptionRequest, options []runtime.Option) error {
	maintenance, err := fleet.maintain(ctx)
	if err != nil {
		return err
	}
	defer maintenance.finish()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := renewFleetAdoption(ctx, maintenance, cancel)
	defer func() { _ = stop() }()
	selected, _, err := maintenance.readHead(ctx)
	if err != nil {
		return err
	}
	expected := request.Head
	expected.Identity = fleet.identity
	if selected != expected || len(maintenance.inventory.Readers) != 0 {
		return recovery.ErrConflict
	}
	accepted, _, err := fleet.readAcceptance(ctx)
	if err != nil {
		return err
	}
	if err := validateAdoptionInputs(ctx, maintenance, selected, accepted, options); err != nil {
		return err
	}
	return stop()
}

func (s *FleetStore) verifyAdoption(ctx context.Context, blob fleetBlob) error {
	if blob.Adoption == nil {
		return nil
	}
	data, ttl, err := s.store.ReadWithLifetime(ctx, s.prefix+"adoption-receipt:"+blob.Adoption.Receipt, fleetDescriptorMaxBytes)
	if err != nil {
		return err
	}
	var record fleetAdoptionReceipt
	if ttl != 0 || payloadDigest(data) != blob.Adoption.Receipt || json.Unmarshal(data, &record) != nil || record.validate() != nil || record.Request.approval() != s.approval || record.Request.Head.Identity != blob.Adoption.Previous.Identity {
		return errors.New("catalog adoption receipt does not match independent approval")
	}
	return nil
}

type closedFleetWitness struct {
	witness            *recovery.Witness
	expected, approval recovery.Record
}

func (w *closedFleetWitness) check(ctx context.Context) error {
	current, err := w.witness.Current(ctx, w.expected.DeploymentID)
	if err != nil {
		return err
	}
	if current != w.expected {
		return recovery.ErrConflict
	}
	return nil
}
func (w *closedFleetWitness) Approved(ctx context.Context, deployment string) (recovery.Record, error) {
	if deployment != w.expected.DeploymentID {
		return recovery.Record{}, recovery.ErrConflict
	}
	return w.approval, w.check(ctx)
}
func (*closedFleetWitness) CheckBootstrap(context.Context, recovery.Record) error {
	return recovery.ErrBootstrapConsumed
}
func (*closedFleetWitness) ConsumeBootstrap(context.Context, recovery.Record) error {
	return recovery.ErrBootstrapConsumed
}

type closedFleetStore struct {
	storage.IncarnationStore
	gate *closedFleetWitness
}

func (s *closedFleetStore) CompareAndSwap(ctx context.Context, mutations []storage.CompareAndSwapMutation, live ...string) error {
	if err := s.gate.check(ctx); err != nil {
		return err
	}
	if err := s.IncarnationStore.CompareAndSwap(ctx, mutations, live...); err != nil {
		return err
	}
	return s.gate.check(ctx)
}

func adoptionFleetIdentity(record recovery.Record) (runtime.FleetIdentity, error) {
	if record.Epoch <= 0 {
		return runtime.FleetIdentity{}, recovery.ErrConflict
	}
	return runtime.FleetIdentity{DeploymentID: record.DeploymentID, RecoveryEpoch: uint64(record.Epoch), BackendID: record.BackendID}, nil
}

func adoptionPublications(source *FleetStore, entries []fleetBlob, identity runtime.FleetIdentity, digest string) (fleetInventory, []storage.CompareAndSwapMutation, error) {
	inventory := fleetInventory{Version: 1, Readers: map[string]string{}}
	mutations := make([]storage.CompareAndSwapMutation, 0, len(entries)*2)
	for _, blob := range entries {
		original, err := json.Marshal(blob)
		if err != nil {
			return fleetInventory{}, nil, err
		}
		replacement := blob
		replacement.Head.Identity = identity
		replacement.Adoption = &runtime.FleetAdoption{Previous: blob.Head, Receipt: digest}
		updated, err := json.Marshal(replacement)
		if err != nil {
			return fleetInventory{}, nil, err
		}
		mutations = append(mutations, storage.CompareAndSwapMutation{Key: source.publicationKey(blob.Head), ExpectedValue: original}, storage.CompareAndSwapMutation{Key: source.publicationKey(replacement.Head), NewValue: updated})
		inventory.Entries = append(inventory.Entries, replacement)
	}
	if err := inventory.validate(identity); err != nil {
		return fleetInventory{}, nil, err
	}
	return inventory, mutations, nil
}
