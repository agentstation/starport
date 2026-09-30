package recovery

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strconv"
	"strings"

	"github.com/agentstation/starmap/pkg/productfiles"
	"github.com/agentstation/starport/internal/storage"
)

type catalogPreparedStage struct {
	Version        int                    `json:"version"`
	RunSHA256      string                 `json:"run_sha256"`
	TopologySHA256 string                 `json:"topology_sha256"`
	Index          int                    `json:"index"`
	PreviousSHA256 string                 `json:"previous_sha256"`
	Before         HistoryReplayPositions `json:"before"`
	AssetSHA256    string                 `json:"asset_sha256"`
	AssetSize      int                    `json:"asset_size"`
}
type catalogAppliedStage struct {
	Version             int                    `json:"version"`
	Index               int                    `json:"index"`
	PreparedSHA256      string                 `json:"prepared_sha256"`
	NativeReceiptSHA256 string                 `json:"native_receipt_sha256"`
	After               HistoryReplayPositions `json:"after"`
}
type catalogCompleteRecord struct {
	Version         int                    `json:"version"`
	RunSHA256       string                 `json:"run_sha256"`
	TopologySHA256  string                 `json:"topology_sha256"`
	StageCount      int                    `json:"stage_count"`
	PrefixSHA256    string                 `json:"prefix_sha256"`
	PrefixPositions HistoryReplayPositions `json:"prefix_positions"`
	PreviousSHA256  string                 `json:"previous_sha256"`
	FinalPositions  HistoryReplayPositions `json:"final_positions"`
}
type catalogRecordIdentity struct {
	asset    string
	prepared string
	applied  string
}

type catalogJournalState struct {
	count           int
	previous        string
	positions       HistoryReplayPositions
	prefixPrevious  string
	prefixPositions HistoryReplayPositions
	pending         *catalogPreparedStage
	orphanAsset     bool
	assetBytes      int64
	complete        string
	records         []catalogRecordIdentity
}

func catalogStageName(index int, phase string) string {
	return fmt.Sprintf("catalog-%06d.%s.json", index, phase)
}
func catalogPrepared(r *historyRunner, asset catalogStageAsset, data []byte) catalogPreparedStage {
	return catalogPreparedStage{Version: 1, RunSHA256: historySHA256(r.runBytes), TopologySHA256: r.run.CatalogPreparation.TopologySHA256, Index: asset.Index, PreviousSHA256: asset.PreviousSHA256, Before: asset.Before, AssetSHA256: historySHA256(data), AssetSize: len(data)}
}
func (r *historyRunner) validCatalogAsset(asset catalogStageAsset, state catalogJournalState, index int) bool {
	return asset.Version == 1 && asset.RunSHA256 == historySHA256(r.runBytes) && asset.TopologySHA256 == r.run.CatalogPreparation.TopologySHA256 && asset.Index == index && asset.Before == state.positions && asset.PreviousSHA256 == state.previous
}

// scanCatalogJournal uses retained bytes only. It never calls a native target or repairs evidence.
func (r *historyRunner) scanCatalogJournal(ctx context.Context, prefix historyJournalState) (catalogJournalState, error) {
	state := catalogJournalState{previous: prefix.previous, positions: prefix.positions, prefixPrevious: prefix.previous, prefixPositions: prefix.positions}
	plan := r.run.CatalogPreparation
	if plan == nil {
		return state, ErrConflict
	}
	for index := 0; index <= plan.StageCount; index++ {
		var prepared catalogPreparedStage
		body, err := r.readJournalRecord(ctx, catalogStageName(index, historyPreparedPhase), &prepared)
		if err != nil {
			return state, err
		}
		var applied catalogAppliedStage
		after, err := r.readJournalRecord(ctx, catalogStageName(index, historyAppliedPhase), &applied)
		if err != nil {
			return state, err
		}
		asset, data, err := r.readCatalogAsset(ctx, index)
		if err != nil {
			return state, err
		}
		if data == nil {
			if body != nil || after != nil {
				return state, ErrConflict
			}
			break
		}
		if !r.validCatalogAsset(asset, state, index) {
			return state, ErrConflict
		}
		expected := catalogPrepared(r, asset, data)
		encoded, err := json.Marshal(expected, json.Deterministic(true))
		if err != nil {
			return state, err
		}
		native, err := asset.nativeReceipt(r, historySHA256(encoded))
		if err != nil {
			return state, err
		}
		if int64(len(data)) > historyAssetAggregateMaxBytes-state.assetBytes {
			return state, storage.ErrValueTooLarge
		}
		state.assetBytes += int64(len(data))
		if body == nil {
			if after != nil {
				return state, ErrConflict
			}
			state.pending = &expected
			state.orphanAsset = true
			break
		}
		if prepared != expected {
			return state, ErrConflict
		}
		if after == nil {
			state.pending = &prepared
			break
		}
		next := state.positions
		next.KV.Sequence++
		next.KV.ReceiptSHA256 = native
		if applied != (catalogAppliedStage{Version: 1, Index: index, PreparedSHA256: historySHA256(body), NativeReceiptSHA256: native, After: next}) {
			return state, ErrConflict
		}
		state.records = append(state.records, catalogRecordIdentity{asset: historySHA256(data), prepared: historySHA256(body), applied: historySHA256(after)})
		state.count++
		state.previous = historySHA256(after)
		state.positions = next
	}
	var completed catalogCompleteRecord
	body, err := r.readJournalRecord(ctx, "catalog.complete.json", &completed)
	if err != nil {
		return state, err
	}
	if body != nil {
		expected := r.catalogCompletion(state)
		if state.pending != nil || state.count != plan.StageCount+1 || completed != expected {
			return state, ErrConflict
		}
		state.complete = historySHA256(body)
	}
	return state, r.checkCatalogNames(ctx, state)
}
func (r *historyRunner) catalogCompletion(state catalogJournalState) catalogCompleteRecord {
	return catalogCompleteRecord{Version: 1, RunSHA256: historySHA256(r.runBytes), TopologySHA256: r.run.CatalogPreparation.TopologySHA256, StageCount: r.run.CatalogPreparation.StageCount, PrefixSHA256: state.prefixPrevious, PrefixPositions: state.prefixPositions, PreviousSHA256: state.previous, FinalPositions: state.positions}
}
func (r *historyRunner) checkCatalogNames(ctx context.Context, state catalogJournalState) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	root, err := r.directory.Open()
	if err != nil {
		return err
	}
	entries, err := fs.ReadDir(root.FS(), ".")
	err = errors.Join(err, root.Close())
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), "catalog") {
			continue
		}
		if r.run.CatalogPreparation == nil || !catalogJournalName(entry, state) {
			return ErrConflict
		}
	}
	directory, err := r.directory.ExistingChild("catalog-assets")
	if errors.Is(err, os.ErrNotExist) {
		if state.count > 0 || state.pending != nil {
			return ErrConflict
		}
		return nil
	}
	if err != nil {
		return err
	}
	if err := directory.CheckNoPendingPublications(ctx); err != nil {
		return err
	}
	assets, err := directory.Open()
	if err != nil {
		return err
	}
	entries, err = fs.ReadDir(assets.FS(), ".")
	err = errors.Join(err, assets.Close())
	if err != nil {
		return err
	}
	retained := entries[:0]
	for _, entry := range entries {
		if entry.Name() == productfiles.PublicationDirectoryName && entry.IsDir() {
			continue
		}
		retained = append(retained, entry)
	}
	entries = retained
	expected := state.count
	if state.pending != nil {
		expected++
	}
	if len(entries) != expected {
		return ErrConflict
	}
	for _, entry := range entries {
		id, found := strings.CutSuffix(entry.Name(), ".json")
		if !found {
			return ErrConflict
		}
		index, err := strconv.Atoi(id)
		if err != nil || index < 0 || index >= expected || entry.Name() != catalogAssetName(index) || entry.IsDir() {
			return ErrConflict
		}
	}
	return nil
}

func catalogJournalName(entry fs.DirEntry, state catalogJournalState) bool {
	name := entry.Name()
	if name == "catalog-assets" {
		return entry.IsDir()
	}
	if entry.IsDir() {
		return false
	}
	if name == "catalog.complete.json" {
		return state.complete != ""
	}
	for _, phase := range []string{historyPreparedPhase, historyAppliedPhase} {
		id, found := strings.CutSuffix(strings.TrimPrefix(name, "catalog-"), "."+phase+".json")
		if !found {
			continue
		}
		index, err := strconv.Atoi(id)
		if err != nil || index < 0 || name != catalogStageName(index, phase) {
			return false
		}
		if index < state.count {
			return true
		}
		return index == state.count && state.pending != nil && !state.orphanAsset && phase == historyPreparedPhase
	}
	return false
}
