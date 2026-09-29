package recovery

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
	"time"

	"github.com/agentstation/starport/internal/blob"
	"github.com/agentstation/starport/internal/sqlstore"
	"github.com/agentstation/starport/internal/storage"
)

const historyJournalRecordMaxBytes = 64 << 10

type historyPreparedStep struct {
	Version        int                      `json:"version"`
	RunSHA256      string                   `json:"run_sha256"`
	Ordinal        int                      `json:"ordinal"`
	Kind           string                   `json:"kind"`
	PayloadSHA256  string                   `json:"payload_sha256"`
	PreviousSHA256 string                   `json:"previous_sha256"`
	Before         HistoryReplayPositions   `json:"before"`
	ImageDirectory string                   `json:"image_directory"`
	KV             *KVSnapshot              `json:"kv,omitempty"`
	SQL            *sqlstore.SQLiteSnapshot `json:"sql,omitempty"`
	Blobs          *blob.Snapshot           `json:"blobs,omitempty"`
}
type historyAppliedStep struct {
	Version             int                    `json:"version"`
	Ordinal             int                    `json:"ordinal"`
	PreparedSHA256      string                 `json:"prepared_sha256"`
	NativeReceiptSHA256 string                 `json:"native_receipt_sha256"`
	After               HistoryReplayPositions `json:"after"`
}
type historyJournalState struct {
	count                 int
	previous              string
	positions             HistoryReplayPositions
	pending               *historyPreparedStep
	kvRotated, sqlRotated bool
}

func (r *historyRunner) openRun(ctx context.Context) error {
	if err := r.directory.RecoverPublications(ctx); err != nil {
		return err
	}
	body, err := r.directory.ReadFile("runner.json", historyJournalRecordMaxBytes)
	if errors.Is(err, os.ErrNotExist) {
		root, err := r.directory.Open()
		if err != nil {
			return err
		}
		entries, listErr := fs.ReadDir(root.FS(), ".")
		closeErr := root.Close()
		if err := errors.Join(listErr, closeErr); err != nil {
			return err
		}
		for _, entry := range entries {
			if strings.HasPrefix(entry.Name(), "step-") || entry.Name() == "images" {
				return ErrConflict
			}
		}
		if err := r.checkNativePositions(ctx, HistoryReplayPositions{}, ""); err != nil {
			return err
		}
		r.run = historyRunRecord{Version: 1, AcceptanceSHA256: r.accepted.Digest(), HistorySHA256: r.accepted.state.history.digest, TargetSHA256: r.request.TargetSHA256, Authority: historyAcceptedAuthority(r.accepted), ValidatedAt: time.Now().UTC()}
		body, err = json.Marshal(r.run, json.Deterministic(true))
		if err != nil {
			return err
		}
		if err := r.directory.CompareAndPublish(ctx, "runner.json", nil, body); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	var record historyRunRecord
	if json.Unmarshal(body, &record, json.RejectUnknownMembers(true)) != nil || record.Version != 1 || record.AcceptanceSHA256 != r.accepted.Digest() || record.HistorySHA256 != r.accepted.state.history.digest || record.TargetSHA256 != r.request.TargetSHA256 || record.Authority != historyAcceptedAuthority(r.accepted) || record.ValidatedAt.IsZero() {
		return ErrConflict
	}
	r.run = record
	r.runBytes = bytes.Clone(body)
	return nil
}
func (r *historyRunner) checkRun(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	body, err := r.directory.ReadFile("runner.json", historyJournalRecordMaxBytes)
	if err != nil || !bytes.Equal(body, r.runBytes) {
		return errors.Join(ErrConflict, err)
	}
	return nil
}
func historyStepName(ordinal int, phase string) string {
	return fmt.Sprintf("step-%06d.%s.json", ordinal, phase)
}
func (r *historyRunner) readJournalRecord(ctx context.Context, name string, value any) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	body, err := r.directory.ReadFile(name, historyJournalRecordMaxBytes)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if len(body) == 0 || json.Unmarshal(body, value, json.RejectUnknownMembers(true)) != nil {
		return nil, ErrConflict
	}
	canonical, err := json.Marshal(value, json.Deterministic(true))
	if err != nil || !bytes.Equal(body, canonical) {
		return nil, ErrConflict
	}
	return body, nil
}
func (r *historyRunner) scanJournal(ctx context.Context) (historyJournalState, error) {
	state := historyJournalState{previous: historySHA256(r.runBytes)}
	if err := r.checkRun(ctx); err != nil {
		return state, err
	}
	missing := false
	for _, step := range r.accepted.state.history.manifest.Steps {
		var prepared historyPreparedStep
		before, err := r.readJournalRecord(ctx, historyStepName(step.Ordinal, "prepared"), &prepared)
		if err != nil {
			return state, err
		}
		var applied historyAppliedStep
		after, err := r.readJournalRecord(ctx, historyStepName(step.Ordinal, "applied"), &applied)
		if err != nil {
			return state, err
		}
		if before == nil {
			if after != nil {
				return state, ErrConflict
			}
			missing = true
			continue
		}
		if missing || state.pending != nil || !r.validPrepared(prepared, step, state) {
			return state, ErrConflict
		}
		if after == nil {
			state.pending = &prepared
			continue
		}
		if applied.Version != 1 || applied.Ordinal != step.Ordinal || applied.PreparedSHA256 != historySHA256(before) || !historyDigest(applied.NativeReceiptSHA256) || applied.After != advanceHistoryPosition(prepared.Before, step.Kind, applied.NativeReceiptSHA256) {
			return state, ErrConflict
		}
		state.count++
		state.previous = historySHA256(after)
		state.positions = applied.After
		state.kvRotated = state.kvRotated || step.Kind == historyKVAuthorityFinal
		state.sqlRotated = state.sqlRotated || step.Kind == historySQLAuthorityFinal
	}
	return state, r.checkUnexpectedJournalFiles(ctx)
}
func (r *historyRunner) validPrepared(p historyPreparedStep, step historyStep, state historyJournalState) bool {
	if p.Version != 1 || p.RunSHA256 != historySHA256(r.runBytes) || p.Ordinal != step.Ordinal || p.Kind != step.Kind || p.PayloadSHA256 != step.SHA256 || p.PreviousSHA256 != state.previous || p.Before != state.positions || !validHistoryImageName(p.ImageDirectory, step.Ordinal) {
		return false
	}
	switch historyNativeOwner(step.Kind) {
	case historyOwnerKV:
		return p.KV != nil && p.KV.validate() == nil && p.Blobs != nil && p.SQL == nil
	case historyOwnerSQL:
		return p.SQL != nil && p.SQL.Size > 0 && historyDigest(p.SQL.SHA256) && p.KV == nil && p.Blobs == nil
	case historyOwnerBlob:
		return p.Blobs != nil && p.KV == nil && p.SQL == nil
	}
	return false
}
func validHistoryImageName(name string, ordinal int) bool {
	prefix := fmt.Sprintf("%06d-", ordinal)
	if !strings.HasPrefix(name, prefix) || len(name) != len(prefix)+26 {
		return false
	}
	for _, ch := range strings.TrimPrefix(name, prefix) {
		if ch < 'A' || ch > 'Z' {
			if ch < '2' || ch > '7' {
				return false
			}
		}
	}
	return true
}
func (r *historyRunner) checkUnexpectedJournalFiles(ctx context.Context) error {
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
	allowed := map[string]bool{}
	for _, step := range r.accepted.state.history.manifest.Steps {
		allowed[historyStepName(step.Ordinal, "prepared")] = true
		allowed[historyStepName(step.Ordinal, "applied")] = true
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "step-") && !allowed[entry.Name()] {
			return ErrConflict
		}
	}
	return nil
}
func (r *historyRunner) publishApplied(ctx context.Context, applied historyAppliedStep) error {
	body, err := json.Marshal(applied, json.Deterministic(true))
	if err != nil {
		return err
	}
	return r.directory.CompareAndPublish(ctx, historyStepName(applied.Ordinal, "applied"), nil, body)
}
func advanceHistoryPosition(before HistoryReplayPositions, kind, digest string) HistoryReplayPositions {
	switch historyNativeOwner(kind) {
	case historyOwnerKV:
		before.KV.Sequence++
		before.KV.ReceiptSHA256 = digest
	case historyOwnerSQL:
		before.SQL.Sequence++
		before.SQL.ReceiptSHA256 = digest
	case historyOwnerBlob:
		before.Blobs.Sequence++
		before.Blobs.ReceiptSHA256 = digest
	}
	return before
}
func (r *historyRunner) checkNativePositions(ctx context.Context, positions HistoryReplayPositions, skip string) error {
	if skip != historyOwnerKV {
		if err := r.targets.KV.InspectImport(ctx, r.identity.KVClaim, positions.KV, func(storage.TransferRecord) error { return nil }); err != nil {
			return err
		}
	}
	if skip != historyOwnerSQL {
		if err := r.witness.db.CheckRelationalImportPosition(ctx, r.identity.SQLOriginal, r.identity.SQL, positions.SQL); err != nil {
			return err
		}
	}
	if skip != historyOwnerBlob {
		if err := r.targets.Blobs.CheckImportPosition(ctx, r.identity.ComponentOperation, r.identity.BlobOriginal, positions.Blobs); err != nil {
			return err
		}
	}
	return ctx.Err()
}
