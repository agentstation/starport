package revision

import (
	"context"
	"database/sql"
	"encoding/json/v2"
	"fmt"
	"math"

	"github.com/agentstation/starport/internal/sqlstore"
)

type sqlRecoveryPayload struct {
	Version   int               `json:"version"`
	Expected  *Stamp            `json:"expected"`
	Authority RecoveryAuthority `json:"authority"`
}

// SQLRecoveryTransition binds the complete retained row or explicit absence to replacement authority.
type SQLRecoveryTransition struct{ payload *sqlRecoveryPayload }

// Format excludes recovery evidence from diagnostic formatting.
func (SQLRecoveryTransition) Format(state fmt.State, _ rune) { redactRecovery(state) }

// MarshalJSON returns private evidence for the ordered relational replay receipt.
func (r SQLRecoveryTransition) MarshalJSON() ([]byte, error) { return encodeRecovery(r.payload) }

// UnmarshalJSON refuses unknown fields, unsupported versions, and invalid authority replacement.
func (r *SQLRecoveryTransition) UnmarshalJSON(data []byte) error {
	if r == nil || len(data) > maxRecoveryTransitionBytes || !explicitRecoveryMember(data, "expected") {
		return ErrRecoveryConflict
	}
	var payload sqlRecoveryPayload
	if json.Unmarshal(data, &payload, json.RejectUnknownMembers(true)) != nil || payload.Version != 1 || !payload.Authority.valid() {
		return ErrRecoveryConflict
	}
	if payload.Expected != nil && (!validRecoveryStamp(*payload.Expected) || payload.Expected.Sequence > math.MaxInt64 || payload.Expected.Epoch == payload.Authority.Epoch) {
		return ErrRecoveryConflict
	}
	r.payload = &payload
	return nil
}

// NewSQLRecoveryTransition copies the exact expected row and explicitly accepted replacement authority.
func NewSQLRecoveryTransition(expected *Stamp, authority RecoveryAuthority) (SQLRecoveryTransition, error) {
	data, err := encodeRecovery(sqlRecoveryPayload{Version: 1, Expected: expected, Authority: authority})
	if err != nil {
		return SQLRecoveryTransition{}, err
	}
	var result SQLRecoveryTransition
	err = result.UnmarshalJSON(data)
	return result, err
}

// Digest binds canonical typed input for RelationalReplayStep. It establishes no permission.
func (r SQLRecoveryTransition) Digest() (string, error) {
	data, err := r.MarshalJSON()
	var checked SQLRecoveryTransition
	if err != nil || checked.UnmarshalJSON(data) != nil {
		return "", ErrRecoveryConflict
	}
	return recoveryDigest(data), nil
}

// CaptureSQLRecovery reads the complete singleton authority without initializing it.
// The caller supplies a connection from its fenced snapshot or closed replay transaction.
func CaptureSQLRecovery(ctx context.Context, db *sqlstore.DB, conn *sql.Conn) (*Stamp, error) {
	return readSQLRecovery(ctx, db, conn, false)
}

func readSQLRecovery(ctx context.Context, db *sqlstore.DB, conn *sql.Conn, lock bool) (*Stamp, error) {
	if ctx == nil || db == nil || conn == nil {
		return nil, ErrRecoveryConflict
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	size := "OCTET_LENGTH(epoch)"
	if db.Dialect() == sqlstore.TypeSQLite {
		size = "LENGTH(CAST(epoch AS BLOB))"
	}
	query := "SELECT id, CASE WHEN " + size + " > 256 THEN NULL ELSE epoch END, sequence FROM authorization_revision ORDER BY id LIMIT 2"
	if lock && db.Dialect() != sqlstore.TypeSQLite {
		query += " FOR UPDATE"
	}
	rows, err := conn.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var found *Stamp
	for rows.Next() {
		var id, sequence int64
		var epoch sql.NullString
		if err := rows.Scan(&id, &epoch, &sequence); err != nil {
			return nil, err
		}
		if found != nil || id != 1 || !epoch.Valid || sequence <= 0 {
			return nil, ErrRecoveryConflict
		}
		stamp := Stamp{Epoch: epoch.String, Sequence: uint64(sequence)} // #nosec G115 -- sequence is positive before conversion.
		if !validRecoveryStamp(stamp) {
			return nil, ErrRecoveryConflict
		}
		found = &stamp
	}
	return found, rows.Err()
}

// ApplySQLRecovery replaces revision authority inside the supplied ReplayRelationalImport transaction.
// It does not start, commit, initialize, or activate authority. The coordinator binds the receipt.
// Run this final replacement once after all policy replay, while every import barrier remains closed.
// It may use its own final ordered step. Complete graph and revision validation must precede activation.
func ApplySQLRecovery(ctx context.Context, db *sqlstore.DB, conn *sql.Conn, transition SQLRecoveryTransition) error {
	if _, err := transition.Digest(); err != nil {
		return err
	}
	before, err := readSQLRecovery(ctx, db, conn, true)
	if err != nil {
		return err
	}
	expected := transition.payload.Expected
	if (before == nil) != (expected == nil) || before != nil && *before != *expected {
		return ErrRecoveryConflict
	}
	var result sql.Result
	if before == nil {
		result, err = conn.ExecContext(ctx, db.Bind("INSERT INTO authorization_revision(id,epoch,sequence) VALUES(1,?,1)"), transition.payload.Authority.Epoch)
	} else {
		result, err = conn.ExecContext(ctx, db.Bind("UPDATE authorization_revision SET epoch=?,sequence=1 WHERE id=1 AND epoch=? AND sequence=?"), transition.payload.Authority.Epoch, before.Epoch, int64(before.Sequence)) // #nosec G115 -- readSQLRecovery reads a positive int64 sequence.
	}
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil || count != 1 {
		return ErrRecoveryConflict
	}
	return nil
}
