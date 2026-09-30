package recovery

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/agentstation/starport/internal/storage"
)

// Base64 represents all key and value bytes. Per-mutation metadata fits within 1 KiB.
const catalogExpiringRetirement = "expiring-retirement"

const catalogAssetMaxBytes = (storage.ImportReplayMaxBytes*4+2)/3 + 128*1024

type catalogMutationAsset struct {
	Key             []byte `json:"key"`
	ExpectedPresent bool   `json:"expected_present"`
	Expected        []byte `json:"expected"`
	NewPresent      bool   `json:"new_present"`
	New             []byte `json:"new"`
}
type catalogStageAsset struct {
	Version        int                    `json:"version"`
	RunSHA256      string                 `json:"run_sha256"`
	TopologySHA256 string                 `json:"topology_sha256"`
	Index          int                    `json:"index"`
	PreviousSHA256 string                 `json:"previous_sha256"`
	Before         HistoryReplayPositions `json:"before"`
	Mutations      []catalogMutationAsset `json:"mutations"`
	Kind           string                 `json:"kind,omitempty"`
	Expiring       []catalogExpiringAsset `json:"expiring,omitempty"`
}

type catalogExpiringAsset struct {
	Key             []byte `json:"key"`
	Value           []byte `json:"value"`
	ValuePresent    bool   `json:"value_present"`
	ExpiresAtMillis int64  `json:"expires_at_millis"`
}

func catalogAssetName(index int) string { return fmt.Sprintf("%06d.json", index) }
func (a catalogStageAsset) mutations() ([]storage.CompareAndSwapMutation, error) {
	if a.Kind != "" || len(a.Expiring) != 0 || len(a.Mutations) == 0 || len(a.Mutations) > 128 {
		return nil, ErrConflict
	}
	out := make([]storage.CompareAndSwapMutation, len(a.Mutations))
	for index, m := range a.Mutations {
		key := string(m.Key)
		if !catalogKey(key) {
			return nil, ErrConflict
		}
		if !m.ExpectedPresent && len(m.Expected) != 0 || !m.NewPresent && len(m.New) != 0 {
			return nil, ErrConflict
		}
		out[index].Key = key
		if m.ExpectedPresent {
			out[index].ExpectedValue = bytes.Clone(m.Expected)
			if out[index].ExpectedValue == nil {
				out[index].ExpectedValue = []byte{}
			}
		}
		if m.NewPresent {
			out[index].NewValue = bytes.Clone(m.New)
			if out[index].NewValue == nil {
				out[index].NewValue = []byte{}
			}
		}
	}
	return out, nil
}
func (a catalogStageAsset) expiringRecords() ([]storage.TransferRecord, error) {
	if a.Kind != catalogExpiringRetirement || len(a.Mutations) != 0 || len(a.Expiring) == 0 || len(a.Expiring) > 128 {
		return nil, ErrConflict
	}
	out := make([]storage.TransferRecord, len(a.Expiring))
	for index, original := range a.Expiring {
		record := storage.TransferRecord{Key: string(original.Key), ExpiresAtMillis: original.ExpiresAtMillis}
		if original.ValuePresent {
			record.Value = original.Value
			if record.Value == nil {
				record.Value = []byte{}
			}
		} else if len(original.Value) != 0 {
			return nil, ErrConflict
		}
		out[index] = record
	}
	return copyCatalogExpiringRecords(out)
}

func copyCatalogExpiringRecords(records []storage.TransferRecord) ([]storage.TransferRecord, error) {
	if len(records) == 0 || len(records) > 128 {
		return nil, ErrConflict
	}
	total := 0
	for _, record := range records {
		if !strings.HasPrefix(record.Key, "catalog:fleet:") || !strings.HasSuffix(record.Key, ":lease") && !strings.HasSuffix(record.Key, ":maintenance") || record.ExpiresAtMillis <= 0 || record.Validate() != nil {
			return nil, ErrConflict
		}
		total += len(record.Key) + len(record.Value) + 8
		if total > storage.ImportReplayMaxBytes {
			return nil, storage.ErrValueTooLarge
		}
	}
	out := make([]storage.TransferRecord, len(records))
	for index, record := range records {
		record.Value = bytes.Clone(record.Value)
		out[index] = record
	}
	return out, nil
}

func (a catalogStageAsset) nativeReceipt(r *historyRunner, evidence string) (string, error) {
	if a.Kind == catalogExpiringRetirement {
		records, err := a.expiringRecords()
		if err != nil {
			return "", err
		}
		return storage.ImportExpiringReconciliationSHA256(r.identity.KVClaim, a.Before.KV.Sequence+1, a.Before.KV.ReceiptSHA256, evidence, records)
	}
	mutations, err := a.mutations()
	if err != nil {
		return "", err
	}
	return storage.ImportReconciliationSHA256(r.identity.KVClaim, a.Before.KV.Sequence+1, a.Before.KV.ReceiptSHA256, evidence, mutations)
}

func newExpiringCatalogAsset(r *historyRunner, index int, before HistoryReplayPositions, previous string, records []storage.TransferRecord) (catalogStageAsset, []byte, error) {
	asset := catalogStageAsset{Version: 1, RunSHA256: historySHA256(r.runBytes), TopologySHA256: r.run.CatalogPreparation.TopologySHA256, Index: index, Before: before, PreviousSHA256: previous, Kind: catalogExpiringRetirement}
	checked, err := copyCatalogExpiringRecords(records)
	if err != nil {
		return asset, nil, err
	}
	for _, record := range checked {
		asset.Expiring = append(asset.Expiring, catalogExpiringAsset{[]byte(record.Key), record.Value, record.Value != nil, record.ExpiresAtMillis})
	}
	if _, err := asset.nativeReceipt(r, strings.Repeat("a", 64)); err != nil {
		return asset, nil, err
	}
	encoded, err := json.Marshal(asset, json.Deterministic(true))
	if err == nil && len(encoded) > catalogAssetMaxBytes {
		err = storage.ErrValueTooLarge
	}
	return asset, encoded, err
}

func newCatalogAsset(r *historyRunner, index int, before HistoryReplayPositions, previous string, mutations []storage.CompareAndSwapMutation) (catalogStageAsset, []byte, error) {
	asset := catalogStageAsset{Version: 1, RunSHA256: historySHA256(r.runBytes), TopologySHA256: r.run.CatalogPreparation.TopologySHA256, Index: index, Before: before, PreviousSHA256: previous}
	for _, m := range mutations {
		if m.TTL != 0 {
			return asset, nil, storage.ErrInvalidMutation
		}
		asset.Mutations = append(asset.Mutations, catalogMutationAsset{Key: []byte(m.Key), ExpectedPresent: m.ExpectedValue != nil, Expected: bytes.Clone(m.ExpectedValue), NewPresent: m.NewValue != nil, New: bytes.Clone(m.NewValue)})
	}
	checked, err := asset.mutations()
	if err != nil {
		return asset, nil, err
	}
	// The storage owner checks raw byte limits, presence, duplicate keys and reserved controls.
	if _, err = storage.ImportReconciliationSHA256(r.identity.KVClaim, before.KV.Sequence+1, before.KV.ReceiptSHA256, strings.Repeat("a", 64), checked); err != nil {
		return asset, nil, err
	}
	encoded, err := json.Marshal(asset, json.Deterministic(true))
	if err == nil && len(encoded) > catalogAssetMaxBytes {
		err = storage.ErrValueTooLarge
	}
	return asset, encoded, err
}
func (r *historyRunner) readCatalogAsset(ctx context.Context, index int) (catalogStageAsset, []byte, error) {
	var asset catalogStageAsset
	if err := ctx.Err(); err != nil {
		return asset, nil, err
	}
	directory, err := r.directory.ExistingChild("catalog-assets")
	if errors.Is(err, os.ErrNotExist) {
		return asset, nil, nil
	}
	if err != nil {
		return asset, nil, err
	}
	data, err := directory.ReadFile(catalogAssetName(index), catalogAssetMaxBytes)
	if errors.Is(err, os.ErrNotExist) {
		return asset, nil, nil
	}
	if err != nil {
		return asset, nil, err
	}
	if len(data) == 0 || json.Unmarshal(data, &asset, json.RejectUnknownMembers(true)) != nil {
		return asset, nil, ErrConflict
	}
	canonical, err := json.Marshal(asset, json.Deterministic(true))
	if err != nil || !bytes.Equal(canonical, data) {
		return asset, nil, ErrConflict
	}
	return asset, data, nil
}
func (r *historyRunner) publishCatalogAsset(ctx context.Context, index int, data []byte) error {
	directory, err := r.directory.Child("catalog-assets")
	if err != nil {
		return err
	}
	return directory.CompareAndPublish(ctx, catalogAssetName(index), nil, data)
}

func catalogKey(key string) bool {
	return strings.HasPrefix(key, "catalog:") || strings.HasPrefix(key, "catalog_generation:") || strings.HasPrefix(key, "catalog_candidate_generation:")
}
