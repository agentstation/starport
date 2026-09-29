package identity

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/agentstation/starport/internal/sqlstore"
)

func applyRecoveryMembership(ctx context.Context, db *sqlstore.DB, conn *sql.Conn, change RecoveryMembershipChange) error {
	value := change.Before
	if value == nil {
		value = change.After
	}
	var user, team, stamp string
	err := conn.QueryRowContext(ctx, db.Bind(recoveryLock(db, `SELECT SUBSTR(user_id,1,192),SUBSTR(team_id,1,192),SUBSTR(created_at,1,65) FROM team_memberships WHERE user_id=? AND team_id=?`)), value.UserID, value.TeamID).Scan(&user, &team, &stamp)
	if errors.Is(err, sql.ErrNoRows) {
		if change.Before != nil {
			return ErrReplayConflict
		}
		if err := requireRecoveryPrincipal(ctx, db, conn, userPrincipal, value.UserID); err != nil {
			return err
		}
		if err := requireRecoveryPrincipal(ctx, db, conn, teamPrincipal, value.TeamID); err != nil {
			return err
		}
		return execRecoveryWrite(ctx, conn, db.Bind(`INSERT INTO team_memberships(user_id,team_id,created_at) VALUES(?,?,?)`), value.UserID, value.TeamID, value.CreatedAt.Format(time.RFC3339Nano))
	}
	if err != nil {
		return err
	}
	created, err := time.Parse(time.RFC3339Nano, stamp)
	if err != nil || change.Before == nil || user != value.UserID || team != value.TeamID || !created.Equal(value.CreatedAt) {
		return ErrReplayConflict
	}
	return execRecoveryWrite(ctx, conn, db.Bind(`DELETE FROM team_memberships WHERE user_id=? AND team_id=? AND created_at=?`), value.UserID, value.TeamID, stamp)
}
func applyRecoveryGrant(ctx context.Context, db *sqlstore.DB, conn *sql.Conn, change RecoveryGrantChange) error {
	value := change.Before
	if value == nil {
		value = change.After
	}
	var account, user, team, stamp string
	err := conn.QueryRowContext(ctx, db.Bind(recoveryLock(db, `SELECT SUBSTR(account_id,1,192),SUBSTR(user_id,1,192),SUBSTR(team_id,1,192),SUBSTR(created_at,1,65) FROM account_grants WHERE account_id=? AND user_id=? AND team_id=?`)), value.AccountID, value.UserID, value.TeamID).Scan(&account, &user, &team, &stamp)
	if errors.Is(err, sql.ErrNoRows) {
		if change.Before != nil {
			return ErrReplayConflict
		}
		kind, id := userPrincipal, value.UserID
		if value.TeamID != "" {
			kind, id = teamPrincipal, value.TeamID
		}
		if err := requireRecoveryPrincipal(ctx, db, conn, kind, id); err != nil {
			return err
		}
		return execRecoveryWrite(ctx, conn, db.Bind(`INSERT INTO account_grants(account_id,user_id,team_id,created_at) VALUES(?,?,?,?)`), value.AccountID, value.UserID, value.TeamID, value.CreatedAt.Format(time.RFC3339Nano))
	}
	if err != nil {
		return err
	}
	created, err := time.Parse(time.RFC3339Nano, stamp)
	if err != nil || change.Before == nil || account != value.AccountID || user != value.UserID || team != value.TeamID || !created.Equal(value.CreatedAt) {
		return ErrReplayConflict
	}
	return execRecoveryWrite(ctx, conn, db.Bind(`DELETE FROM account_grants WHERE account_id=? AND user_id=? AND team_id=? AND created_at=?`), value.AccountID, value.UserID, value.TeamID, stamp)
}
func readRecoveryOrigin(ctx context.Context, db *sqlstore.DB, conn *sql.Conn, id string) (*RecoveryBudgetOrigin, error) {
	var value RecoveryBudgetOrigin
	var allowed int
	err := conn.QueryRowContext(ctx, db.Bind(recoveryLock(db, `SELECT SUBSTR(team_id,1,192),SUBSTR(history_id,1,257),initialize_allowed FROM team_budget_origins WHERE team_id=?`)), id).Scan(&value.TeamID, &value.HistoryID, &allowed)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	value.InitializeAllowed = allowed == 1
	if value.TeamID != id || (allowed != 0 && allowed != 1) || validateRecoveryOrigin(&RecoveryOriginChange{After: &value}) != nil {
		return nil, ErrReplayConflict
	}
	return &value, nil
}
func applyRecoveryOrigin(ctx context.Context, db *sqlstore.DB, conn *sql.Conn, change RecoveryOriginChange) error {
	current, err := readRecoveryOrigin(ctx, db, conn, change.After.TeamID)
	if err != nil {
		return err
	}
	if (current == nil) != (change.Before == nil) || current != nil && *current != *change.Before {
		return ErrReplayConflict
	}
	allowed := 0
	if change.After.InitializeAllowed {
		allowed = 1
	}
	if current == nil {
		return execRecoveryWrite(ctx, conn, db.Bind(`INSERT INTO team_budget_origins(team_id,history_id,initialize_allowed) VALUES(?,?,?)`), change.After.TeamID, change.After.HistoryID, allowed)
	}
	if *current == *change.After {
		return nil
	}
	return execRecoveryWrite(ctx, conn, db.Bind(`UPDATE team_budget_origins SET initialize_allowed=? WHERE team_id=? AND history_id=? AND initialize_allowed=1`), allowed, current.TeamID, current.HistoryID)
}
func requireRecoveryTeamOrigin(ctx context.Context, db *sqlstore.DB, conn *sql.Conn, id string) error {
	team, err := readRecoveryTeam(ctx, db, conn, id)
	if err != nil {
		return err
	}
	if team == nil {
		return nil
	}
	origin, err := readRecoveryOrigin(ctx, db, conn, id)
	if err != nil || origin == nil {
		return errors.Join(ErrReplayConflict, err)
	}

	return nil
}
