package catalog

import (
	"context"
	"encoding/json/v2"
	"errors"
	"reflect"
	"strconv"
	"strings"

	"github.com/agentstation/starmap/runtime"
	"github.com/agentstation/starport/internal/recovery"
	"github.com/agentstation/starport/internal/storage"
)

type capturedFleet struct {
	capturedCatalog
	boundary                                 recovery.Record
	prefix                                   string
	inventory                                fleetInventory
	head                                     runtime.FleetHead
	accepted                                 fleetAcceptance
	identity                                 runtime.FleetIdentity
	inventoryFound, headFound, acceptedFound bool
	publications                             map[string]fleetBlob
	chunks                                   map[string]capturedFleetChunk
	heads                                    map[runtime.FleetHead]bool
	generations                              map[string]GenerationIndexEntry
	maxGrant                                 uint64
	epoch                                    uint64
	epochFound                               bool
}

func (c capturedCatalog) inspectFleet(ctx context.Context, boundary recovery.Record) error {
	f := capturedFleet{capturedCatalog: c, boundary: boundary, prefix: "catalog:fleet:{" + payloadDigest([]byte(boundary.DeploymentID)) + "}:v1:", publications: map[string]fleetBlob{}, chunks: map[string]capturedFleetChunk{}, heads: map[runtime.FleetHead]bool{}, generations: map[string]GenerationIndexEntry{}}
	if err := f.loadSelection(ctx); err != nil {
		return err
	}

	for _, blob := range f.inventory.Entries {
		f.heads[blob.Head] = true
		f.publications[fleetPublicationKey(f.prefix, blob.Head)] = blob
		addCapturedFleetChunks(f.prefix, blob, f.chunks)
	}
	if err := f.inspectPending(); err != nil {
		return err
	}
	var err error
	f.epoch, f.epochFound, err = c.capturedFleetEpoch(ctx, f.prefix)
	if err != nil {
		return err
	}
	if (f.headFound || len(f.inventory.Entries) > 0 || f.inventory.Pending != nil) && !f.epochFound {
		return errors.New("captured fleet lost its publication epoch")
	}
	if err := c.records.Enumerate(ctx, func(record storage.TransferRecord) error { return f.inspectRecord(ctx, record) }); err != nil {
		return err
	}
	if err := f.inspectPublications(ctx); err != nil {
		return err
	}
	if f.epoch < f.maxGrant {
		return errors.New("captured fleet lost its publication epoch")
	}
	return nil
}

func (f *capturedFleet) loadSelection(ctx context.Context) error {
	var err error
	f.inventoryFound, err = f.decodeOptional(ctx, f.prefix+"inventory", fleetRetentionRecordBytes, &f.inventory)
	if err != nil {
		return err
	}
	f.headFound, err = f.decodeOptional(ctx, f.prefix+"head", 4096, &f.head)
	if err != nil {
		return err
	}
	f.acceptedFound, err = f.decodeOptional(ctx, f.prefix+"accepted", fleetDescriptorMaxBytes, &f.accepted)
	if err != nil {
		return err
	}
	f.identity = f.head.Identity
	if !f.headFound {
		if len(f.inventory.Entries) > 0 {
			f.identity = f.inventory.Entries[0].Head.Identity
		} else if f.inventory.Pending != nil {
			f.identity = f.inventory.Pending.Head.Identity
		}
	}
	if f.identity != (runtime.FleetIdentity{}) {
		if err := validateCapturedFleetIdentity(f.identity, f.boundary); err != nil {
			return err
		}
	}
	if f.inventoryFound {
		if err := f.inventory.validate(f.identity); err != nil {
			return err
		}
		if err := f.requireMarker(ctx, f.prefix+"inventory-initialized", "fleet-retention/1"); err != nil {
			return err
		}
	}
	return f.validateSelection(ctx)
}

func (f *capturedFleet) validateSelection(ctx context.Context) error {
	if !f.headFound && len(f.inventory.Entries) > 0 {
		return errors.New("captured fleet retained publications have no head")
	}
	if f.headFound {
		if !f.inventoryFound || f.head == (runtime.FleetHead{}) {
			return errors.New("captured fleet head has no valid inventory")
		}
		if err := f.head.Validate(); err != nil {
			return err
		}
		if err := f.requireMarker(ctx, f.prefix+"head-initialized", "fleet-head/1"); err != nil {
			return err
		}
	}
	if !f.acceptedFound {
		return nil
	}
	accepted := f.accepted
	if !f.headFound || accepted.Head == (runtime.FleetHead{}) || accepted.Head.Identity != f.identity || accepted.Head.Revision > f.head.Revision || len(accepted.History) == 0 || accepted.History[len(accepted.History)-1].GenerationID != accepted.Head.GenerationID {
		return errors.New("invalid captured fleet acceptance")
	}
	return accepted.Head.Validate()
}

func (f *capturedFleet) inspectPublications(ctx context.Context) error {
	for _, blob := range f.inventory.Entries {
		data, err := readFleetBlob(ctx, f.prefix, blob, f.read)
		if err != nil {
			return err
		}
		snapshot, err := decodeFleetBlob(data, blob)
		if err != nil {
			return err
		}
		if err := runtime.ValidateFleetRecovery(ctx, snapshot); err != nil {
			return err
		}
		if err := f.inspectFleetAdoption(ctx, f.prefix, blob); err != nil {
			return err
		}
		f.maxGrant = max(f.maxGrant, snapshot.Publication.Grant.Epoch)
		entry, err := fleetIndexEntry(snapshot.Publication.Generation)
		if err != nil {
			return err
		}
		if prior, found := f.generations[entry.GenerationID]; found && !sameCapturedEntry(prior, entry) {
			return errors.New("captured fleet generation has conflicting content")
		}
		f.generations[entry.GenerationID] = entry
	}
	if f.headFound && !f.heads[f.head] || f.acceptedFound && !f.heads[f.accepted.Head] {
		return errors.New("captured fleet selection has no retained publication")
	}
	return validateCapturedHistory(f.accepted.History, f.generations)
}

func (f *capturedFleet) inspectPending() error {
	pending := f.inventory.Pending
	if pending == nil {
		return nil
	}
	if f.heads[pending.Head] || pending.Head == f.head || pending.Head == f.accepted.Head {
		return errors.New("pending fleet publication selects retained state")
	}
	for _, entry := range f.accepted.History {
		if entry.GenerationID == pending.Head.GenerationID {
			if !f.retainsGeneration(entry.GenerationID) {
				return errors.New("pending fleet deletion removes retained history")
			}
		}
	}
	// Pending uploads and deletions may contain only part of their declared bytes.
	// Preserve those bytes for owner recovery without selecting the publication.
	f.publications[fleetPublicationKey(f.prefix, pending.Head)] = *pending
	addCapturedFleetChunks(f.prefix, *pending, f.chunks)
	return nil
}

func (f *capturedFleet) inspectRecord(ctx context.Context, record storage.TransferRecord) error {
	if !strings.HasPrefix(record.Key, "catalog:fleet:") {
		return nil
	}
	if !strings.HasPrefix(record.Key, f.prefix) {
		return errors.New("captured fleet record belongs to another deployment or schema")
	}
	suffix := strings.TrimPrefix(record.Key, f.prefix)
	if suffix != "lease" && suffix != "maintenance" && record.ExpiresAtMillis != 0 {
		return errors.New("durable fleet backup record has an expiration")
	}
	switch suffix {
	case "inventory", "epoch", "head", "accepted":
		return nil
	case "inventory-initialized":
		if !f.inventoryFound {
			return errors.New("captured fleet lost its initialized inventory")
		}
		return nil
	case "head-initialized":
		if !f.headFound {
			return errors.New("captured fleet lost its initialized head")
		}
		return nil
	case "maintenance":
		if !fleetBlobToken(string(record.Value)) || record.ExpiresAtMillis == 0 {
			return errors.New("invalid captured fleet maintenance lease")
		}
		return nil
	case "lease":
		return f.inspectLease(record)
	}
	return f.inspectRetainedRecord(ctx, record, suffix)
}

func (f *capturedFleet) inspectRetainedRecord(ctx context.Context, record storage.TransferRecord, suffix string) error {
	if expected, found := f.publications[record.Key]; found {
		var actual fleetBlob
		if len(record.Value) > fleetDescriptorMaxBytes || json.Unmarshal(record.Value, &actual) != nil || !reflect.DeepEqual(expected, actual) {
			return errors.New("captured fleet receipt differs from its inventory")
		}
		return nil
	}
	if chunk, found := f.chunks[record.Key]; found {
		if len(record.Value) != chunk.size || payloadDigest(record.Value) != chunk.digest {
			return errors.New("captured fleet chunk differs from its inventory")
		}
		return nil
	}
	if strings.HasPrefix(suffix, "adoption-receipt:") || strings.HasPrefix(suffix, "adoption-operation:") {
		return f.inspectAdoptionRecord(ctx, f.prefix, record)
	}
	return errors.New("captured fleet contains an unowned record")
}

func (f *capturedFleet) inspectLease(record storage.TransferRecord) error {
	var grant fleetGrant
	if len(record.Value) > 4096 || record.ExpiresAtMillis == 0 || json.Unmarshal(record.Value, &grant) != nil || grant.Epoch == 0 || !f.epochFound || grant.Epoch != f.epoch || grant.Holder == "" || grant.Session == "" {
		return errors.New("invalid captured fleet lease")
	}
	if f.identity != (runtime.FleetIdentity{}) && grant.Identity != f.identity {
		return errors.New("captured fleet lease differs from the retained identity")
	}
	return validateCapturedFleetIdentity(grant.Identity, f.boundary)
}

type capturedFleetChunk struct {
	digest string
	size   int
}

func addCapturedFleetChunks(prefix string, blob fleetBlob, chunks map[string]capturedFleetChunk) {
	for index, digest := range blob.Record.Chunks {
		chunks[prefix+"blob:"+blob.ID+":"+digest] = capturedFleetChunk{digest, min(generationChunkSize, blob.Record.Size-index*generationChunkSize)}
	}
}

func sameCapturedEntry(a, b GenerationIndexEntry) bool {
	return a.GenerationID == b.GenerationID && a.GeneratedAt.Equal(b.GeneratedAt) && a.PayloadChecksum == b.PayloadChecksum && a.SemanticChecksum == b.SemanticChecksum
}

func validateCapturedFleetIdentity(identity runtime.FleetIdentity, boundary recovery.Record) error {
	if err := identity.Validate(); err != nil {
		return err
	}
	if boundary.Epoch <= 0 {
		return errors.New("invalid captured recovery boundary")
	}
	if identity.DeploymentID != boundary.DeploymentID || identity.BackendID != boundary.BackendID || identity.RecoveryEpoch >= uint64(boundary.Epoch) {
		return errors.New("captured fleet identity differs from the closed recovery boundary")
	}
	return nil
}

func (c capturedCatalog) decodeOptional(ctx context.Context, key string, limit int, target any) (bool, error) {
	data, err := c.read(ctx, key, limit)
	if errors.Is(err, storage.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, json.Unmarshal(data, target, json.RejectUnknownMembers(true))
}

func (c capturedCatalog) requireMarker(ctx context.Context, key, expected string) error {
	data, err := c.read(ctx, key, 64)
	if err != nil {
		return err
	}
	if string(data) != expected {
		return errors.New("invalid captured fleet initialization marker")
	}
	return nil
}

func (c capturedCatalog) capturedFleetEpoch(ctx context.Context, prefix string) (uint64, bool, error) {
	data, err := c.read(ctx, prefix+"epoch", 32)
	if errors.Is(err, storage.ErrNotFound) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	epoch, err := strconv.ParseUint(string(data), 10, 64)
	if err != nil || epoch == 0 || strconv.FormatUint(epoch, 10) != string(data) {
		return 0, false, errors.New("invalid captured fleet epoch")
	}
	return epoch, true, nil
}

func (c capturedCatalog) inspectFleetAdoption(ctx context.Context, prefix string, blob fleetBlob) error {
	if blob.Adoption == nil {
		return nil
	}
	data, err := c.read(ctx, prefix+"adoption-receipt:"+blob.Adoption.Receipt, fleetDescriptorMaxBytes)
	if err != nil {
		return err
	}
	var receipt fleetAdoptionReceipt
	if payloadDigest(data) != blob.Adoption.Receipt || json.Unmarshal(data, &receipt) != nil || receipt.validate() != nil {
		return errors.New("invalid captured catalog adoption receipt")
	}
	identity, err := adoptionFleetIdentity(receipt.Request.approval())
	if err != nil {
		return err
	}
	if identity != blob.Head.Identity || receipt.Request.Head.Identity != blob.Adoption.Previous.Identity {
		return errors.New("captured catalog adoption differs from its publication")
	}
	return nil
}

func (c capturedCatalog) inspectAdoptionRecord(ctx context.Context, prefix string, record storage.TransferRecord) error {
	var receipt fleetAdoptionReceipt
	if len(record.Value) > fleetDescriptorMaxBytes || json.Unmarshal(record.Value, &receipt, json.RejectUnknownMembers(true)) != nil || receipt.validate() != nil {
		return errors.New("invalid captured catalog adoption record")
	}
	receiptKey := prefix + "adoption-receipt:" + payloadDigest(record.Value)
	operationKey := prefix + "adoption-operation:" + payloadDigest([]byte(receipt.Request.OperationID))
	if record.Key != receiptKey && record.Key != operationKey || receipt.Request.Closed.DeploymentID != receipt.Request.Head.Identity.DeploymentID || prefix != "catalog:fleet:{"+payloadDigest([]byte(receipt.Request.Closed.DeploymentID))+"}:v1:" {
		return errors.New("captured adoption record differs from its key")
	}
	otherKey := receiptKey
	if record.Key == receiptKey {
		otherKey = operationKey
	}
	other, err := c.read(ctx, otherKey, fleetDescriptorMaxBytes)
	if err != nil {
		return err
	}
	if string(other) != string(record.Value) {
		return errors.New("captured adoption operation differs from its receipt")
	}
	return nil
}

func (f *capturedFleet) retainsGeneration(id string) bool {
	for head := range f.heads {
		if head.GenerationID == id {
			return true
		}
	}
	return false
}
