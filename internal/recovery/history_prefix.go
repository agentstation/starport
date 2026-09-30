package recovery

import (
	"context"
	"fmt"
	"math"
)

// HistoryPrefix retains checked non-final history. It grants no activation permission.
type HistoryPrefix struct{ runner *historyRunner }

// Format omits private accepted history and replay evidence.
func (p HistoryPrefix) Format(state fmt.State, _ rune) {
	_, _ = fmt.Fprint(state, "<private recovery history prefix>")
}

func validCatalogPlan(plan *CatalogPreparationPlan) bool {
	return plan != nil && historyDigest(plan.TopologySHA256) && plan.StageCount >= 0 && plan.StageCount < math.MaxInt
}
func sameCatalogPlan(a, b *CatalogPreparationPlan) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}
func catalogPrefixCount(steps []historyStep) (int, error) {
	if len(steps) < 2 || steps[len(steps)-2].Kind != historyKVAuthorityFinal || steps[len(steps)-1].Kind != historySQLAuthorityFinal {
		return 0, ErrConflict
	}
	for _, step := range steps[:len(steps)-2] {
		if step.Kind == historyKVAuthorityFinal || step.Kind == historySQLAuthorityFinal {
			return 0, ErrConflict
		}
	}
	return len(steps) - 2, nil
}

// ReplayImportedHistoryPrefix stops before the required final KV and SQL rotations.
// The declared topology and stage count remain immutable across every retry.
func (w *Witness) ReplayImportedHistoryPrefix(ctx context.Context, source *RestoreSource, accepted *AcceptedHistory, targets HistoryReplayTargets, request HistoryReplayRequest) (*HistoryPrefix, error) {
	if ctx == nil || !validCatalogPlan(request.CatalogPreparation) {
		return nil, ErrConflict
	}
	runner, err := newHistoryRunner(ctx, w, source, accepted, targets, request)
	if err != nil {
		return nil, err
	}
	stop, err := catalogPrefixCount(accepted.state.history.manifest.Steps)
	if err != nil {
		return nil, err
	}
	state, err := runner.replayUntil(ctx, stop)
	if err != nil {
		return nil, err
	}
	if state.count != stop || state.pending != nil {
		return nil, ErrConflict
	}
	return &HistoryPrefix{runner: runner}, nil
}

// checkCatalogPrefix checks original typed records without decoding catalog stage assets.
func (r *historyRunner) checkCatalogPrefix(ctx context.Context, lane catalogJournalState) error {
	stop, err := catalogPrefixCount(r.accepted.state.history.manifest.Steps)
	if err != nil {
		return err
	}
	state := historyJournalState{previous: historySHA256(r.runBytes)}
	for _, step := range r.accepted.state.history.manifest.Steps[:stop] {
		var prepared historyPreparedStep
		before, err := r.readJournalRecord(ctx, historyStepName(step.Ordinal, historyPreparedPhase), &prepared)
		if err != nil || before == nil || !r.validPrepared(prepared, step, state) {
			return ErrConflict
		}
		var applied historyAppliedStep
		after, err := r.readJournalRecord(ctx, historyStepName(step.Ordinal, historyAppliedPhase), &applied)
		if err != nil || after == nil || applied.Version != 1 || applied.Ordinal != step.Ordinal || applied.PreparedSHA256 != historySHA256(before) || !historyDigest(applied.NativeReceiptSHA256) || applied.After != advanceHistoryPosition(prepared.Before, step.Kind, applied.NativeReceiptSHA256) {
			return ErrConflict
		}
		state.previous = historySHA256(after)
		state.positions = applied.After
	}
	if state.previous != lane.prefixPrevious || state.positions != lane.prefixPositions {
		return ErrConflict
	}
	return nil
}
