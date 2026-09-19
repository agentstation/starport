package identity

import (
	"context"
	"database/sql"

	"github.com/agentstation/starport/internal/authorization/revision"
	"github.com/agentstation/starport/internal/sqlstore"
)

type principalKind uint8

const (
	userPrincipal principalKind = iota
	teamPrincipal
)

func deletePrincipal(ctx context.Context, db *sqlstore.DB, authority *revision.SQL, kind principalKind, id string, expected uint64) error {
	statements := [3]string{
		`DELETE FROM account_grants WHERE user_id = ?`,
		`DELETE FROM team_memberships WHERE user_id = ?`,
		`DELETE FROM users WHERE id = ? AND revision = ?`,
	}
	conflict := ErrUserConflict
	if kind == teamPrincipal {
		statements = [3]string{
			`DELETE FROM account_grants WHERE team_id = ?`,
			`DELETE FROM team_memberships WHERE team_id = ?`,
			`DELETE FROM teams WHERE id = ? AND revision = ?`,
		}
		conflict = ErrTeamConflict
	}
	return authority.Apply(ctx, func(tx *sql.Tx) error {
		for _, statement := range statements[:2] {
			if _, err := tx.ExecContext(ctx, db.Bind(statement), id); err != nil {
				return err
			}
		}
		result, err := tx.ExecContext(ctx, db.Bind(statements[2]), id, expected)
		if err != nil {
			return err
		}
		return oneRowMoved(result, conflict)
	})
}
