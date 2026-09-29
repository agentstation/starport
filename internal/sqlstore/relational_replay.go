package sqlstore

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json/v2"
	"errors"
	"strconv"
)

const relationalReplayCurrent = "relational-replay-current-v1"

// RelationalReplayStep binds an ordered domain transition to independent evidence.
// The domain owner derives TransitionSHA256 from its complete canonical typed input.
// A digest binds bytes. It does not establish provenance or complete history.
type RelationalReplayStep struct {
	Sequence         int64  `json:"sequence"`
	PreviousSHA256   string `json:"previous_sha256"`
	EvidenceSHA256   string `json:"evidence_sha256"`
	TransitionSHA256 string `json:"transition_sha256"`
}

type relationalReplayReceipt struct {
	Version     int    `json:"version"`
	ClaimSHA256 string `json:"claim_sha256"`
	RelationalReplayStep
}

func (r relationalReplayReceipt) valid() bool {
	return r.Version == 1 && relationalActivationDigest(r.ClaimSHA256) && r.Sequence > 0 &&
		(r.Sequence == 1 && r.PreviousSHA256 == "" || r.Sequence > 1 && relationalActivationDigest(r.PreviousSHA256)) &&
		relationalActivationDigest(r.EvidenceSHA256) && relationalActivationDigest(r.TransitionSHA256)
}

func (r relationalReplayReceipt) key() string {
	return "relational-replay-v1:" + r.ClaimSHA256 + ":" + strconv.FormatInt(r.Sequence, 10)
}

func relationalReplayDigest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// ReplayRelationalImport commits an ordered domain transition and its receipt atomically.
// apply must use conn without committing, changing schema, or touching recovery controls.
// The coordinator must fence writers and validate the final domain state before activation.
// Exact retries return their original receipt without repeating earlier mutations.
func (db *DB) ReplayRelationalImport(ctx context.Context, snapshot SQLiteSnapshot, identity RelationalImportIdentity, step RelationalReplayStep, apply func(context.Context, *sql.Conn) error) (digest string, resultErr error) {
	if db == nil || db.DB == nil || ctx == nil {
		return "", ErrClosed
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if apply == nil || !relationalActivationDigest(snapshot.SHA256) || snapshot.Format != sqliteSnapshotFormat || snapshot.Size <= 0 {
		return "", ErrImportRestricted
	}
	claim, err := relationalImportClaim(snapshot, identity)
	if err != nil {
		return "", err
	}
	receipt := relationalReplayReceipt{Version: 1, ClaimSHA256: relationalReplayDigest(claim), RelationalReplayStep: step}
	if !receipt.valid() {
		return "", ErrImportRestricted
	}
	encoded, err := json.Marshal(receipt)
	if err != nil {
		return "", err
	}
	owner, err := db.acquireMigrationOwner(ctx)
	if err != nil {
		return "", err
	}
	defer func() { resultErr = errors.Join(resultErr, owner.close()) }()
	err = owner.transaction(ctx, func() error {
		if err := db.verifyReplayBarrier(ctx, owner.conn, claim); err != nil {
			return err
		}
		current, currentBytes, err := db.readReplayCursor(ctx, owner.conn, receipt.ClaimSHA256)
		if err != nil {
			return err
		}
		prior, err := db.readReplayValue(ctx, owner.conn, receipt.key())
		if err != nil {
			return err
		}
		if prior != "" {
			if prior != string(encoded) || current.Sequence < receipt.Sequence {
				return ErrImportRestricted
			}
			return nil
		}
		if current.Sequence != receipt.Sequence-1 || receipt.Sequence > 1 && relationalReplayDigest([]byte(currentBytes)) != receipt.PreviousSHA256 {
			return ErrImportRestricted
		}
		if err := apply(ctx, owner.conn); err != nil {
			return err
		}
		if err := db.verifyReplayBarrier(ctx, owner.conn, claim); err != nil {
			return err
		}
		_, retained, err := db.readReplayCursor(ctx, owner.conn, receipt.ClaimSHA256)
		if err != nil || retained != currentBytes {
			return errors.Join(ErrImportRestricted, err)
		}
		if _, err := owner.conn.ExecContext(ctx, db.Bind("INSERT INTO sqlstore_meta(name,value) VALUES(?,?)"), receipt.key(), string(encoded)); err != nil {
			return err
		}
		if currentBytes == "" {
			_, err = owner.conn.ExecContext(ctx, db.Bind("INSERT INTO sqlstore_meta(name,value) VALUES(?,?)"), relationalReplayCurrent, string(encoded))
		} else {
			_, err = owner.conn.ExecContext(ctx, db.Bind("UPDATE sqlstore_meta SET value=? WHERE name=?"), string(encoded), relationalReplayCurrent)
		}
		return err
	})
	if err != nil {
		return "", err
	}
	return relationalReplayDigest(encoded), nil
}

func (db *DB) verifyReplayBarrier(ctx context.Context, conn *sql.Conn, claim []byte) error {
	marker, err := readRelationalImport(ctx, conn)
	if err != nil || marker != string(claim) {
		return errors.Join(ErrImportRestricted, err)
	}
	active, err := db.readActivationReceipt(ctx, conn, relationalActivationCurrent)
	if err != nil || active != "" {
		return errors.Join(ErrImportRestricted, err)
	}
	return nil
}

func (db *DB) readReplayCursor(ctx context.Context, conn *sql.Conn, claim string) (relationalReplayReceipt, string, error) {
	var receipt relationalReplayReceipt
	data, err := db.readReplayValue(ctx, conn, relationalReplayCurrent)
	if err != nil || data == "" {
		return receipt, data, err
	}
	if json.Unmarshal([]byte(data), &receipt, json.RejectUnknownMembers(true)) != nil || !receipt.valid() || receipt.ClaimSHA256 != claim {
		return receipt, "", ErrImportRestricted
	}
	canonical, err := json.Marshal(receipt)
	if err != nil || string(canonical) != data {
		return receipt, "", ErrImportRestricted
	}
	history, err := db.readReplayValue(ctx, conn, receipt.key())
	if err != nil || history != data {
		return receipt, "", errors.Join(ErrImportRestricted, err)
	}
	return receipt, data, nil
}

func (db *DB) readReplayValue(ctx context.Context, conn *sql.Conn, name string) (string, error) {
	var value string
	err := conn.QueryRowContext(ctx, db.Bind("SELECT SUBSTR(value,1,1025) FROM sqlstore_meta WHERE name=?"), name).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if len(value) == 0 || len(value) > 1024 {
		return "", ErrImportRestricted
	}
	return value, nil
}
