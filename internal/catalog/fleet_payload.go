package catalog

import (
	"bytes"
	"context"
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

// stageBlob stores immutable chunks without creating a publication receipt or moving a head.
func (s *FleetStore) stageBlob(ctx context.Context, encoded []byte) ([]byte, error) {
	if len(encoded) == 0 || len(encoded) > fleetEncodedMaxBytes {
		return nil, errors.New("fleet publication exceeds the encoded byte bound")
	}
	record, chunks := encodeGenerationPayload(encoded)
	for key, chunk := range chunks {
		// The content digest belongs to this deployment's namespace.
		key = s.prefix + "chunk:" + key[len(catalogGenerationChunkKeyPrefix):]
		if err := s.store.CompareAndSwap(ctx, []storage.CompareAndSwapMutation{{Key: key, NewValue: chunk}}); err != nil {
			if !errors.Is(err, storage.ErrConflict) {
				return nil, err
			}
			current, _, err := s.store.ReadWithLifetime(ctx, key, generationChunkSize)
			if err != nil {
				return nil, err
			}
			if !bytes.Equal(current, chunk) {
				return nil, fleetStoreConflict("a stored immutable fleet chunk has different content")
			}
		}
	}
	return json.Marshal(record)
}

func (s *FleetStore) readBlob(ctx context.Context, key string) ([]byte, error) {
	encoded, _, err := s.store.ReadWithLifetime(ctx, key, fleetDescriptorMaxBytes)
	if err != nil {
		return nil, fleetReadError(err, key)
	}
	var record generationRecord
	if err := json.Unmarshal(encoded, &record); err != nil {
		return nil, err
	}
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
		chunk, _, err := s.store.ReadWithLifetime(ctx, s.prefix+"chunk:"+digest, generationChunkSize)
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
