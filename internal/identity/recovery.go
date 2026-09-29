package identity

import (
	"context"
	"database/sql"
	"encoding/json/v2"
	"errors"
	"strings"
	"time"

	"github.com/agentstation/starport/internal/policyrecord"
	"github.com/agentstation/starport/internal/sqlstore"
)

// ErrRecoveryReference reports inconsistent identity or grant records in a backup.
var ErrRecoveryReference = errors.New("captured identity reference is invalid")

// RecoveryReport counts verified identity records and unavailable account references.
// Counts do not authorize account selection or restore permission.
type RecoveryReport struct {
	BudgetOrigins          int64 `json:"budget_origins"`
	UnknownBudgetHistories int64 `json:"unknown_budget_histories"`
	Users                  int64 `json:"users"`
	Teams                  int64 `json:"teams"`
	Memberships            int64 `json:"memberships"`
	Grants                 int64 `json:"grants"`
	MissingGrantAccounts   int64 `json:"missing_grant_accounts"`
}

// VerifyRecoverySnapshot checks identities and their links in a verified SQL copy.
// Deleted accounts remain diagnostic references. Missing users and teams cause refusal.
func VerifyRecoverySnapshot(ctx context.Context, source *sqlstore.RelationalSnapshotView, accountExists func(context.Context, string) (bool, error), checkTeamBudget func(context.Context, Team) (bool, error)) (report RecoveryReport, err error) {
	if source == nil || accountExists == nil || checkTeamBudget == nil {
		return report, ErrRecoveryReference
	}
	if report.Users, err = verifyRecoveryUsers(ctx, source); err != nil {
		return report, err
	}
	if report.Teams, report.UnknownBudgetHistories, err = verifyRecoveryTeams(ctx, source, checkTeamBudget); err != nil {
		return report, err
	}
	if report.BudgetOrigins, err = verifyRecoveryBudgetOrigins(ctx, source); err != nil {
		return report, err
	}
	if report.Memberships, err = verifyRecoveryMemberships(ctx, source); err != nil {
		return report, err
	}
	report.Grants, report.MissingGrantAccounts, err = verifyRecoveryGrants(ctx, source, accountExists)
	return report, err
}

func verifyRecoveryUsers(ctx context.Context, source *sqlstore.RelationalSnapshotView) (count int64, resultErr error) {
	rows, err := source.QueryContext(ctx, `SELECT id,subject,revision,CASE WHEN length(CAST(record AS BLOB))<=? THEN record END FROM users ORDER BY id`, policyrecord.MaxBytes)
	if err != nil {
		return 0, err
	}
	defer func() { resultErr = errors.Join(resultErr, rows.Close()) }()
	for rows.Next() {
		var id, subject string
		var revision uint64
		var data sql.NullString
		if err := rows.Scan(&id, &subject, &revision, &data); err != nil {
			return count, err
		}
		if !data.Valid {
			return count, policyrecord.ErrTooLarge
		}
		var strict userRecord
		if json.Unmarshal([]byte(data.String), &strict, json.RejectUnknownMembers(true)) != nil {
			return count, ErrRecoveryReference
		}
		stored, err := decodeUser(data.String)
		if err != nil || stored.User.ID != id || stored.User.Subject != subject || stored.Revision != revision {
			return count, ErrRecoveryReference
		}
		count++
	}
	return count, rows.Err()
}

func verifyRecoveryTeams(ctx context.Context, source *sqlstore.RelationalSnapshotView, checkBudget func(context.Context, Team) (bool, error)) (count, unknown int64, resultErr error) {
	rows, err := source.QueryContext(ctx, `SELECT t.id,t.revision,CASE WHEN length(CAST(t.record AS BLOB))<=? THEN t.record END,o.team_id FROM teams t LEFT JOIN team_budget_origins o ON o.team_id=t.id ORDER BY t.id`, policyrecord.MaxBytes)
	if err != nil {
		return 0, 0, err
	}
	defer func() { resultErr = errors.Join(resultErr, rows.Close()) }()
	for rows.Next() {
		var id string
		var revision uint64
		var data, origin sql.NullString
		if err := rows.Scan(&id, &revision, &data, &origin); err != nil {
			return count, unknown, err
		}
		if !data.Valid {
			return count, unknown, policyrecord.ErrTooLarge
		}
		var strict teamRecord
		if json.Unmarshal([]byte(data.String), &strict, json.RejectUnknownMembers(true)) != nil {
			return count, unknown, ErrRecoveryReference
		}
		stored, err := decodeTeam(data.String)
		if err != nil || stored.Team.ID != id || stored.Revision != revision || !origin.Valid {
			return count, unknown, ErrRecoveryReference
		}
		known, err := checkBudget(ctx, stored.Team)
		if err != nil {
			return count, unknown, err
		}
		if !known {
			unknown++
		}
		count++
	}
	return count, unknown, rows.Err()
}

func verifyRecoveryMemberships(ctx context.Context, source *sqlstore.RelationalSnapshotView) (count int64, resultErr error) {
	rows, err := source.QueryContext(ctx, `SELECT m.user_id,m.team_id,m.created_at,u.id,t.id FROM team_memberships m LEFT JOIN users u ON u.id=m.user_id LEFT JOIN teams t ON t.id=m.team_id`)
	if err != nil {
		return 0, err
	}
	defer func() { resultErr = errors.Join(resultErr, rows.Close()) }()
	for rows.Next() {
		var value Membership
		var created string
		var user, team sql.NullString
		if err := rows.Scan(&value.UserID, &value.TeamID, &created, &user, &team); err != nil {
			return count, err
		}
		if value.Validate() != nil || !user.Valid || !team.Valid {
			return count, ErrRecoveryReference
		}
		if _, err := time.Parse(time.RFC3339Nano, created); err != nil {
			return count, ErrRecoveryReference
		}
		count++
	}
	return count, rows.Err()
}

func verifyRecoveryGrants(ctx context.Context, source *sqlstore.RelationalSnapshotView, accountExists func(context.Context, string) (bool, error)) (count, missing int64, resultErr error) {
	rows, err := source.QueryContext(ctx, `SELECT g.account_id,g.user_id,g.team_id,g.created_at,u.id,t.id FROM account_grants g LEFT JOIN users u ON u.id=g.user_id LEFT JOIN teams t ON t.id=g.team_id`)
	if err != nil {
		return 0, 0, err
	}
	defer func() { resultErr = errors.Join(resultErr, rows.Close()) }()
	for rows.Next() {
		var value AccountGrant
		var created string
		var user, team sql.NullString
		if err := rows.Scan(&value.AccountID, &value.UserID, &value.TeamID, &created, &user, &team); err != nil {
			return count, missing, err
		}
		if value.Validate() != nil || (value.UserID != "" && !user.Valid) || (value.TeamID != "" && !team.Valid) {
			return count, missing, ErrRecoveryReference
		}
		if _, err := time.Parse(time.RFC3339Nano, created); err != nil {
			return count, missing, ErrRecoveryReference
		}
		found, err := accountExists(ctx, value.AccountID)
		if err != nil {
			return count, missing, err
		}
		if !found {
			missing++
		}
		count++
	}
	return count, missing, rows.Err()
}

// Retained origins can outlive their team. A consumed grant does not prove a completed KV initialization.
func verifyRecoveryBudgetOrigins(ctx context.Context, source *sqlstore.RelationalSnapshotView) (count int64, resultErr error) {
	rows, err := source.QueryContext(ctx, `SELECT team_id,history_id,initialize_allowed FROM team_budget_origins`)
	if err != nil {
		return 0, err
	}
	defer func() { resultErr = errors.Join(resultErr, rows.Close()) }()
	for rows.Next() {
		var id, history string
		var allowed int
		if err := rows.Scan(&id, &history, &allowed); err != nil {
			return count, err
		}
		if !validID(id) || (len(history) > 256 || strings.IndexFunc(history, func(r rune) bool { return r < 32 || r == 127 }) >= 0) || (allowed != 0 && allowed != 1) || (allowed == 1 && history == "") {
			return count, ErrRecoveryReference
		}
		count++
	}
	return count, rows.Err()
}
