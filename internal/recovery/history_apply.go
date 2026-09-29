package recovery

import (
	"context"
	"database/sql"
	"encoding/json/v2"
	"errors"
	"path/filepath"

	"github.com/agentstation/starport/internal/blob"
	"github.com/agentstation/starport/internal/sqlstore"
)

func (r *historyRunner) apply(ctx context.Context, p historyPreparedStep) (historyAppliedStep, error) {
	if p.Ordinal < 1 || p.Ordinal > len(r.accepted.state.history.payloads) {
		return historyAppliedStep{}, ErrConflict
	}
	body, err := json.Marshal(p, json.Deterministic(true))
	if err != nil {
		return historyAppliedStep{}, err
	}
	evidence := historySHA256(body)
	images, err := r.directory.ExistingChild("images")
	if err != nil {
		return historyAppliedStep{}, err
	}
	if _, err := images.ExistingChild(p.ImageDirectory); err != nil {
		return historyAppliedStep{}, err
	}
	payload := r.accepted.state.history.payloads[p.Ordinal-1]
	if historySHA256(payload) != p.PayloadSHA256 {
		return historyAppliedStep{}, ErrConflict
	}
	var receipt string
	switch historyNativeOwner(p.Kind) {
	case historyOwnerKV:
		receipt, err = r.applyKV(ctx, p, payload, evidence)
	case historyOwnerSQL:
		receipt, err = r.applySQL(ctx, p, payload, evidence)
	case historyOwnerBlob:
		receipt, err = r.applyBlob(ctx, p, payload, evidence)
	default:
		return historyAppliedStep{}, ErrConflict
	}
	if err != nil {
		return historyAppliedStep{}, err
	}
	if !historyDigest(receipt) {
		return historyAppliedStep{}, ErrConflict
	}
	return historyAppliedStep{Version: 1, Ordinal: p.Ordinal, PreparedSHA256: evidence, NativeReceiptSHA256: receipt, After: advanceHistoryPosition(p.Before, p.Kind, receipt)}, nil
}
func (r *historyRunner) applyKV(ctx context.Context, p historyPreparedStep, payload []byte, evidence string) (receipt string, resultErr error) {
	before, err := OpenKVSnapshot(ctx, KVSnapshotPath(filepath.Join(r.imagePath(p), historyOwnerKV)), r.request.ScratchDirectory, *p.KV)
	if err != nil {
		return "", err
	}
	defer func() { resultErr = errors.Join(resultErr, before.Close()) }()
	assets, err := blob.OpenSnapshot(ctx, filepath.Join(r.imagePath(p), "blobs.tar"), r.request.ScratchDirectory, *p.Blobs)
	if err != nil {
		return "", err
	}
	defer func() { resultErr = errors.Join(resultErr, assets.Close()) }()
	prepared, err := prepareHistoryKV(ctx, p.Kind, payload, before, assets, r.run.ValidatedAt, r.targets.Encryption, r.run.Authority)
	if err != nil {
		return "", err
	}
	return r.targets.KV.ReconcileImport(ctx, r.identity.KVClaim, p.Before.KV.Sequence+1, p.Before.KV.ReceiptSHA256, evidence, prepared.mutations)
}
func (r *historyRunner) applySQL(ctx context.Context, p historyPreparedStep, payload []byte, evidence string) (string, error) {
	image, err := sqlstore.OpenRelationalSnapshot(ctx, filepath.Join(r.imagePath(p), historyOwnerSQL, "starport.db"), *p.SQL, r.request.ScratchDirectory)
	if err != nil {
		return "", err
	}
	if err := image.Close(); err != nil {
		return "", err
	}
	prepared, err := prepareHistorySQL(p.Kind, payload, r.run.Authority)
	if err != nil {
		return "", err
	}
	step := sqlstore.RelationalReplayStep{Sequence: p.Before.SQL.Sequence + 1, PreviousSHA256: p.Before.SQL.ReceiptSHA256, EvidenceSHA256: evidence, TransitionSHA256: prepared.digest}
	return r.witness.db.ReplayRelationalImport(ctx, r.identity.SQLOriginal, r.identity.SQL, step, func(ctx context.Context, conn *sql.Conn) error { return prepared.apply(ctx, r.witness.db, conn) })
}
