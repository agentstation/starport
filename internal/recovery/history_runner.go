package recovery

import (
	"context"
	"encoding/json/v2"
	"fmt"
	"path/filepath"
	"strconv"
	"time"

	"github.com/agentstation/starmap/pkg/productfiles"
	"github.com/agentstation/starport/internal/authorization/revision"
	"github.com/agentstation/starport/internal/blob"
	"github.com/agentstation/starport/internal/credentials"
	"github.com/agentstation/starport/internal/sqlstore"
	"github.com/agentstation/starport/internal/storage"
)

const (
	historyOwnerKV           = "kv"
	historyOwnerSQL          = "sql"
	historyOwnerBlob         = "blob"
	historyKVAuthorityFinal  = "kv_authorization_final"
	historySQLAuthorityFinal = "sql_authorization_final"
	historySQLIdentity       = "sql_identity"
)

// HistoryKVTarget combines inspection and ordered mutation for one selected native target.
type HistoryKVTarget interface {
	storage.ImportInspector
	storage.ImportReconciler
	storage.ImportRecordReader
	storage.ImportPositionInspector
}

// HistoryBlobTarget combines guarded capture and ordered publication for one selected target.
type HistoryBlobTarget interface {
	blob.ImportReplayInspector
	blob.ImportPublicationReplayer
}

// HistoryReplayTargets supplies configured owners. Every external writer must remain fenced.
type HistoryReplayTargets struct {
	KV         HistoryKVTarget
	Blobs      HistoryBlobTarget
	Encryption *credentials.EncryptionService
}

// HistoryReplayRequest binds selected target configuration and an existing private scratch directory.
// The composition root must check local path overlap before opening any target.
type HistoryReplayRequest struct {
	TargetSHA256       string
	ScratchDirectory   string
	CatalogPreparation *CatalogPreparationPlan
}

// HistoryReplayPositions records where each native replay chain stopped.
type HistoryReplayPositions struct {
	KV    storage.ImportReplayPosition      `json:"kv"`
	SQL   sqlstore.RelationalReplayPosition `json:"sql"`
	Blobs blob.ImportReplayPosition         `json:"blobs"`
}

// HistoryReplayReport reports retained recovery progress. It does not permit activation.
type HistoryReplayReport struct {
	TargetSHA256     string                 `json:"target_sha256"`
	Boundary         Record                 `json:"boundary"`
	AcceptanceSHA256 string                 `json:"acceptance_sha256"`
	HistorySHA256    string                 `json:"history_sha256"`
	JournalSHA256    string                 `json:"journal_sha256"`
	CompletedSteps   int                    `json:"completed_steps"`
	DeclaredSteps    int                    `json:"declared_steps"`
	Positions        HistoryReplayPositions `json:"positions"`
	KVRotated        bool                   `json:"kv_rotated"`
	SQLRotated       bool                   `json:"sql_rotated"`
	Restricted       bool                   `json:"restricted"`
}
type historyRunRecord struct {
	Version            int                        `json:"version"`
	AcceptanceSHA256   string                     `json:"acceptance_sha256"`
	HistorySHA256      string                     `json:"history_sha256"`
	TargetSHA256       string                     `json:"target_sha256"`
	Authority          revision.RecoveryAuthority `json:"authority"`
	ValidatedAt        time.Time                  `json:"validated_at"`
	CatalogPreparation *CatalogPreparationPlan    `json:"catalog_preparation,omitempty"`
}
type historyRunner struct {
	witness   *Witness
	accepted  *AcceptedHistory
	identity  PreparedImportIdentity
	targets   HistoryReplayTargets
	request   HistoryReplayRequest
	directory *productfiles.Directory
	run       historyRunRecord
	runBytes  []byte
}

// CompletedHistory retains a checked private replay journal with all admission still closed.
// Final graph, file, catalog, and authority validation remain separate requirements.
type CompletedHistory struct {
	runner *historyRunner
	report HistoryReplayReport
}

// Format excludes private replay payloads and operator references.
func (CompletedHistory) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("<private completed recovery history>"))
}

// Report returns diagnostic progress without exposing a mutation or activation capability.
func (c *CompletedHistory) Report() HistoryReplayReport {
	if c == nil {
		return HistoryReplayReport{Restricted: true}
	}
	return c.report
}

// ReplayImportedHistory applies accepted typed history under the original native import claims.
// It retains every pre-step image before mutation and reuses that image after a lost reply.
// Failure leaves admission closed. A successful result also grants no activation permission.
func (w *Witness) ReplayImportedHistory(ctx context.Context, source *RestoreSource, accepted *AcceptedHistory, targets HistoryReplayTargets, request HistoryReplayRequest) (*CompletedHistory, error) {
	runner, err := newHistoryRunner(ctx, w, source, accepted, targets, request)
	if err != nil {
		return nil, err
	}
	state, err := runner.replayUntil(ctx, len(accepted.state.history.manifest.Steps))
	if err != nil {
		return nil, err
	}
	completed := &CompletedHistory{runner: runner, report: runner.report(state)}
	if err := completed.check(ctx, w); err != nil {
		return nil, err
	}
	return completed, nil
}
func (r *historyRunner) replayUntil(ctx context.Context, stop int) (historyJournalState, error) {
	state, err := r.scanJournal(ctx)
	if err != nil {
		return state, err
	}
	for state.count < stop {
		if err := r.accepted.check(ctx, r.witness); err != nil {
			return state, err
		}
		step := r.accepted.state.history.manifest.Steps[state.count]
		if r.run.CatalogPreparation != nil && state.count == len(r.accepted.state.history.manifest.Steps)-2 && (state.catalog == nil || state.catalog.complete == "") {
			return state, ErrConflict
		}
		prepared := state.pending
		if prepared == nil {
			if err := r.guard(ctx, state.positions, ""); err != nil {
				return state, err
			}
			prepared, err = r.prepare(ctx, step, state.positions, state.previous)
			if err != nil {
				return state, err
			}
		}
		if err := r.guard(ctx, state.positions, historyNativeOwner(step.Kind)); err != nil {
			return state, err
		}
		applied, err := r.apply(ctx, *prepared)
		if err != nil {
			return state, err
		}
		if err := r.guard(ctx, applied.After, ""); err != nil {
			return state, err
		}
		if err := r.publishApplied(ctx, applied); err != nil {
			return state, err
		}
		body, err := json.Marshal(applied, json.Deterministic(true))
		if err != nil {
			return state, err
		}
		state.count++
		state.previous = historySHA256(body)
		state.positions = applied.After
		state.pending = nil
		state.kvRotated = state.kvRotated || step.Kind == historyKVAuthorityFinal
		state.sqlRotated = state.sqlRotated || step.Kind == historySQLAuthorityFinal
	}
	if state.catalog != nil && state.catalog.pending != nil {
		if err := r.guard(ctx, state.positions, historyOwnerKV); err != nil {
			return state, err
		}
		if err := r.checkCatalogPendingPosition(ctx, *state.catalog); err != nil {
			return state, err
		}
	} else if err := r.guard(ctx, state.positions, ""); err != nil {
		return state, err
	}
	return state, nil
}
func newHistoryRunner(ctx context.Context, w *Witness, source *RestoreSource, accepted *AcceptedHistory, targets HistoryReplayTargets, request HistoryReplayRequest) (*historyRunner, error) {
	if ctx == nil || w == nil || w.db == nil || source == nil || accepted == nil || accepted.state == nil || targets.KV == nil || targets.Blobs == nil || targets.Encryption == nil || !historyDigest(request.TargetSHA256) || !filepath.IsAbs(request.ScratchDirectory) || filepath.Clean(request.ScratchDirectory) != request.ScratchDirectory {
		return nil, ErrConflict
	}
	if request.CatalogPreparation != nil {
		if !validCatalogPlan(request.CatalogPreparation) {
			return nil, ErrConflict
		}
		if _, err := catalogPrefixCount(accepted.state.history.manifest.Steps); err != nil {
			return nil, err
		}
		selected := *request.CatalogPreparation
		request.CatalogPreparation = &selected
	}
	if err := accepted.check(ctx, w); err != nil {
		return nil, err
	}
	manifest := accepted.state.history.manifest
	if request.TargetSHA256 != manifest.TargetSHA256 || source.request.ManifestSHA256 != manifest.BackupSHA256 {
		return nil, ErrConflict
	}
	identity, err := source.ImportIdentity(manifest.Operation)
	if err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(identity, json.Deterministic(true))
	if err != nil || historySHA256(encoded) != manifest.PreparedSHA256 {
		return nil, ErrConflict
	}
	if err := checkHistoryRotationOrder(manifest.Steps); err != nil {
		return nil, err
	}
	if _, err := productfiles.ExistingDirectory(request.ScratchDirectory); err != nil {
		return nil, err
	}
	directory, err := productfiles.ExistingDirectory(accepted.state.directory)
	if err != nil {
		return nil, err
	}
	runner := &historyRunner{witness: w, accepted: accepted, identity: identity, targets: targets, request: request, directory: directory}
	if err := runner.openRun(ctx); err != nil {
		return nil, err
	}
	return runner, nil
}
func (r *historyRunner) guard(ctx context.Context, positions HistoryReplayPositions, skip string) error {
	if err := r.accepted.check(ctx, r.witness); err != nil {
		return err
	}
	if err := r.checkRun(ctx); err != nil {
		return err
	}
	return r.checkNativePositions(ctx, positions, skip)
}
func (c *CompletedHistory) check(ctx context.Context, witness *Witness) error {
	if ctx == nil || c == nil || c.runner == nil || witness == nil || witness.db != c.runner.witness.db {
		return ErrConflict
	}
	state, err := c.runner.scanJournal(ctx)
	if err != nil {
		return err
	}
	if state.pending != nil || state.count != len(c.runner.accepted.state.history.manifest.Steps) || c.runner.report(state) != c.report {
		return ErrConflict
	}
	if err := c.runner.guard(ctx, state.positions, ""); err != nil {
		return err
	}
	// Native owners return retained receipts for exact historical retries.
	// The final cursor checks above exclude an unrecorded successor.
	for _, step := range c.runner.accepted.state.history.manifest.Steps {
		var prepared historyPreparedStep
		if _, err := c.runner.readJournalRecord(ctx, historyStepName(step.Ordinal, "prepared"), &prepared); err != nil {
			return err
		}
		var retained historyAppliedStep
		if _, err := c.runner.readJournalRecord(ctx, historyStepName(step.Ordinal, "applied"), &retained); err != nil {
			return err
		}
		repeated, err := c.runner.apply(ctx, prepared)
		if err != nil {
			return err
		}
		if repeated != retained {
			return ErrConflict
		}
	}
	return c.runner.guard(ctx, state.positions, "")
}
func (r *historyRunner) report(state historyJournalState) HistoryReplayReport {
	return HistoryReplayReport{TargetSHA256: r.request.TargetSHA256, Boundary: r.accepted.state.boundary, AcceptanceSHA256: r.accepted.Digest(), HistorySHA256: r.accepted.state.history.digest, JournalSHA256: state.previous, CompletedSteps: state.count, DeclaredSteps: len(r.accepted.state.history.manifest.Steps), Positions: state.positions, KVRotated: state.kvRotated, SQLRotated: state.sqlRotated, Restricted: true}
}
func historyNativeOwner(kind string) string {
	switch kind {
	case historySQLIdentity, historySQLAuthorityFinal:
		return historyOwnerSQL
	case "blob_publication":
		return historyOwnerBlob
	default:
		return historyOwnerKV
	}
}
func checkHistoryRotationOrder(steps []historyStep) error {
	kv, sql := false, false
	for _, step := range steps {
		switch historyNativeOwner(step.Kind) {
		case historyOwnerKV:
			if kv {
				return ErrConflict
			}
			if step.Kind == historyKVAuthorityFinal {
				kv = true
			}
		case historyOwnerSQL:
			if sql {
				return ErrConflict
			}
			if step.Kind == historySQLAuthorityFinal {
				sql = true
			}
		}
	}
	return nil
}
func historyAcceptedAuthority(a *AcceptedHistory) revision.RecoveryAuthority {
	return revision.RecoveryAuthority{RecoveryID: a.state.digest, Epoch: strconv.FormatInt(a.state.boundary.Epoch, 10)}
}
