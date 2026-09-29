package catalog

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"strings"

	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/agentstation/starport/internal/recovery"
	"github.com/agentstation/starport/internal/storage"
)

type capturedCatalogRecords interface {
	ReadCaptured(context.Context, string, int) (storage.TransferRecord, error)
	Enumerate(context.Context, func(storage.TransferRecord) error) error
}

type capturedCatalog struct{ records capturedCatalogRecords }

// InspectCapturedCatalog checks catalog references without opening a runtime or renewing permission.
// The closed backup boundary supplies deployment identity, not activation approval.
func InspectCapturedCatalog(ctx context.Context, view *recovery.KVSnapshotView, boundary recovery.Record) error {
	if view == nil {
		return errors.New("catalog backup requires a captured store")
	}
	return inspectCapturedCatalog(ctx, view, boundary)
}

func inspectCapturedCatalog(ctx context.Context, records capturedCatalogRecords, boundary recovery.Record) error {
	if ctx == nil || records == nil || !validCapturedBoundary(boundary) {
		return errors.New("catalog backup requires a closed recovery boundary")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	captured := capturedCatalog{records}
	generations := map[string]GenerationIndexEntry{}
	pointers := map[string]string{}
	var history []GenerationIndexEntry
	fleetPresent := false
	err := records.Enumerate(ctx, func(record storage.TransferRecord) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if strings.HasPrefix(record.Key, "catalog:fleet:") {
			fleetPresent = true
			return nil
		}
		if !strings.HasPrefix(record.Key, "catalog_generation:") && !strings.HasPrefix(record.Key, "catalog_candidate_generation:") {
			return nil
		}
		if record.ExpiresAtMillis != 0 {
			return errors.New("durable catalog backup record has an expiration")
		}
		switch {
		case record.Key == catalogCurrentGenerationKey || record.Key == candidateCurrentGenerationKey:
			if len(record.Value) == 0 || len(record.Value) > 4096 {
				return errors.New("invalid retained catalog pointer")
			}
			pointers[record.Key] = string(record.Value)
		case record.Key == catalogGenerationIndexKey:
			if len(record.Value) > fleetDescriptorMaxBytes {
				return storage.ErrValueTooLarge
			}
			if err := json.Unmarshal(record.Value, &history, json.RejectUnknownMembers(true)); err != nil {
				return err
			}
			if len(history) > catalogGenerationIndexCap {
				return errors.New("retained catalog history exceeds its bound")
			}
		case strings.HasPrefix(record.Key, catalogGenerationKeyPrefix):
			entry, err := captured.generation(ctx, record)
			if err != nil {
				return err
			}
			generations[entry.GenerationID] = entry
		case strings.HasPrefix(record.Key, catalogGenerationChunkKeyPrefix):
			digest := strings.TrimPrefix(record.Key, catalogGenerationChunkKeyPrefix)
			if len(record.Value) == 0 || len(record.Value) > generationChunkSize || payloadDigest(record.Value) != digest {
				return errors.New("retained catalog chunk differs from its key")
			}
		default:
			return errors.New("unsupported retained catalog record")
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("inspect captured catalog: %w", err)
	}
	for _, id := range pointers {
		if _, found := generations[id]; !found {
			return errors.New("retained catalog pointer selects a missing generation")
		}
	}
	// A crash can leave the accepted pointer ahead of its separately written history.
	// Every recorded entry must still bind exact retained content.
	if err := validateCapturedHistory(history, generations); err != nil {
		return err
	}
	if fleetPresent {
		return captured.inspectFleet(ctx, boundary)
	}
	return nil
}

func validateCapturedHistory(history []GenerationIndexEntry, generations map[string]GenerationIndexEntry) error {
	if len(history) > catalogGenerationIndexCap {
		return errors.New("retained catalog history exceeds its bound")
	}
	seen := map[string]bool{}
	for _, entry := range history {
		expected, found := generations[entry.GenerationID]
		if !found || seen[entry.GenerationID] || !entry.GeneratedAt.Equal(expected.GeneratedAt) || entry.PayloadChecksum != expected.PayloadChecksum || entry.SemanticChecksum != expected.SemanticChecksum {
			return errors.New("retained catalog history differs from its generation")
		}
		seen[entry.GenerationID] = true
	}
	return nil
}

func (c capturedCatalog) read(ctx context.Context, key string, limit int) ([]byte, error) {
	record, err := c.records.ReadCaptured(ctx, key, limit)
	if err != nil {
		return nil, err
	}
	if record.ExpiresAtMillis != 0 {
		return nil, errors.New("durable catalog backup record has an expiration")
	}
	return record.Value, nil
}

func (c capturedCatalog) BatchGet(ctx context.Context, keys []string) (map[string][]byte, error) {
	values := make(map[string][]byte, len(keys))
	for _, key := range keys {
		data, err := c.read(ctx, key, generationChunkSize)
		if err != nil {
			return nil, err
		}
		values[key] = data
	}
	return values, nil
}

func (c capturedCatalog) generation(ctx context.Context, record storage.TransferRecord) (GenerationIndexEntry, error) {
	id := strings.TrimPrefix(record.Key, catalogGenerationKeyPrefix)
	if id == "" || len(id) > 4096 {
		return GenerationIndexEntry{}, errors.New("invalid retained catalog generation key")
	}
	if len(record.Value) > fleetDescriptorMaxBytes {
		return GenerationIndexEntry{}, storage.ErrValueTooLarge
	}
	descriptor, err := decodeGenerationRecord(record.Value, id)
	if err != nil {
		return GenerationIndexEntry{}, err
	}
	payload, err := readGenerationPayload(ctx, c, descriptor, id)
	if err != nil {
		return GenerationIndexEntry{}, err
	}
	var generation catalogs.Generation
	if err := json.Unmarshal(payload, &generation); err != nil {
		return GenerationIndexEntry{}, err
	}
	if generation.Manifest.GenerationID != id {
		return GenerationIndexEntry{}, errors.New("retained catalog generation differs from its key")
	}
	if err := generation.Validate(); err != nil {
		return GenerationIndexEntry{}, err
	}
	return fleetIndexEntry(generation)
}

func validCapturedBoundary(boundary recovery.Record) bool {
	return !boundary.Open && boundary.Epoch > 0 && strings.TrimSpace(boundary.DeploymentID) != ""
}
