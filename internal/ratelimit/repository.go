package ratelimit

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/agentstation/starport/internal/storage"
)

const (
	// StorageSchemaVersion identifies the only rate-limit schema.
	StorageSchemaVersion = 1
	// StoragePrefix is the rate-limit v1 namespace.
	StoragePrefix = "ratelimit:v1:subject:"

	maxCASAttempts = 64
)

var (
	// ErrRepositoryRequired reports an absent rate-limit storage adapter.
	ErrRepositoryRequired = errors.New("rate-limit storage is required")
	// ErrInvalidSubject reports an empty rate-limit identity.
	ErrInvalidSubject = errors.New("rate-limit subject is required")
	// ErrInvalidLimit reports a non-positive request limit.
	ErrInvalidLimit = errors.New("rate-limit limit must be positive")
	// ErrInvalidWindow reports a non-positive limit window.
	ErrInvalidWindow = errors.New("rate-limit window must be positive")
	// ErrConflict reports exhausted compare-and-swap attempts.
	ErrConflict = errors.New("rate-limit update conflict")
	// ErrCorruptRecord reports invalid durable rate-limit data.
	ErrCorruptRecord = errors.New("rate-limit record is invalid")
)

// Clock supplies deterministic repository time.
type Clock interface {
	Now() time.Time
}

// Repository is the atomic rate-limit contract.
type Repository interface {
	Consume(context.Context, string, int64, time.Duration) (Decision, error)
}

type repository struct {
	store storage.KVStore
	clock Clock
}

type windowRecord struct {
	SchemaVersion int       `json:"schema_version"`
	Revision      uint64    `json:"revision"`
	Count         int64     `json:"count"`
	ResetAt       time.Time `json:"reset_at"`
}

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now() }

// Open returns a storage-backed fixed-window repository.
func Open(store storage.KVStore, clock Clock) (Repository, error) {
	if store == nil {
		return nil, ErrRepositoryRequired
	}
	if clock == nil {
		clock = systemClock{}
	}
	return &repository{store: store, clock: clock}, nil
}

func (r *repository) Consume(ctx context.Context, subject string, limit int64, window time.Duration) (Decision, error) {
	if strings.TrimSpace(subject) == "" {
		return Decision{}, ErrInvalidSubject
	}
	if limit <= 0 {
		return Decision{}, ErrInvalidLimit
	}
	if window <= 0 {
		return Decision{}, ErrInvalidWindow
	}
	key := storageKey(subject)
	for attempt := 0; attempt < maxCASAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return Decision{}, err
		}
		now := r.clock.Now()
		currentData, err := r.store.Get(ctx, key)
		var current windowRecord
		switch {
		case errors.Is(err, storage.ErrNotFound):
			current = windowRecord{SchemaVersion: StorageSchemaVersion, ResetAt: now.Add(window)}
			currentData = nil
		case err != nil:
			return Decision{}, fmt.Errorf("get rate-limit record: %w", err)
		default:
			current, err = decodeWindow(currentData)
			if err != nil {
				return Decision{}, err
			}
			if !now.Before(current.ResetAt) {
				current = windowRecord{SchemaVersion: StorageSchemaVersion, ResetAt: now.Add(window)}
			}
		}

		updated := current
		updated.Revision++
		updated.Count++
		updatedData, err := json.Marshal(updated)
		if err != nil {
			return Decision{}, fmt.Errorf("encode rate-limit record: %w", err)
		}
		if err := r.store.CompareAndSwapBatch(ctx, []storage.CompareAndSwapMutation{{
			Key: key, ExpectedValue: currentData, NewValue: updatedData, TTL: updated.ResetAt.Sub(now),
		}}); err != nil {
			if errors.Is(err, storage.ErrConflict) {
				continue
			}
			return Decision{}, fmt.Errorf("update rate-limit record: %w", err)
		}
		remaining := limit - updated.Count
		if remaining < 0 {
			remaining = 0
		}
		return Decision{
			Allowed:   updated.Count <= limit,
			Limit:     limit,
			Count:     updated.Count,
			Remaining: remaining,
			ResetAt:   updated.ResetAt,
		}, nil
	}
	return Decision{}, ErrConflict
}

func decodeWindow(data []byte) (windowRecord, error) {
	var stored windowRecord
	if err := json.Unmarshal(data, &stored); err != nil {
		return windowRecord{}, fmt.Errorf("%w: decode: %v", ErrCorruptRecord, err)
	}
	if stored.SchemaVersion != StorageSchemaVersion || stored.Revision == 0 || stored.Count < 1 || stored.ResetAt.IsZero() {
		return windowRecord{}, ErrCorruptRecord
	}
	return stored, nil
}

func storageKey(subject string) string {
	return StoragePrefix + base64.RawURLEncoding.EncodeToString([]byte(subject))
}
