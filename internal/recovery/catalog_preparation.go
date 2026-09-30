package recovery

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json/v2"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/agentstation/starport/internal/storage"
)

// CatalogPreparationPlan binds the actual compiled topology and its immutable stage count.
// StageCount excludes the required final selection stage.
type CatalogPreparationPlan struct {
	TopologySHA256 string `json:"topology_sha256"`
	StageCount     int    `json:"stage_count"`
}

// CatalogPreparationLane journals bounded catalog transitions under original closed native claims.
// Its methods structurally satisfy the catalog owner's target contract without importing catalog.
type CatalogPreparationLane struct{ *catalogPreparationState }

type catalogPreparationState struct {
	mu     sync.Mutex
	runner *historyRunner
	state  catalogJournalState
}

// Format omits private mutations, preimages, claims and retained assets.
func (l CatalogPreparationLane) Format(state fmt.State, _ rune) {
	_, _ = fmt.Fprint(state, "<private catalog preparation lane>")
}

func (l *CatalogPreparationLane) initialized() bool {
	return l != nil && l.catalogPreparationState != nil && l.runner != nil
}

// OpenCatalogPreparation reopens the original checked prefix and immutable catalog journal.
// It never recaptures changed targets to create a replacement stage.
func (p *HistoryPrefix) OpenCatalogPreparation(ctx context.Context) (*CatalogPreparationLane, error) {
	if ctx == nil || p == nil || p.runner == nil {
		return nil, ErrConflict
	}
	r := p.runner
	state, err := r.scanJournal(ctx)
	if err != nil {
		return nil, err
	}
	stop, err := catalogPrefixCount(r.accepted.state.history.manifest.Steps)
	if err != nil {
		return nil, err
	}
	if state.count != stop || state.pending != nil || state.catalog == nil {
		return nil, ErrConflict
	}
	lane := &CatalogPreparationLane{catalogPreparationState: &catalogPreparationState{runner: r, state: *state.catalog}}
	if err := lane.guard(ctx, func(ctx context.Context, _ *sql.Conn) error { return r.checkCatalogPendingPosition(ctx, lane.state) }); err != nil {
		return nil, err
	}
	return lane, nil
}
func (l *CatalogPreparationLane) guard(ctx context.Context, callback func(context.Context, *sql.Conn) error) error {
	if ctx == nil || !l.initialized() || callback == nil {
		return ErrConflict
	}
	bounded, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	r := l.runner
	if err := r.accepted.check(bounded, r.witness); err != nil {
		return err
	}
	if err := r.checkRun(bounded); err != nil {
		return err
	}
	if err := r.checkCatalogPrefix(bounded, l.state); err != nil {
		return err
	}
	if err := r.targets.Blobs.CheckImportPosition(bounded, r.identity.ComponentOperation, r.identity.BlobOriginal, l.state.positions.Blobs); err != nil {
		return err
	}
	request := ClosedImportGuardRequest{Snapshot: r.identity.SQLOriginal, Import: r.identity.SQL, Position: l.state.positions.SQL, Boundary: r.accepted.state.boundary}
	return r.witness.GuardClosedImport(bounded, request, func(ctx context.Context, conn *sql.Conn) error {
		if conn == nil {
			return ErrConflict
		}
		return callback(ctx, conn)
	})
}
func (l *CatalogPreparationLane) checkPosition(ctx context.Context, position storage.ImportReplayPosition) error {
	return l.runner.targets.KV.CheckImportPosition(ctx, l.runner.identity.KVClaim, position)
}

// ReadCatalogTopology reads one bounded record at the lane's original exact native cursor.
// The caller must keep external writers fenced throughout the complete preparation procedure.
func (l *CatalogPreparationLane) ReadCatalogTopology(ctx context.Context, key string, limit int) ([]byte, error) {
	if ctx == nil || !l.initialized() || !validCatalogPlan(l.runner.run.CatalogPreparation) {
		return nil, ErrConflict
	}
	if !catalogKey(key) || limit <= 0 || limit > storage.ImportReplayMaxBytes {
		return nil, ErrConflict
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	var value []byte
	err := l.guard(ctx, func(ctx context.Context, _ *sql.Conn) error {
		record, err := l.runner.targets.KV.ReadImportAt(ctx, l.runner.identity.KVClaim, l.state.positions.KV, key, limit)
		if err != nil {
			return err
		}
		if record.ExpiresAtMillis != 0 {
			return ErrConflict
		}
		value = bytes.Clone(record.Value)
		if value == nil {
			value = []byte{}
		}
		return nil
	})
	return value, err
}

// CompletedCatalogTopology proves an exact historical stage under the current original native claim.
// It grants no current catalog permission and never repeats old mutations.
func (l *CatalogPreparationLane) CompletedCatalogTopology(ctx context.Context, topology string, index int) (bool, error) {
	if ctx == nil || !l.initialized() || !validCatalogPlan(l.runner.run.CatalogPreparation) {
		return false, ErrConflict
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if topology != l.runner.run.CatalogPreparation.TopologySHA256 || index < 0 || index > l.runner.run.CatalogPreparation.StageCount {
		return false, ErrConflict
	}
	if index >= l.state.count {
		if index == l.state.count && l.state.pending != nil && !l.state.orphanAsset {
			return l.recoverPending(ctx)
		}
		return false, l.guard(ctx, func(ctx context.Context, _ *sql.Conn) error { return l.checkPosition(ctx, l.state.positions.KV) })
	}
	var applied catalogAppliedStage
	var prepared catalogPreparedStage
	body, err := l.runner.readJournalRecord(ctx, catalogStageName(index, "prepared"), &prepared)
	if err != nil || body == nil || index >= len(l.state.records) || historySHA256(body) != l.state.records[index].prepared {
		return false, errors.Join(ErrConflict, err)
	}
	after, err := l.runner.readJournalRecord(ctx, catalogStageName(index, "applied"), &applied)
	if err != nil || after == nil || historySHA256(after) != l.state.records[index].applied {
		return false, errors.Join(ErrConflict, err)
	}
	asset, data, err := l.runner.readCatalogAsset(ctx, index)
	if err != nil || data == nil || historySHA256(data) != l.state.records[index].asset {
		return false, errors.Join(ErrConflict, err)
	}
	expected := catalogPrepared(l.runner, asset, data)
	if expected != prepared {
		return false, ErrConflict
	}
	mutations, err := asset.mutations()
	if err != nil {
		return false, err
	}
	native, err := storage.ImportReconciliationSHA256(l.runner.identity.KVClaim, prepared.Before.KV.Sequence+1, prepared.Before.KV.ReceiptSHA256, historySHA256(body), mutations)
	if err != nil {
		return false, err
	}
	next := prepared.Before
	next.KV.Sequence++
	next.KV.ReceiptSHA256 = native
	if applied != (catalogAppliedStage{Version: 1, Index: index, PreparedSHA256: historySHA256(body), NativeReceiptSHA256: native, After: next}) {
		return false, ErrConflict
	}
	if err := l.guard(ctx, func(ctx context.Context, _ *sql.Conn) error { return l.checkPosition(ctx, l.state.positions.KV) }); err != nil {
		return false, err
	}
	if l.state.count == l.runner.run.CatalogPreparation.StageCount+1 && l.state.complete == "" {
		if err := l.complete(ctx); err != nil {
			return false, err
		}
	}
	return true, nil
}

// ApplyCatalogTopology retains exact original mutation assets before the bounded native transaction.
// An uncertain native reply requires the same asset and receipt. It never substitutes new preimages.
func (l *CatalogPreparationLane) ApplyCatalogTopology(ctx context.Context, topology string, index int, mutations []storage.CompareAndSwapMutation) error {
	return l.applyCatalogTopology(ctx, topology, index, mutations, nil)
}

// ApplyExpiringCatalogTopology retires exact captured lease controls through the native expiry owner.
// It retains a distinct immutable asset and ordered receipt without renewing any lease or opening admission.
func (l *CatalogPreparationLane) ApplyExpiringCatalogTopology(ctx context.Context, topology string, index int, records []storage.TransferRecord) error {
	return l.applyCatalogTopology(ctx, topology, index, nil, records)
}

func (l *CatalogPreparationLane) applyCatalogTopology(ctx context.Context, topology string, index int, mutations []storage.CompareAndSwapMutation, records []storage.TransferRecord) error {
	if ctx == nil || !l.initialized() || !validCatalogPlan(l.runner.run.CatalogPreparation) {
		return ErrConflict
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	r := l.runner
	if topology != r.run.CatalogPreparation.TopologySHA256 || index < 0 || index > r.run.CatalogPreparation.StageCount || index > l.state.count {
		return ErrConflict
	}
	if index < l.state.count {
		return l.checkCompletedAsset(ctx, index, mutations, records)
	}
	if err := r.checkRun(ctx); err != nil {
		return err
	}
	asset, encoded, err := catalogRequestedAsset(r, index, l.state.positions, l.state.previous, mutations, records)
	if err != nil {
		return err
	}
	original, retained, err := r.readCatalogAsset(ctx, index)
	if err != nil {
		return err
	}
	if retained != nil && !bytes.Equal(encoded, retained) {
		return ErrConflict
	}
	if retained == nil {
		if int64(len(encoded)) > historyAssetAggregateMaxBytes-l.state.assetBytes {
			return storage.ErrValueTooLarge
		}
		if err := l.guard(ctx, func(ctx context.Context, _ *sql.Conn) error { return l.checkPosition(ctx, l.state.positions.KV) }); err != nil {
			return err
		}
		if err := r.publishCatalogAsset(ctx, index, encoded); err != nil {
			return fmt.Errorf("retain catalog stage asset: %w", err)
		}
		l.state.assetBytes += int64(len(encoded))
	} else {
		asset = original
	}
	prepared := catalogPrepared(r, asset, encoded)
	body, err := json.Marshal(prepared, json.Deterministic(true))
	if err != nil {
		return err
	}
	if err := r.directory.CompareAndPublish(ctx, catalogStageName(index, "prepared"), nil, body); err != nil {
		return fmt.Errorf("retain prepared catalog stage: %w", err)
	}
	l.state.pending = &prepared
	l.state.orphanAsset = false
	if err := r.checkCatalogNames(ctx, l.state); err != nil {
		return fmt.Errorf("check catalog stage journal names: %w", err)
	}
	native, err := asset.nativeReceipt(r, historySHA256(body))
	if err != nil {
		return err
	}
	next := prepared.Before
	next.KV.Sequence++
	next.KV.ReceiptSHA256 = native
	err = l.guard(ctx, func(ctx context.Context, _ *sql.Conn) error {
		receipt, err := l.applyNativeAsset(ctx, asset, historySHA256(body))
		if err != nil {
			return err
		}
		if receipt != native {
			return ErrConflict
		}
		return l.checkPosition(ctx, next.KV)
	})
	if err != nil {
		return fmt.Errorf("apply guarded catalog stage: %w", err)
	}
	applied := catalogAppliedStage{Version: 1, Index: index, PreparedSHA256: historySHA256(body), NativeReceiptSHA256: native, After: next}
	after, err := json.Marshal(applied, json.Deterministic(true))
	if err != nil {
		return err
	}
	if err := r.directory.CompareAndPublish(ctx, catalogStageName(index, "applied"), nil, after); err != nil {
		return err
	}
	l.state.records = append(l.state.records, catalogRecordIdentity{asset: historySHA256(encoded), prepared: historySHA256(body), applied: historySHA256(after)})
	l.state.count++
	l.state.previous = historySHA256(after)
	l.state.positions = next
	l.state.pending = nil
	if l.state.count == r.run.CatalogPreparation.StageCount+1 {
		return l.complete(ctx)
	}
	return nil
}
func (l *CatalogPreparationLane) checkCompletedAsset(ctx context.Context, index int, mutations []storage.CompareAndSwapMutation, records []storage.TransferRecord) error {
	asset, data, err := l.runner.readCatalogAsset(ctx, index)
	if err != nil || data == nil {
		return errors.Join(ErrConflict, err)
	}
	_, expected, err := catalogRequestedAsset(l.runner, index, asset.Before, asset.PreviousSHA256, mutations, records)
	if err != nil {
		return err
	}
	if !bytes.Equal(data, expected) || index >= len(l.state.records) || historySHA256(data) != l.state.records[index].asset {
		return ErrConflict
	}
	var prepared catalogPreparedStage
	body, err := l.runner.readJournalRecord(ctx, catalogStageName(index, "prepared"), &prepared)
	if err != nil || body == nil || historySHA256(body) != l.state.records[index].prepared {
		return errors.Join(ErrConflict, err)
	}
	var applied catalogAppliedStage
	after, err := l.runner.readJournalRecord(ctx, catalogStageName(index, "applied"), &applied)
	if err != nil || after == nil || historySHA256(after) != l.state.records[index].applied {
		return errors.Join(ErrConflict, err)
	}
	return l.guard(ctx, func(ctx context.Context, _ *sql.Conn) error { return l.checkPosition(ctx, l.state.positions.KV) })
}
func (l *CatalogPreparationLane) complete(ctx context.Context) error {
	r := l.runner
	prefix := historyJournalState{previous: l.state.prefixPrevious, positions: l.state.prefixPositions}
	state, err := r.scanCatalogJournal(ctx, prefix)
	if err != nil {
		return err
	}
	if state.count != r.run.CatalogPreparation.StageCount+1 || state.pending != nil || state.positions != l.state.positions {
		return ErrConflict
	}
	body, err := json.Marshal(r.catalogCompletion(state), json.Deterministic(true))
	if err != nil {
		return err
	}
	if err := l.guard(ctx, func(ctx context.Context, _ *sql.Conn) error { return l.checkPosition(ctx, state.positions.KV) }); err != nil {
		return err
	}
	if err := r.directory.CompareAndPublish(ctx, "catalog.complete.json", nil, body); err != nil {
		return err
	}
	l.state.complete = historySHA256(body)
	return nil
}

func catalogRequestedAsset(r *historyRunner, index int, before HistoryReplayPositions, previous string, mutations []storage.CompareAndSwapMutation, records []storage.TransferRecord) (catalogStageAsset, []byte, error) {
	if records != nil {
		if mutations != nil {
			return catalogStageAsset{}, nil, ErrConflict
		}
		return newExpiringCatalogAsset(r, index, before, previous, records)
	}
	return newCatalogAsset(r, index, before, previous, mutations)
}

func (l *CatalogPreparationLane) applyNativeAsset(ctx context.Context, asset catalogStageAsset, evidence string) (string, error) {
	r := l.runner
	if asset.Kind == catalogExpiringRetirement {
		records, err := asset.expiringRecords()
		if err != nil {
			return "", err
		}
		target, ok := r.targets.KV.(storage.ImportExpiringRetirer)
		if !ok {
			return "", ErrConflict
		}
		return target.ReconcileExpiringImport(ctx, r.identity.KVClaim, asset.Before.KV.Sequence+1, asset.Before.KV.ReceiptSHA256, evidence, records)
	}
	mutations, err := asset.mutations()
	if err != nil {
		return "", err
	}
	return r.targets.KV.ReconcileImport(ctx, r.identity.KVClaim, asset.Before.KV.Sequence+1, asset.Before.KV.ReceiptSHA256, evidence, mutations)
}
