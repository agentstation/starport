package recovery

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/agentstation/starport/internal/storage"
)

const typedHistoryPayloadMaxBytes = 8 << 20
const typedHistoryMaxMutations = 128

type preparedHistoryKV struct {
	digest    string
	mutations []storage.CompareAndSwapMutation
}

func (preparedHistoryKV) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("<private prepared recovery mutations>"))
}

type historyKVProposal struct {
	source  *KVSnapshotView
	changes map[string]storage.CompareAndSwapMutation
	size    int
}

func newHistoryKVProposal(source *KVSnapshotView) *historyKVProposal {
	return &historyKVProposal{source: source, changes: map[string]storage.CompareAndSwapMutation{}}
}
func (p *historyKVProposal) add(changes []storage.CompareAndSwapMutation) error {
	for _, change := range changes {
		if _, exists := p.changes[change.Key]; exists {
			return storage.ErrInvalidMutation
		}
		if change.Key == "" || len(change.Key) > storage.TransferMaxKeyBytes || change.TTL != 0 || strings.HasPrefix(change.Key, "storage:") || strings.HasPrefix(change.Key, "!badger!") {
			return storage.ErrInvalidMutation
		}
		size := len(change.Key) + len(change.ExpectedValue) + len(change.NewValue)
		if size > storage.ImportReplayMaxBytes-p.size || len(p.changes) >= typedHistoryMaxMutations {
			return storage.ErrValueTooLarge
		}
		p.size += size
		change.ExpectedValue = bytes.Clone(change.ExpectedValue)
		change.NewValue = bytes.Clone(change.NewValue)
		p.changes[change.Key] = change
	}
	return nil
}
func (p *historyKVProposal) ReadCaptured(ctx context.Context, key string, maximum int) (storage.TransferRecord, error) {
	if err := ctx.Err(); err != nil {
		return storage.TransferRecord{}, err
	}
	if change, ok := p.changes[key]; ok {
		if change.NewValue == nil {
			return storage.TransferRecord{}, storage.ErrNotFound
		}
		if len(change.NewValue) > maximum {
			return storage.TransferRecord{}, storage.ErrValueTooLarge
		}
		return storage.TransferRecord{Key: key, Value: bytes.Clone(change.NewValue)}, nil
	}
	return p.source.ReadCaptured(ctx, key, maximum)
}
func (p *historyKVProposal) GetBounded(ctx context.Context, key string, maximum int) ([]byte, error) {
	record, err := p.ReadCaptured(ctx, key, maximum)
	return record.Value, err
}
func (p *historyKVProposal) finish(digest string) (preparedHistoryKV, error) {
	if len(p.changes) == 0 {
		return preparedHistoryKV{}, storage.ErrInvalidMutation
	}
	result := preparedHistoryKV{digest: digest}
	for _, key := range slices.Sorted(maps.Keys(p.changes)) {
		result.mutations = append(result.mutations, p.changes[key])
	}
	return result, nil
}
func decodeHistoryPayload(kind string, data []byte, destination any) (string, error) {
	if len(data) == 0 || len(data) > typedHistoryPayloadMaxBytes {
		return "", ErrConflict
	}
	if err := json.Unmarshal(data, destination, json.RejectUnknownMembers(true)); err != nil {
		return "", ErrConflict
	}
	canonical, err := json.Marshal(destination, json.Deterministic(true))
	if err != nil {
		return "", ErrConflict
	}
	sum := sha256.Sum256(append(append([]byte(kind), '\n'), canonical...))
	return hex.EncodeToString(sum[:]), nil
}
func explicitHistoryMembers(data []byte, names ...string) bool {
	var object map[string]jsontext.Value
	if json.Unmarshal(data, &object) != nil {
		return false
	}
	for _, name := range names {
		if _, ok := object[name]; !ok {
			return false
		}
	}
	return true
}
