package sqlstore

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json/v2"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
)

// RecoveryStartupState reports checked native recovery controls. It grants no activation authority.
// Current replay consistency does not establish the originally approved final position.
type RecoveryStartupState struct {
	Activated      bool
	ClaimSHA256    string
	DecisionSHA256 string
	ReceiptSHA256  string
}

// InspectRecoveryStartup reads existing selected SQL state before application effects.
// It creates no database or schema and does not migrate or repair recovery records.
// Missing schema permits first boot only when no retained recovery schema exists.
func InspectRecoveryStartup(ctx context.Context, config Config, deployment string) (state RecoveryStartupState, resultErr error) {
	if ctx == nil {
		return state, ErrClosed
	}
	if err := ctx.Err(); err != nil {
		return state, err
	}
	if err := config.Validate(); err != nil {
		return state, err
	}
	db, err := openRecoveryStartup(config)
	if err != nil || db == nil {
		return state, errors.Join(err, ctx.Err())
	}
	defer func() { resultErr = errors.Join(resultErr, db.Close(), ctx.Err()) }()
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return state, err
	}
	defer func() {
		if err := tx.Rollback(); !errors.Is(err, sql.ErrTxDone) {
			resultErr = errors.Join(resultErr, err)
		}
	}()
	return inspectRecoveryTransaction(ctx, db, tx, deployment)
}

func inspectRecoveryTransaction(ctx context.Context, db *DB, tx *sql.Tx, deployment string) (state RecoveryStartupState, resultErr error) {
	meta, err := recoveryTableExists(ctx, tx, db.dialect, "sqlstore_meta")
	if err != nil {
		return state, err
	}
	if !meta {
		for _, table := range []string{"schema_migrations", "catalog_recovery"} {
			present, err := recoveryTableExists(ctx, tx, db.dialect, table)
			if err != nil || present {
				return state, errors.Join(ErrImportRestricted, err)
			}
		}
		return state, nil
	}
	read := func(name string) (string, error) {
		var value string
		limit := 4096
		if name == relationalActivationCurrent || strings.HasPrefix(name, relationalActivationPrefix) {
			limit = 512
		}
		err := tx.QueryRowContext(ctx, db.Bind("SELECT SUBSTR(value,1,?) FROM sqlstore_meta WHERE name=?"), limit+1, name).Scan(&value)
		if errors.Is(err, sql.ErrNoRows) {
			return "", nil
		}
		if err != nil || len(value) == 0 || len(value) > limit {
			return "", errors.Join(ErrImportRestricted, err)
		}
		return value, nil
	}
	marker, err := read(relationalImportMarker)
	if err != nil || marker != "" {
		return state, errors.Join(ErrImportRestricted, err)
	}
	current, err := read(relationalActivationCurrent)
	if err != nil {
		return state, err
	}
	replay, err := read(relationalReplayCurrent)
	if err != nil {
		return state, err
	}
	if current == "" {
		if replay != "" {
			return state, ErrImportRestricted
		}
		var retained int
		err := tx.QueryRowContext(ctx, db.Bind("SELECT COUNT(*) FROM sqlstore_meta WHERE name LIKE ? OR name LIKE ?"), relationalActivationPrefix+"%", "relational-replay-v1:%").Scan(&retained)
		if err != nil || retained != 0 {
			return state, errors.Join(ErrImportRestricted, err)
		}
		return state, nil
	}
	return inspectRecoveryReceipts(ctx, db, tx, deployment, current, replay, read)
}

func inspectRecoveryReceipts(ctx context.Context, db *DB, tx *sql.Tx, deployment, current, replay string, read func(string) (string, error)) (state RecoveryStartupState, resultErr error) {
	var activation relationalActivationReceipt
	if json.Unmarshal([]byte(current), &activation, json.RejectUnknownMembers(true)) != nil || !validStartupActivationReceipt(activation) || len(current) > 512 ||
		!relationalActivationDigest(activation.ClaimSHA256) || !relationalActivationDigest(activation.DecisionSHA256) {
		return state, ErrImportRestricted
	}
	canonical, err := json.Marshal(activation)
	if err != nil || !bytes.Equal(canonical, []byte(current)) {
		return state, ErrImportRestricted
	}
	history, err := read(relationalActivationPrefix + activation.ClaimSHA256)
	if err != nil || history != current {
		return state, errors.Join(ErrImportRestricted, err)
	}
	position, err := inspectStartupReplay(replay, activation.ClaimSHA256, read)
	if err != nil {
		return state, err
	}
	if activation.Position != nil && *activation.Position != position {
		return state, ErrImportRestricted
	}
	var epoch int64
	var opened int
	var backend, evidence string
	err = tx.QueryRowContext(ctx, db.Bind("SELECT epoch,gate_open,backend_id,evidence FROM catalog_recovery WHERE deployment_id=?"), deployment).Scan(&epoch, &opened, &backend, &evidence)
	if err != nil || epoch <= 0 || opened != 1 || backend == "" || evidence == "" {
		return state, errors.Join(ErrImportRestricted, err)
	}
	return RecoveryStartupState{Activated: true, ClaimSHA256: activation.ClaimSHA256, DecisionSHA256: activation.DecisionSHA256, ReceiptSHA256: relationalReplayDigest([]byte(current))}, nil
}

func openRecoveryStartup(config Config) (*DB, error) {
	if config.Type != TypeSQLite {
		return Open(config)
	}
	if config.SQLite.Path == "" {
		return nil, nil
	}
	info, err := os.Stat(config.SQLite.Path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("SQL recovery preflight requires an existing regular database file")
	}
	dsn := "file:" + url.PathEscape(config.SQLite.Path) + "?mode=ro&_pragma=query_only(1)&_pragma=busy_timeout(50)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	return &DB{DB: db, dialect: TypeSQLite}, nil
}

func recoveryTableExists(ctx context.Context, tx *sql.Tx, dialect, name string) (bool, error) {
	query := "SELECT COUNT(*) FROM sqlite_schema WHERE type='table' AND name=?"
	switch dialect {
	case TypePostgres:
		query = "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema=current_schema() AND table_name=$1"
	case TypeMySQL:
		query = "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema=DATABASE() AND table_name=?"
	}
	var count int
	if err := tx.QueryRowContext(ctx, query, name).Scan(&count); err != nil {
		return false, fmt.Errorf("inspect existing recovery schema: %w", err)
	}
	return count != 0, nil
}

func validStartupActivationReceipt(receipt relationalActivationReceipt) bool {
	switch receipt.Version {
	case 1:
		return receipt.Position == nil
	case 2:
		return receipt.Position != nil && receipt.Position.valid()
	default:
		return false
	}
}

func inspectStartupReplay(replay, claim string, read func(string) (string, error)) (RelationalReplayPosition, error) {
	var position RelationalReplayPosition
	if replay != "" {
		var cursor relationalReplayReceipt
		if json.Unmarshal([]byte(replay), &cursor, json.RejectUnknownMembers(true)) != nil || !cursor.valid() || cursor.ClaimSHA256 != claim {
			return position, ErrImportRestricted
		}
		position = RelationalReplayPosition{Sequence: cursor.Sequence, ReceiptSHA256: relationalReplayDigest([]byte(replay))}
		canonical, err := json.Marshal(cursor)
		if err != nil || string(canonical) != replay {
			return position, ErrImportRestricted
		}
		retained, err := read(cursor.key())
		if err != nil || retained != replay {
			return position, errors.Join(ErrImportRestricted, err)
		}
	}
	return position, nil
}
