package catalog

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"time"

	"github.com/agentstation/starmap/pkg/catalogs"

	"github.com/agentstation/starport/internal/storage"
)

const (
	catalogGenerationIndexKey = "catalog_generation:v1:index"
	// catalogGenerationIndexCap bounds the acceptance history to one small
	// ordered record, so freshness reads never scan generation records.
	catalogGenerationIndexCap      = 32
	catalogGenerationIndexAttempts = 5
)

// GenerationIndexEntry records one accepted generation in acceptance order.
// The semantic checksum excludes provenance, so the diff service can skip
// provenance-only churn without decoding payloads.
type GenerationIndexEntry struct {
	GenerationID     string    `json:"generation_id"`
	GeneratedAt      time.Time `json:"generated_at"`
	PayloadChecksum  string    `json:"payload_checksum"`
	SemanticChecksum string    `json:"semantic_checksum,omitempty"`
}

// History returns accepted generations in acceptance order, oldest first.
// A store without an index (the remote head store) reports no history.
func (s *GenerationStore) History(ctx context.Context) ([]GenerationIndexEntry, error) {
	if s.indexKey == "" {
		return nil, nil
	}
	encoded, err := s.store.Get(ctx, s.indexKey)
	if err != nil {
		if stderrors.Is(err, storage.ErrNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("read catalog generation index: %w", err)
	}
	var entries []GenerationIndexEntry
	if err := json.Unmarshal(encoded, &entries); err != nil {
		return nil, fmt.Errorf("decode catalog generation index: %w", err)
	}
	return entries, nil
}

// appendIndexEntry records one accepted generation after its pointer swap.
// Commit is idempotent, so a caller retry after an index failure is safe.
func (s *GenerationStore) appendIndexEntry(ctx context.Context, generation catalogs.Generation) error {
	if s.indexKey == "" {
		return nil
	}
	entry := GenerationIndexEntry{
		GenerationID:    generation.Manifest.GenerationID,
		GeneratedAt:     generation.Manifest.GeneratedAt,
		PayloadChecksum: generation.Manifest.Payload.Checksum,
	}
	decoded, err := catalogs.DecodeCatalogPayload(generation.Payload)
	if err != nil {
		return fmt.Errorf("decode payload for catalog generation index %q: %w", entry.GenerationID, err)
	}
	semantic, err := catalogs.CatalogSemanticChecksum(decoded)
	if err != nil {
		return fmt.Errorf("checksum payload for catalog generation index %q: %w", entry.GenerationID, err)
	}
	entry.SemanticChecksum = semantic

	for attempt := 0; attempt < catalogGenerationIndexAttempts; attempt++ {
		existing, err := s.store.Get(ctx, s.indexKey)
		if err != nil && !stderrors.Is(err, storage.ErrNotFound) {
			return fmt.Errorf("read catalog generation index: %w", err)
		}
		var entries []GenerationIndexEntry
		if len(existing) > 0 {
			if err := json.Unmarshal(existing, &entries); err != nil {
				return fmt.Errorf("decode catalog generation index: %w", err)
			}
		}
		if indexContains(entries, entry.GenerationID) {
			return nil
		}
		entries = append(entries, entry)
		if len(entries) > catalogGenerationIndexCap {
			entries = entries[len(entries)-catalogGenerationIndexCap:]
		}
		encoded, err := json.Marshal(entries)
		if err != nil {
			return fmt.Errorf("encode catalog generation index: %w", err)
		}
		if err := s.store.CompareAndSwap(ctx, s.indexKey, existing, encoded); err != nil {
			if stderrors.Is(err, storage.ErrConflict) {
				continue
			}
			return fmt.Errorf("write catalog generation index: %w", err)
		}
		return nil
	}
	return fmt.Errorf("write catalog generation index %q: concurrent updates exhausted retries", entry.GenerationID)
}

func indexContains(entries []GenerationIndexEntry, generationID string) bool {
	for _, entry := range entries {
		if entry.GenerationID == generationID {
			return true
		}
	}
	return false
}
