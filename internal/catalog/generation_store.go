package catalog

import (
	"bytes"
	"context"
	"encoding/json"
	stderrors "errors"
	"fmt"

	"github.com/agentstation/starmap/pkg/catalogs"
	starmapstorage "github.com/agentstation/starmap/pkg/catalogs/storage"
	starmaperrors "github.com/agentstation/starmap/pkg/errors"

	"github.com/agentstation/starport/internal/storage"
)

const (
	catalogCurrentGenerationKey = "catalog_generation:v1:current"
	remoteCurrentGenerationKey  = "catalog_remote_generation:v1:current"
	catalogGenerationKeyPrefix  = "catalog_generation:v1:generation:"
	catalogGenerationResource   = "catalog generation"
)

// GenerationStore adapts Starport's configured KV store to Starmap's durable
// immutable-generation contract.
type GenerationStore struct {
	store      storage.KVStore
	currentKey string
	// indexKey selects the ordered acceptance-history record. Only the
	// accepted runtime store keeps history; the remote head store leaves it
	// empty and records none.
	indexKey string
}

// NewGenerationStore creates a durable Starmap generation store.
func NewGenerationStore(store storage.KVStore) (*GenerationStore, error) {
	return newGenerationStore(store, catalogCurrentGenerationKey, catalogGenerationIndexKey)
}

// newRemoteGenerationStore creates the verified remote-head store. It shares
// immutable generation records with the accepted runtime store but owns a
// separate current pointer.
func newRemoteGenerationStore(store storage.KVStore) (*GenerationStore, error) {
	return newGenerationStore(store, remoteCurrentGenerationKey, "")
}

func newGenerationStore(store storage.KVStore, currentKey, indexKey string) (*GenerationStore, error) {
	if store == nil {
		return nil, stderrors.New("catalog generation KV store is required")
	}
	if currentKey == "" {
		return nil, stderrors.New("catalog generation current key is required")
	}
	return &GenerationStore{store: store, currentKey: currentKey, indexKey: indexKey}, nil
}

// Current returns the atomically selected generation.
func (s *GenerationStore) Current(ctx context.Context) (catalogs.Generation, error) {
	currentID, err := s.store.Get(ctx, s.currentKey)
	if err != nil {
		if stderrors.Is(err, storage.ErrNotFound) {
			return catalogs.Generation{}, &starmaperrors.NotFoundError{
				Resource: catalogGenerationResource, ID: "current",
			}
		}
		return catalogs.Generation{}, fmt.Errorf("read current catalog generation: %w", err)
	}
	return s.Get(ctx, string(currentID))
}

// Get returns one immutable generation by ID.
func (s *GenerationStore) Get(ctx context.Context, generationID string) (catalogs.Generation, error) {
	encoded, err := s.store.Get(ctx, catalogGenerationKey(generationID))
	if err != nil {
		if stderrors.Is(err, storage.ErrNotFound) {
			return catalogs.Generation{}, &starmaperrors.NotFoundError{
				Resource: catalogGenerationResource, ID: generationID,
			}
		}
		return catalogs.Generation{}, fmt.Errorf("read catalog generation %q: %w", generationID, err)
	}
	var generation catalogs.Generation
	if err := json.Unmarshal(encoded, &generation); err != nil {
		return catalogs.Generation{}, fmt.Errorf("decode catalog generation %q: %w", generationID, err)
	}
	if err := generation.Validate(); err != nil {
		return catalogs.Generation{}, fmt.Errorf("validate catalog generation %q: %w", generationID, err)
	}
	if generation.Manifest.GenerationID != generationID {
		return catalogs.Generation{}, fmt.Errorf(
			"catalog generation key %q contains generation %q",
			generationID,
			generation.Manifest.GenerationID,
		)
	}
	return generation, nil
}

// Commit stores one immutable generation, then selects it with compare-and-swap.
func (s *GenerationStore) Commit(
	ctx context.Context,
	generation catalogs.Generation,
	expectedGenerationID string,
) error {
	if err := generation.Validate(); err != nil {
		return err
	}
	encoded, err := json.Marshal(generation)
	if err != nil {
		return fmt.Errorf("encode catalog generation %q: %w", generation.Manifest.GenerationID, err)
	}
	generationKey := catalogGenerationKey(generation.Manifest.GenerationID)
	if err := s.store.CompareAndSwap(ctx, generationKey, nil, encoded); err != nil {
		if !stderrors.Is(err, storage.ErrConflict) {
			return fmt.Errorf("store catalog generation %q: %w", generation.Manifest.GenerationID, err)
		}
		existing, readErr := s.store.Get(ctx, generationKey)
		if readErr != nil {
			return fmt.Errorf("read existing catalog generation %q: %w", generation.Manifest.GenerationID, readErr)
		}
		if !bytes.Equal(existing, encoded) {
			return &starmaperrors.ConflictError{
				Resource: catalogGenerationResource,
				Expected: generation.Manifest.GenerationID,
				Actual:   generation.Manifest.GenerationID,
				Message:  "generation ID is already bound to different content",
			}
		}
	}

	var expected []byte
	if expectedGenerationID != "" {
		expected = []byte(expectedGenerationID)
	}
	actual := []byte(generation.Manifest.GenerationID)
	if err := s.store.CompareAndSwap(ctx, s.currentKey, expected, actual); err != nil {
		if !stderrors.Is(err, storage.ErrConflict) {
			return fmt.Errorf("select catalog generation %q: %w", generation.Manifest.GenerationID, err)
		}
		current, readErr := s.store.Get(ctx, s.currentKey)
		if readErr == nil && bytes.Equal(current, actual) {
			return s.appendIndexEntry(ctx, generation)
		}
		if readErr != nil && !stderrors.Is(readErr, storage.ErrNotFound) {
			return fmt.Errorf("read current catalog generation after conflict: %w", readErr)
		}
		return &starmaperrors.ConflictError{
			Resource: "catalog current generation",
			Expected: expectedGenerationID,
			Actual:   string(current),
		}
	}
	return s.appendIndexEntry(ctx, generation)
}

func catalogGenerationKey(generationID string) string {
	return catalogGenerationKeyPrefix + generationID
}

var _ starmapstorage.Store = (*GenerationStore)(nil)
