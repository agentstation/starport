package identity

import (
	"context"
	"database/sql"
	"encoding/json/v2"
	"errors"
	"reflect"

	"github.com/agentstation/starport/internal/policyrecord"
	"github.com/agentstation/starport/internal/sqlstore"
)

func recoveryLock(db *sqlstore.DB, query string) string {
	if db.Dialect() != sqlstore.TypeSQLite {
		return query + " FOR UPDATE"
	}
	return query
}
func readRecoveryUser(ctx context.Context, db *sqlstore.DB, conn *sql.Conn, id string) (*UserRecord, error) {
	query := boundedIdentityQuery(db.Dialect(), `SELECT record,SUBSTR(id,1,192),SUBSTR(subject,1,192),revision FROM users WHERE id=?`)
	var data sql.NullString
	var found, subject string
	var revision uint64
	err := conn.QueryRowContext(ctx, db.Bind(recoveryLock(db, query)), policyrecord.MaxBytes, id).Scan(&data, &found, &subject, &revision)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !data.Valid || found != id {
		return nil, ErrReplayConflict
	}
	var stored userRecord
	if json.Unmarshal([]byte(data.String), &stored, json.RejectUnknownMembers(true)) != nil || stored.SchemaVersion != StorageSchemaVersion || stored.User.ID != found || stored.User.Subject != subject || stored.Revision != revision {
		return nil, ErrReplayConflict
	}
	value := UserRecord{Revision: stored.Revision, User: stored.User}
	if normalizeRecoveryUser(&RecoveryUserChange{Before: &value}) != nil {
		return nil, ErrReplayConflict
	}
	return &value, nil
}
func readRecoveryTeam(ctx context.Context, db *sqlstore.DB, conn *sql.Conn, id string) (*TeamRecord, error) {
	query := boundedIdentityQuery(db.Dialect(), `SELECT record,SUBSTR(id,1,192),revision FROM teams WHERE id=?`)
	var data sql.NullString
	var found string
	var revision uint64
	err := conn.QueryRowContext(ctx, db.Bind(recoveryLock(db, query)), policyrecord.MaxBytes, id).Scan(&data, &found, &revision)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !data.Valid || found != id {
		return nil, ErrReplayConflict
	}
	var stored teamRecord
	if json.Unmarshal([]byte(data.String), &stored, json.RejectUnknownMembers(true)) != nil || stored.SchemaVersion != StorageSchemaVersion || stored.Team.ID != found || stored.Revision != revision {
		return nil, ErrReplayConflict
	}
	value := TeamRecord{Revision: stored.Revision, Team: stored.Team}
	if normalizeRecoveryTeam(&RecoveryTeamChange{Before: &value}) != nil {
		return nil, ErrReplayConflict
	}
	return &value, nil
}
func applyRecoveryUser(ctx context.Context, db *sqlstore.DB, conn *sql.Conn, change RecoveryUserChange) error {
	selected := change.Before
	if selected == nil {
		selected = change.After
	}
	current, err := readRecoveryUser(ctx, db, conn, selected.User.ID)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(current, change.Before) {
		return ErrReplayConflict
	}
	if change.After == nil {
		if err := requireRecoveryUnlinked(ctx, db, conn, userPrincipal, selected.User.ID); err != nil {
			return err
		}
		return execRecoveryWrite(ctx, conn, db.Bind(`DELETE FROM users WHERE id=? AND revision=?`), selected.User.ID, selected.Revision)
	}
	data, err := policyrecord.Marshal(userRecord{SchemaVersion: StorageSchemaVersion, Revision: change.After.Revision, User: change.After.User})
	if err != nil {
		return ErrReplayConflict
	}
	if current == nil {
		return execRecoveryWrite(ctx, conn, db.Bind(`INSERT INTO users(id,subject,revision,record) VALUES(?,?,?,?)`), change.After.User.ID, change.After.User.Subject, change.After.Revision, string(data))
	}
	return execRecoveryWrite(ctx, conn, db.Bind(`UPDATE users SET revision=?,record=? WHERE id=? AND revision=?`), change.After.Revision, string(data), current.User.ID, current.Revision)
}
func applyRecoveryTeam(ctx context.Context, db *sqlstore.DB, conn *sql.Conn, change RecoveryTeamChange) error {
	selected := change.Before
	if selected == nil {
		selected = change.After
	}
	current, err := readRecoveryTeam(ctx, db, conn, selected.Team.ID)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(current, change.Before) {
		return ErrReplayConflict
	}
	if change.After == nil {
		if err := requireRecoveryTeamOrigin(ctx, db, conn, selected.Team.ID); err != nil {
			return err
		}
		if err := requireRecoveryUnlinked(ctx, db, conn, teamPrincipal, selected.Team.ID); err != nil {
			return err
		}
		return execRecoveryWrite(ctx, conn, db.Bind(`DELETE FROM teams WHERE id=? AND revision=?`), selected.Team.ID, selected.Revision)
	}
	data, err := policyrecord.Marshal(teamRecord{SchemaVersion: StorageSchemaVersion, Revision: change.After.Revision, Team: change.After.Team})
	if err != nil {
		return ErrReplayConflict
	}
	if current == nil {
		return execRecoveryWrite(ctx, conn, db.Bind(`INSERT INTO teams(id,revision,record) VALUES(?,?,?)`), change.After.Team.ID, change.After.Revision, string(data))
	}
	return execRecoveryWrite(ctx, conn, db.Bind(`UPDATE teams SET revision=?,record=? WHERE id=? AND revision=?`), change.After.Revision, string(data), current.Team.ID, current.Revision)
}
func requireRecoveryUnlinked(ctx context.Context, db *sqlstore.DB, conn *sql.Conn, kind principalKind, id string) error {
	queries := []string{`SELECT 1 FROM team_memberships WHERE user_id=? LIMIT 1`, `SELECT 1 FROM account_grants WHERE user_id=? LIMIT 1`}
	if kind == teamPrincipal {
		queries = []string{`SELECT 1 FROM team_memberships WHERE team_id=? LIMIT 1`, `SELECT 1 FROM account_grants WHERE team_id=? LIMIT 1`}
	}
	for _, query := range queries {
		var found int
		err := conn.QueryRowContext(ctx, db.Bind(query), id).Scan(&found)
		if !errors.Is(err, sql.ErrNoRows) {
			return errors.Join(ErrReplayConflict, err)
		}
	}
	return nil
}
func requireRecoveryPrincipal(ctx context.Context, db *sqlstore.DB, conn *sql.Conn, kind principalKind, id string) error {
	if kind == userPrincipal {
		value, err := readRecoveryUser(ctx, db, conn, id)
		if err != nil || value == nil {
			return errors.Join(ErrReplayConflict, err)
		}
		return nil
	}
	value, err := readRecoveryTeam(ctx, db, conn, id)
	if err != nil || value == nil {
		return errors.Join(ErrReplayConflict, err)
	}
	return nil
}
func execRecoveryWrite(ctx context.Context, conn *sql.Conn, query string, args ...any) error {
	result, err := conn.ExecContext(ctx, query, args...)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return ErrReplayConflict
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrReplayConflict
	}
	return nil
}
