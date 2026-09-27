package reservation

import (
	"context"
	"encoding/json/v2"
	"errors"

	"github.com/agentstation/starport/internal/limits"
	"github.com/agentstation/starport/internal/storage"
)

// TeamHistoryAuthority owns the independent, one-use SQL grant. Its successful
// return authorizes exactly one KV initialization attempt, never a reset.
type TeamHistoryAuthority interface {
	ClaimBudgetHistory(ctx context.Context, teamID, interval, historyID string) (bool, error)
}

type teamHistoryReceipt struct {
	Version int    `json:"version"`
	Meter   Meter  `json:"meter"`
	History string `json:"history"`
}

// InitializeTeamHistory belongs to bounded policy refresh, outside admission.
// It consumes SQL permission before the atomic KV initialization. A durable
// receipt makes completed retries safe. Without a receipt, consumed permission
// requires recovery; a restart must not infer fresh capacity from missing state.
func (r *Repository) InitializeTeamHistory(ctx context.Context, authority TeamHistoryAuthority, teamID, interval, historyID string) error {
	meter := Meter{Scope: limits.ScopeTeam, Holder: teamID, Dimension: limits.DimensionSpend, Interval: interval}
	if authority == nil || !meter.valid() || !validID(historyID) {
		return ErrInvalid
	}
	receipt := teamHistoryReceipt{Version: recordVersion, Meter: meter, History: historyID}
	key := storageKey("team-origin", receipt)
	err := r.verifyTeamReceipt(ctx, key, receipt)
	if !errors.Is(err, storage.ErrNotFound) {
		return err
	}
	claimed, err := authority.ClaimBudgetHistory(ctx, teamID, interval, historyID)
	if err != nil {
		return err
	}
	if !claimed {
		return r.completedTeamInitialization(ctx, key, receipt)
	}
	history, err := FreshHistoryMutation(meter, historyID)
	if err != nil {
		return err
	}
	ack, err := encodeMutation(key, nil, receipt)
	if err != nil {
		return err
	}
	identity := holderIdentity{Scope: limits.ScopeTeam, ID: teamID}
	holder, err := encodeMutation(storageKey("holder", identity), nil, identity)
	if err != nil {
		return err
	}
	err = r.store.CompareAndSwapInWindow(ctx, []storage.CompareAndSwapMutation{holder, history, ack}, storage.TimeWindow{})
	if err == nil {
		return nil
	}
	// An acknowledgement may be lost after commit. Only the complete native
	// receipt permits success; never repeat the write under a consumed grant.
	verified := r.completedTeamInitialization(ctx, key, receipt)
	if verified == nil {
		return nil
	}
	return errors.Join(err, verified)
}

func (r *Repository) completedTeamInitialization(ctx context.Context, key string, receipt teamHistoryReceipt) error {
	err := r.verifyTeamReceipt(ctx, key, receipt)
	if errors.Is(err, storage.ErrNotFound) {
		return ErrHistoryUnknown
	}
	return err
}

func (r *Repository) verifyTeamReceipt(ctx context.Context, key string, expected teamHistoryReceipt) error {
	data, err := r.read(ctx, key)
	if err != nil {
		return err
	}
	var stored teamHistoryReceipt
	if json.Unmarshal(data, &stored) != nil || stored != expected {
		return ErrUnavailable
	}
	_, _, err = r.readHistory(ctx, expected.Meter, expected.History)
	return err
}
