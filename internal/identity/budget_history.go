package identity

import (
	"context"
	"database/sql"
	"errors"

	"github.com/agentstation/starport/internal/policyrecord"
)

// TeamBudgetHistory consumes a fresh team's one-use accounting grant. A false
// result never proves zero usage. Successful initialization is acknowledged by
// the budget store; an interrupted cross-store handoff requires reconciliation.
type TeamBudgetHistory interface {
	ClaimBudgetHistory(ctx context.Context, teamID, interval, historyID string) (bool, error)
}

// retainBudgetOrigin runs in the same transaction that creates the team. The
// retained row is not deleted with the team. It prevents reuse of a team ID from
// receiving another zero-history grant.
func (r *teamRepository) retainBudgetOrigin(ctx context.Context, tx *sql.Tx, team Team) error {
	var count int
	if err := tx.QueryRowContext(ctx, r.db.Bind(`SELECT COUNT(*) FROM team_budget_origins WHERE team_id = ?`), team.ID).Scan(&count); err != nil {
		return err
	}
	if count != 0 {
		return nil
	}
	historyID, allowed := "", 0
	if team.Budget != nil {
		historyID, allowed = team.Budget.HistoryID, 1
	}
	_, err := tx.ExecContext(ctx, r.db.Bind(`INSERT INTO team_budget_origins (team_id, history_id, initialize_allowed) VALUES (?, ?, ?)`), team.ID, historyID, allowed)
	return err
}

func (r *teamRepository) ClaimBudgetHistory(ctx context.Context, teamID, interval, historyID string) (bool, error) {
	if teamID == "" || historyID == "" {
		return false, ErrMissingID
	}
	// An absent or consumed grant is a read-only refusal. Repeated recovery
	// failures must not advance identity revisions or revoke unrelated callers.
	var retained string
	var allowed int
	err := r.db.QueryRowContext(ctx, r.db.Bind(`SELECT history_id, initialize_allowed FROM team_budget_origins WHERE team_id = ?`), teamID).Scan(&retained, &allowed)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if allowed != 1 || retained != historyID {
		return false, nil
	}
	claimed := false
	err = r.authority.Apply(ctx, func(tx *sql.Tx) error {
		var data sql.NullString
		err := tx.QueryRowContext(ctx, r.db.Bind(boundedIdentityQuery(r.db.Dialect(), `SELECT record FROM teams WHERE id = ?`)), policyrecord.MaxBytes, teamID).Scan(&data)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrTeamNotFound
		}
		if err != nil {
			return err
		}
		if !data.Valid {
			return policyrecord.ErrTooLarge
		}
		stored, err := decodeTeam(data.String)
		if err != nil {
			return err
		}
		if stored.Team.Budget == nil || stored.Team.Budget.HistoryID != historyID || stored.Team.Budget.Interval != interval {
			return nil
		}
		result, err := tx.ExecContext(ctx, r.db.Bind(`UPDATE team_budget_origins SET initialize_allowed = 0 WHERE team_id = ? AND history_id = ? AND initialize_allowed = 1`), teamID, historyID)
		if err != nil {
			return err
		}
		count, err := result.RowsAffected()
		claimed = count == 1
		return err
	})
	// A lost commit acknowledgement cannot authorize initialization.
	return claimed && err == nil, err
}

var _ TeamBudgetHistory = (*teamRepository)(nil)
