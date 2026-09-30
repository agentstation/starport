package recovery

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json/v2"
	"errors"
)

func (r *historyRunner) pendingCatalogApplied(ctx context.Context, state catalogJournalState) (catalogAppliedStage, []byte, error) {
	if state.pending == nil || state.orphanAsset {
		return catalogAppliedStage{}, nil, ErrConflict
	}
	asset, data, err := r.readCatalogAsset(ctx, state.count)
	if err != nil || data == nil {
		return catalogAppliedStage{}, nil, errors.Join(ErrConflict, err)
	}
	prepared := catalogPrepared(r, asset, data)
	if prepared != *state.pending {
		return catalogAppliedStage{}, nil, ErrConflict
	}
	body, err := json.Marshal(prepared, json.Deterministic(true))
	if err != nil {
		return catalogAppliedStage{}, nil, err
	}
	var retained catalogPreparedStage
	original, err := r.readJournalRecord(ctx, catalogStageName(state.count, historyPreparedPhase), &retained)
	if err != nil || original == nil || retained != prepared || !bytes.Equal(body, original) {
		return catalogAppliedStage{}, nil, errors.Join(ErrConflict, err)
	}
	receipt, err := asset.nativeReceipt(r, historySHA256(body))
	if err != nil {
		return catalogAppliedStage{}, nil, err
	}
	next := prepared.Before
	next.KV.Sequence++
	next.KV.ReceiptSHA256 = receipt
	applied := catalogAppliedStage{Version: 1, Index: state.count, PreparedSHA256: historySHA256(body), NativeReceiptSHA256: receipt, After: next}
	encoded, err := json.Marshal(applied, json.Deterministic(true))
	return applied, encoded, err
}
func (r *historyRunner) checkCatalogPendingPosition(ctx context.Context, state catalogJournalState) error {
	err := r.targets.KV.CheckImportPosition(ctx, r.identity.KVClaim, state.positions.KV)
	if err == nil || state.pending == nil || state.orphanAsset {
		return err
	}
	applied, _, inspectErr := r.pendingCatalogApplied(ctx, state)
	if inspectErr != nil {
		return inspectErr
	}
	return r.targets.KV.CheckImportPosition(ctx, r.identity.KVClaim, applied.After.KV)
}

// A matching native cursor proves the original pending commit. Local publication does not replay it.
func (l *CatalogPreparationLane) recoverPending(ctx context.Context) (bool, error) {
	applied, body, err := l.runner.pendingCatalogApplied(ctx, l.state)
	if err != nil {
		return false, err
	}
	committed := false
	err = l.guard(ctx, func(ctx context.Context, _ *sql.Conn) error {
		nextErr := l.checkPosition(ctx, applied.After.KV)
		if nextErr == nil {
			committed = true
			return nil
		}
		return l.checkPosition(ctx, l.state.positions.KV)
	})
	if err != nil {
		return false, err
	}
	if !committed {
		return false, nil
	}
	if err := l.runner.directory.CompareAndPublish(ctx, catalogStageName(applied.Index, "applied"), nil, body); err != nil {
		return false, err
	}
	l.state.records = append(l.state.records, catalogRecordIdentity{asset: l.state.pending.AssetSHA256, prepared: applied.PreparedSHA256, applied: historySHA256(body)})
	l.state.count++
	l.state.previous = historySHA256(body)
	l.state.positions = applied.After
	l.state.pending = nil
	if l.state.count == l.runner.run.CatalogPreparation.StageCount+1 {
		if err := l.complete(ctx); err != nil {
			return false, err
		}
	}
	return true, nil
}
