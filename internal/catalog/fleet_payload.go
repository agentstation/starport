package catalog

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/agentstation/starmap/runtime"

	"github.com/agentstation/starport/internal/storage"
)

const (
	fleetDescriptorMaxBytes = 1 << 20
	fleetEncodedMaxBytes    = 2*(runtime.MaxFleetRecoveryBytes+catalogs.MaxCatalogPayloadBytes) + 1<<20
)

// stageBlob records ownership before writing immutable chunks.
func (m *fleetMaintenance) stageBlob(ctx context.Context, head runtime.FleetHead, encoded, grant []byte) (fleetBlob, error) {
	if len(encoded) == 0 || len(encoded) > fleetEncodedMaxBytes {
		return fleetBlob{}, errors.New("fleet publication exceeds the encoded byte bound")
	}
	record, chunks := encodeGenerationPayload(encoded)
	blob := fleetBlob{ID: rand.Text(), Head: head, Record: record}
	m.inventory.Pending = &blob
	if err := m.save(ctx); err != nil {
		return fleetBlob{}, err
	}
	for key, chunk := range chunks {
		key = m.chunkKey(blob, key[len(catalogGenerationChunkKeyPrefix):])
		if err := m.mutate(ctx, []storage.CompareAndSwapMutation{{Key: key, NewValue: chunk}, {Key: m.owner.prefix + "lease", ExpectedValue: grant, NewValue: grant}}, m.owner.prefix+"lease"); err != nil {
			return fleetBlob{}, err
		}
	}
	return blob, nil
}

func (s *FleetStore) readBlob(ctx context.Context, blob fleetBlob) ([]byte, error) {
	encoded, _, err := s.store.ReadWithLifetime(ctx, s.publicationKey(blob.Head), fleetDescriptorMaxBytes)
	if err != nil {
		return nil, fmt.Errorf("read retained fleet receipt: %w", err)
	}
	expected, _ := json.Marshal(blob)
	if !bytes.Equal(encoded, expected) {
		return nil, errors.New("fleet receipt differs from its retention inventory")
	}
	record := blob.Record
	if record.Encoding != generationEncodingChunked || record.ChunkSize != generationChunkSize ||
		record.Size <= 0 || record.Size > fleetEncodedMaxBytes ||
		len(record.Chunks) != (record.Size+generationChunkSize-1)/generationChunkSize {
		return nil, errors.New("invalid fleet publication descriptor")
	}
	data := make([]byte, 0, record.Size)
	for _, digest := range record.Chunks {
		if len(digest) != 64 {
			return nil, errors.New("invalid fleet chunk digest")
		}
		chunk, _, err := s.store.ReadWithLifetime(ctx, s.prefix+"blob:"+blob.ID+":"+digest, generationChunkSize)
		if err != nil {
			return nil, fmt.Errorf("read selected fleet chunk: %w", err)
		}
		if len(chunk) != min(generationChunkSize, record.Size-len(data)) || payloadDigest(chunk) != digest {
			return nil, errors.New("fleet chunk differs from its descriptor")
		}
		data = append(data, chunk...)
	}
	if payloadDigest(data) != record.Digest {
		return nil, errors.New("fleet publication checksum mismatch")
	}
	return data, nil
}
