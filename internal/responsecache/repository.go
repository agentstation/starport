package responsecache

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/agentstation/starport/internal/inference"
)

// RecordSchemaVersion identifies the canonical response-cache record schema.
const RecordSchemaVersion = 1

var (
	// ErrStoreRequired reports an absent response-cache byte store.
	ErrStoreRequired = errors.New("response-cache store is required")
	// ErrCorruptRecord reports invalid durable response-cache data.
	ErrCorruptRecord = errors.New("response-cache record is invalid")
	// ErrKindMismatch reports a record payload that does not match its kind.
	ErrKindMismatch = errors.New("response-cache record kind does not match")
)

// Store is the byte-cache contract required by the repository.
type Store interface {
	GetResponse(context.Context, string) ([]byte, bool, error)
	SetResponse(context.Context, string, []byte) error
}

// Clock supplies deterministic record time.
type Clock interface {
	Now() time.Time
}

// Repository stores versioned canonical inference results.
type Repository interface {
	GetChat(context.Context, string) (inference.ChatResponse, time.Time, bool, error)
	PutChat(context.Context, string, inference.ChatResponse) error
	GetEmbedding(context.Context, string) (inference.EmbeddingResponse, time.Time, bool, error)
	PutEmbedding(context.Context, string, inference.EmbeddingResponse) error
}

type repository struct {
	store Store
	clock Clock
}

type record struct {
	SchemaVersion int                          `json:"schema_version"`
	Kind          string                       `json:"kind"`
	SemanticKey   string                       `json:"semantic_key"`
	CachedAt      time.Time                    `json:"cached_at"`
	Chat          *inference.ChatResponse      `json:"chat,omitempty"`
	Embedding     *inference.EmbeddingResponse `json:"embedding,omitempty"`
}

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now() }

// Open creates a canonical response-cache repository.
func Open(store Store, clock Clock) (Repository, error) {
	if store == nil {
		return nil, ErrStoreRequired
	}
	if clock == nil {
		clock = systemClock{}
	}
	return &repository{store: store, clock: clock}, nil
}

func (r *repository) GetChat(ctx context.Context, key string) (inference.ChatResponse, time.Time, bool, error) {
	stored, found, err := r.get(ctx, key, "chat")
	if err != nil || !found {
		return inference.ChatResponse{}, time.Time{}, found, err
	}
	if stored.Chat == nil || stored.Embedding != nil {
		return inference.ChatResponse{}, time.Time{}, false, ErrKindMismatch
	}
	return stored.Chat.Clone(), stored.CachedAt, true, nil
}

func (r *repository) PutChat(ctx context.Context, key string, response inference.ChatResponse) error {
	response = response.Clone()
	return r.put(ctx, key, record{Kind: "chat", Chat: &response})
}

func (r *repository) GetEmbedding(ctx context.Context, key string) (inference.EmbeddingResponse, time.Time, bool, error) {
	stored, found, err := r.get(ctx, key, "embedding")
	if err != nil || !found {
		return inference.EmbeddingResponse{}, time.Time{}, found, err
	}
	if stored.Embedding == nil || stored.Chat != nil {
		return inference.EmbeddingResponse{}, time.Time{}, false, ErrKindMismatch
	}
	return stored.Embedding.Clone(), stored.CachedAt, true, nil
}

func (r *repository) PutEmbedding(ctx context.Context, key string, response inference.EmbeddingResponse) error {
	response = response.Clone()
	return r.put(ctx, key, record{Kind: "embedding", Embedding: &response})
}

func (r *repository) get(ctx context.Context, key, kind string) (record, bool, error) {
	data, found, err := r.store.GetResponse(ctx, key)
	if err != nil || !found {
		return record{}, found, err
	}
	var stored record
	if err := json.Unmarshal(data, &stored); err != nil {
		return record{}, false, fmt.Errorf("%w: decode: %v", ErrCorruptRecord, err)
	}
	if stored.SchemaVersion != RecordSchemaVersion || stored.SemanticKey != key || stored.CachedAt.IsZero() {
		return record{}, false, ErrCorruptRecord
	}
	if stored.Kind != kind {
		return record{}, false, ErrKindMismatch
	}
	return stored, true, nil
}

func (r *repository) put(ctx context.Context, key string, stored record) error {
	if key == "" {
		return ErrCorruptRecord
	}
	stored.SchemaVersion = RecordSchemaVersion
	stored.SemanticKey = key
	stored.CachedAt = r.clock.Now()
	data, err := json.Marshal(stored)
	if err != nil {
		return fmt.Errorf("encode response-cache record: %w", err)
	}
	return r.store.SetResponse(ctx, key, data)
}
