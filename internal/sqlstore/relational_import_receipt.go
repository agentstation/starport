package sqlstore

import (
	"context"
	"database/sql"
	"encoding/json/v2"
	"errors"
	"strings"
)

const relationalImportMarker = "relational-import-v1"

// ErrImportRestricted keeps an imported SQL database unavailable to ordinary startup.
var ErrImportRestricted = errors.New("relational import requires deployment recovery before startup")

// RelationalImportIdentity binds an import to its operation and recovery policy.
// RestrictionID must identify the complete callback policy, including its selected epoch.
type RelationalImportIdentity struct {
	OperationID   string `json:"operation_id"`
	RestrictionID string `json:"restriction_id"`
}

type relationalImportReceipt struct {
	Version  int                      `json:"version"`
	Identity RelationalImportIdentity `json:"identity"`
	Snapshot SQLiteSnapshot           `json:"snapshot"`
}

// ImportRelationalOnce retains a startup barrier in the same transaction as imported records.
// Exact retries validate the source and receipt without repeating restrictions.
// The caller must fence target writers. A receipt does not verify later target mutations.
func (db *DB) ImportRelationalOnce(ctx context.Context, source string, expected SQLiteSnapshot, scratch string, identity RelationalImportIdentity, restrict func(context.Context, *sql.Conn) error) error {
	claim, err := relationalImportClaim(expected, identity)
	if err != nil {
		return err
	}
	return db.importRelationalImage(ctx, source, expected, scratch, restrict, claim)
}

func relationalImportClaim(expected SQLiteSnapshot, identity RelationalImportIdentity) ([]byte, error) {
	for _, value := range []string{identity.OperationID, identity.RestrictionID} {
		if strings.TrimSpace(value) == "" || len(value) > 256 || strings.IndexFunc(value, func(r rune) bool { return r < 32 || r == 127 }) >= 0 {
			return nil, errors.New("relational import requires bounded operation and restriction identifiers")
		}
	}
	return json.Marshal(relationalImportReceipt{Version: 1, Identity: identity, Snapshot: expected})
}

func readRelationalImport(ctx context.Context, source relationalQuery) (string, error) {
	var value string
	query := "SELECT SUBSTR(value,1,4097) FROM sqlstore_meta WHERE name = '" + relationalImportMarker + "'"
	// Constants define the key and bound. No caller text enters this query.
	err := source.QueryRowContext(ctx, query).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if value == "" || len(value) > 4096 {
		return "", ErrImportRestricted
	}
	return value, nil
}

// CheckImportBarrier refuses a partial or unapproved SQL restore after schema initialization.
// Malformed markers also refuse startup. This method never approves or removes a barrier.
func (db *DB) CheckImportBarrier(ctx context.Context) error {
	if db == nil || db.DB == nil {
		return ErrClosed
	}
	marker, err := readRelationalImport(ctx, db)
	if err != nil {
		return err
	}
	if marker != "" {
		return ErrImportRestricted
	}
	return nil
}
