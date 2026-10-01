package catalog

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"

	"github.com/agentstation/starmap/runtime"
	"github.com/agentstation/starport/internal/recovery"
	"github.com/agentstation/starport/internal/storage"
)

const (
	topologyArchivePrefix         = "catalog:archive:v1:"
	fleetLeaseKind                = "lease"
	fleetHeadKind                 = "head"
	fleetInventoryKind            = "inventory"
	fleetMaintenanceKind          = "maintenance"
	fleetInventoryInitializedKind = "inventory-initialized"
	fleetHeadInitializedKind      = "head-initialized"
)

type historicalFleetRecord struct {
	Kind            string           `json:"kind"`
	Identity        string           `json:"identity,omitempty"`
	Value           []byte           `json:"-"`
	Record          generationRecord `json:"record"`
	ExpiresAtMillis int64            `json:"expires_at_millis,omitempty"`
}

type fleetHistoricalData struct {
	Version     int                     `json:"version"`
	SourceBytes int64                   `json:"source_bytes"`
	Boundary    recovery.Record         `json:"boundary"`
	Identity    runtime.FleetIdentity   `json:"identity"`
	Head        runtime.FleetHead       `json:"head"`
	Accepted    fleetAcceptance         `json:"accepted"`
	Inventory   fleetInventory          `json:"inventory"`
	Records     []historicalFleetRecord `json:"records"`
}

func (a fleetHistoricalData) prefix() string {
	return "catalog:fleet:{" + payloadDigest([]byte(a.Boundary.DeploymentID)) + "}:v1:"
}

// FleetHistoricalArchive preserves source fleet evidence without granting destination authority.
// It is private, immutable, and created only from checked captured records.
type FleetHistoricalArchive struct{ data *fleetHistoricalData }

// Format omits archived ownership tokens and private records.
func (a FleetHistoricalArchive) Format(state fmt.State, _ rune) {
	_, _ = fmt.Fprint(state, "historical fleet catalog (private)")
}

func captureFleetHistoricalArchive(ctx context.Context, records capturedCatalogRecords, boundary recovery.Record) (*FleetHistoricalArchive, error) {
	archive := &fleetHistoricalData{Version: 1, Boundary: boundary}
	err := records.Enumerate(ctx, func(record storage.TransferRecord) error {
		if !strings.HasPrefix(record.Key, "catalog:fleet:") {
			return nil
		}
		if !strings.HasPrefix(record.Key, archive.prefix()) {
			return errors.New("historical catalog belongs to another source deployment")
		}
		kind, identity, found := splitHistoricalKind(strings.TrimPrefix(record.Key, archive.prefix()))
		if !found {
			return errors.New("unsupported historical fleet record")
		}
		next, err := addHistoricalSourceBytes(archive.SourceBytes, int64(len(record.Key)), int64(len(record.Value)))
		if err != nil {
			return err
		}
		archive.SourceBytes = next
		descriptor, _ := encodeGenerationPayload(record.Value)
		archive.Records = append(archive.Records, historicalFleetRecord{Kind: kind, Identity: identity, Value: bytes.Clone(record.Value), Record: descriptor, ExpiresAtMillis: record.ExpiresAtMillis})
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(archive.Records) == 0 {
		return nil, nil
	}
	if err = (capturedCatalog{records}).inspectFleet(ctx, boundary); err != nil {
		return nil, err
	}
	c := capturedCatalog{records}
	for _, field := range []struct {
		suffix string
		target any
	}{{fleetHeadKind, &archive.Head}, {string(OperationAccepted), &archive.Accepted}, {fleetInventoryKind, &archive.Inventory}} {
		data, err := c.read(ctx, archive.prefix()+field.suffix, fleetRetentionRecordBytes)
		if err != nil {
			return nil, err
		}
		if err = json.Unmarshal(data, field.target, json.RejectUnknownMembers(true)); err != nil {
			return nil, err
		}
	}
	archive.Identity = archive.Head.Identity
	slices.SortFunc(archive.Records, func(a, b historicalFleetRecord) int {
		return strings.Compare(a.Kind+":"+a.Identity, b.Kind+":"+b.Identity)
	})
	return &FleetHistoricalArchive{data: archive}, nil
}

func splitHistoricalKind(suffix string) (string, string, bool) {
	for _, kind := range []string{fleetInventoryKind, "epoch", fleetHeadKind, string(OperationAccepted), fleetInventoryInitializedKind, fleetHeadInitializedKind, fleetMaintenanceKind, fleetLeaseKind} {
		if suffix == kind {
			return kind, "", true
		}
	}
	for _, kind := range []string{"blob", "publication", "adoption-receipt", "adoption-operation"} {
		if id, found := strings.CutPrefix(suffix, kind+":"); found && id != "" {
			return kind, id, true
		}
	}
	return "", "", false
}

type historicalFleetView struct {
	records map[string]storage.TransferRecord
}

func (v historicalFleetView) ReadCaptured(ctx context.Context, key string, limit int) (storage.TransferRecord, error) {
	if err := ctx.Err(); err != nil {
		return storage.TransferRecord{}, err
	}
	record, found := v.records[key]
	if !found {
		return storage.TransferRecord{}, storage.ErrNotFound
	}
	if len(record.Value) > limit {
		return storage.TransferRecord{}, storage.ErrValueTooLarge
	}
	return record, nil
}
func (v historicalFleetView) Enumerate(ctx context.Context, visit func(storage.TransferRecord) error) error {
	for _, record := range v.records {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := visit(record); err != nil {
			return err
		}
	}
	return nil
}

func (a fleetHistoricalData) validate(ctx context.Context) error {
	if a.Version != 1 || !validCapturedBoundary(a.Boundary) || a.Identity != a.Head.Identity {
		return errors.New("invalid historical fleet identity")
	}
	view := historicalFleetView{records: map[string]storage.TransferRecord{}}
	var total int64
	for _, record := range a.Records {
		suffix := record.Kind
		if record.Identity != "" {
			suffix += ":" + record.Identity
		}
		kind, id, found := splitHistoricalKind(suffix)
		if !found || kind != record.Kind || id != record.Identity {
			return errors.New("invalid historical fleet record selector")
		}
		key := a.prefix() + suffix
		if _, duplicate := view.records[key]; duplicate {
			return errors.New("duplicate historical fleet record")
		}
		if record.Record.Size != len(record.Value) || record.Record.Digest != payloadDigest(record.Value) {
			return errors.New("historical record differs from its immutable descriptor")
		}
		next, err := addHistoricalSourceBytes(total, int64(len(key)), int64(len(record.Value)))
		if err != nil {
			return err
		}
		total = next
		view.records[key] = storage.TransferRecord{Key: key, Value: record.Value, ExpiresAtMillis: record.ExpiresAtMillis}
	}
	if total != a.SourceBytes {
		return errors.New("historical source census differs from its declared size")
	}
	var head runtime.FleetHead
	var accepted fleetAcceptance
	var inventory fleetInventory
	for _, field := range []struct {
		suffix string
		target any
	}{{fleetHeadKind, &head}, {string(OperationAccepted), &accepted}, {fleetInventoryKind, &inventory}} {
		record, found := view.records[a.prefix()+field.suffix]
		if !found {
			return errors.New("historical fleet lost selection evidence")
		}
		if err := json.Unmarshal(record.Value, field.target, json.RejectUnknownMembers(true)); err != nil {
			return err
		}
	}
	if head != a.Head || !reflect.DeepEqual(accepted, a.Accepted) || !reflect.DeepEqual(inventory, a.Inventory) {
		return errors.New("historical fleet selection differs from retained evidence")
	}

	return (capturedCatalog{view}).inspectFleet(ctx, a.Boundary)
}

func inspectTopologyArchives(ctx context.Context, c capturedCatalog) error {
	var descriptors []storage.TransferRecord
	if err := c.records.Enumerate(ctx, func(record storage.TransferRecord) error {
		if !strings.HasPrefix(record.Key, topologyArchivePrefix) {
			return nil
		}
		if record.ExpiresAtMillis != 0 {
			return errors.New("historical catalog archive must not expire")
		}
		suffix := strings.TrimPrefix(record.Key, topologyArchivePrefix)
		if digest, found := strings.CutPrefix(suffix, "chunk:"); found {
			if len(record.Value) == 0 || len(record.Value) > generationChunkSize || payloadDigest(record.Value) != digest {
				return errors.New("historical archive chunk differs from its identity")
			}
			return nil
		}
		if digest, found := strings.CutPrefix(suffix, "record:"); found && fleetChunkDigest(digest) {
			descriptors = append(descriptors, record)
			return nil
		}
		return errors.New("unsupported historical archive record")
	}); err != nil {
		return err
	}
	for _, retained := range descriptors {
		var descriptor generationRecord
		if len(retained.Value) > fleetDescriptorMaxBytes || json.Unmarshal(retained.Value, &descriptor, json.RejectUnknownMembers(true)) != nil || validateGenerationDescriptor(descriptor, topologyArchiveMetadataMaxBytes) != nil {
			return errors.New("invalid historical archive descriptor")
		}
		if retained.Key != topologyArchivePrefix+"record:"+descriptor.Digest {
			return errors.New("historical archive descriptor differs from its key")
		}
		payload, err := readArchivedPayload(ctx, c, descriptor, topologyArchiveMetadataMaxBytes)
		if err != nil {
			return err
		}
		var archive fleetHistoricalData
		if err := json.Unmarshal(payload, &archive, json.RejectUnknownMembers(true)); err != nil {
			return err
		}
		if err := archive.validateCensus(); err != nil {
			return err
		}
		for index := range archive.Records {
			value, err := readArchivedPayload(ctx, c, archive.Records[index].Record, fleetRetentionMaxBytes)
			if err != nil {
				return err
			}
			archive.Records[index].Value = value
		}
		if err := archive.validate(ctx); err != nil {
			return err
		}
	}
	return nil
}

// The metadata repeats the checked source inventory and two selection records.
// Each final publication chunk and each scalar control descriptor adds at most 1 KiB.
// Raw source values stay in separate chunks. Source key selectors contain bounded ASCII.
const topologyArchiveMetadataMaxBytes = fleetRetentionMaxBytes + fleetRetentionRecordBytes + 2*fleetDescriptorMaxBytes + (fleetRetentionMaxEntries+9)*(1<<10)

func readArchivedPayload(ctx context.Context, c capturedCatalog, descriptor generationRecord, limit int) ([]byte, error) {
	if err := validateGenerationDescriptor(descriptor, limit); err != nil {
		return nil, err
	}
	payload := make([]byte, 0, descriptor.Size)
	for _, digest := range descriptor.Chunks {
		chunk, err := c.read(ctx, topologyArchivePrefix+"chunk:"+digest, generationChunkSize)
		if err != nil {
			return nil, err
		}
		if len(chunk) != min(generationChunkSize, descriptor.Size-len(payload)) || payloadDigest(chunk) != digest {
			return nil, errors.New("historical archive chunk differs from descriptor")
		}
		payload = append(payload, chunk...)
	}
	if payloadDigest(payload) != descriptor.Digest {
		return nil, errors.New("historical archive checksum differs")
	}
	return payload, nil
}

func addHistoricalSourceBytes(total, keyBytes, valueBytes int64) (int64, error) {
	if total < 0 || total > fleetRetentionMaxBytes || keyBytes < 0 || keyBytes > fleetRetentionMaxBytes-total {
		return 0, storage.ErrValueTooLarge
	}
	total += keyBytes
	if valueBytes < 0 || valueBytes > fleetRetentionMaxBytes-total {
		return 0, storage.ErrValueTooLarge
	}
	return total + valueBytes, nil
}

func (a fleetHistoricalData) validateCensus() error {
	var total int64
	for _, record := range a.Records {
		if err := validateGenerationDescriptor(record.Record, fleetRetentionMaxBytes); err != nil {
			return err
		}
		suffix := record.Kind
		if record.Identity != "" {
			suffix += ":" + record.Identity
		}
		next, err := addHistoricalSourceBytes(total, int64(len(a.prefix()+suffix)), int64(record.Record.Size))
		if err != nil {
			return err
		}
		total = next
	}
	if total != a.SourceBytes {
		return errors.New("historical source census differs from its declared size")
	}
	return nil
}
