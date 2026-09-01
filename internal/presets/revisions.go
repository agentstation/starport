package presets

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/rs/zerolog/log"
)

const (
	// RevisionPrefix is the immutable preset revision namespace. Each
	// save stores its record here beside the head, keyed by name and a
	// zero-padded revision number so a prefix scan lists in order.
	RevisionPrefix = "presets:v1:revision:"
	// defaultHistoryLimit bounds a history read that names no limit.
	defaultHistoryLimit = 100
	// revisionKeyWidth zero-pads revision numbers so lexicographic key
	// order is numeric order.
	revisionKeyWidth = 20
)

// storeRevision writes one immutable revision snapshot beside the head
// the caller just committed. The head write already succeeded, so a
// snapshot failure logs loudly instead of failing the save: the preset
// is current either way, and only a later pin or rollback of this one
// revision would miss.
func (r *repository) storeRevision(ctx context.Context, stored presetRecord, data []byte) {
	if err := r.store.Set(ctx, revisionKey(stored.Preset.Name, stored.Revision), data); err != nil {
		log.Warn().Err(err).
			Str("preset", stored.Preset.Name).
			Uint64("revision", stored.Revision).
			Msg("failed to store preset revision snapshot")
	}
}

// dropRevisions removes every revision snapshot of one deleted preset.
// The head delete already succeeded, so cleanup failures log loudly: a
// snapshot left behind is overwritten if the name is created again.
func (r *repository) dropRevisions(ctx context.Context, name string) {
	keys, err := r.store.ScanWithPrefix(ctx, revisionScope(name), 0)
	if err != nil {
		log.Warn().Err(err).Str("preset", name).Msg("failed to list preset revisions for delete")
		return
	}
	for _, key := range keys {
		if err := r.store.Delete(ctx, key); err != nil {
			log.Warn().Err(err).Str("preset", name).Str("key", key).Msg("failed to delete preset revision")
		}
	}
}

// History answers stored revisions newest-first, up to limit.
func (r *repository) History(ctx context.Context, name string, limit int) ([]Record, error) {
	if strings.TrimSpace(name) == "" {
		return nil, fmt.Errorf("%w: invalid name", ErrInvalidPreset)
	}
	if limit <= 0 {
		limit = defaultHistoryLimit
	}
	keys, err := r.store.ScanWithPrefix(ctx, revisionScope(name), 0)
	if err != nil {
		return nil, fmt.Errorf("list preset revisions: %w", err)
	}
	sort.Sort(sort.Reverse(sort.StringSlice(keys)))
	if len(keys) > limit {
		keys = keys[:limit]
	}
	records := make([]Record, 0, len(keys))
	for _, key := range keys {
		data, err := r.store.Get(ctx, key)
		if err != nil {
			return nil, mapReadError("read preset revision", err)
		}
		stored, err := decodePreset(data)
		if err != nil {
			return nil, err
		}
		records = append(records, recordFromPreset(stored))
	}
	return records, nil
}

// GetRevision answers one pinned revision verbatim.
func (r *repository) GetRevision(ctx context.Context, name string, revision uint64) (Record, error) {
	if strings.TrimSpace(name) == "" {
		return Record{}, fmt.Errorf("%w: invalid name", ErrInvalidPreset)
	}
	if revision == 0 {
		return Record{}, fmt.Errorf("%w: revision 0 names no revision", ErrInvalidPreset)
	}
	data, err := r.store.Get(ctx, revisionKey(name, revision))
	if err != nil {
		return Record{}, mapReadError("get preset revision", err)
	}
	stored, err := decodePreset(data)
	if err != nil {
		return Record{}, err
	}
	if stored.Preset.Name != name || stored.Revision != revision {
		return Record{}, fmt.Errorf("%w: revision does not match key", ErrCorruptRecord)
	}
	return recordFromPreset(stored), nil
}

// Rollback saves a new head revision that copies an old one. The head
// keeps its name and creation time; the copied revision supplies the
// description and configuration. The expected revision names the head
// the caller read, so a concurrent save conflicts instead of being
// silently rolled over.
func (r *repository) Rollback(ctx context.Context, name string, toRevision, expectedRevision uint64) (Record, error) {
	target, err := r.GetRevision(ctx, name, toRevision)
	if err != nil {
		return Record{}, err
	}
	headData, err := r.store.Get(ctx, storageKey(name))
	if err != nil {
		return Record{}, mapReadError("get preset for rollback", err)
	}
	head, err := decodePreset(headData)
	if err != nil {
		return Record{}, err
	}
	if head.Revision != expectedRevision {
		return Record{}, ErrConflict
	}
	next := presetRecord{
		SchemaVersion: StorageSchemaVersion,
		Revision:      head.Revision + 1,
		Preset: Preset{
			Name:        name,
			Description: target.Preset.Description,
			Config:      target.Preset.Config.Clone(),
			CreatedAt:   head.Preset.CreatedAt,
			UpdatedAt:   time.Now().UTC(),
		},
	}
	nextData, err := json.Marshal(next)
	if err != nil {
		return Record{}, fmt.Errorf("encode preset rollback: %w", err)
	}
	if err := r.store.CompareAndSwap(ctx, storageKey(name), headData, nextData); err != nil {
		return Record{}, mapConflict("rollback preset", err)
	}
	r.storeRevision(ctx, next, nextData)
	return recordFromPreset(next), nil
}

// revisionScope is the key prefix that holds one preset's revisions.
func revisionScope(name string) string {
	return RevisionPrefix + base64.RawURLEncoding.EncodeToString([]byte(name)) + ":"
}

func revisionKey(name string, revision uint64) string {
	return fmt.Sprintf("%s%0*d", revisionScope(name), revisionKeyWidth, revision)
}
