package sqlstore

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json/v2"
	"errors"
	"os"
	"path/filepath"

	"github.com/agentstation/starmap/pkg/productfiles"
)

const relationalPopulatedPrefix = "relational-populated-v1:"

// ErrPopulatedBackend refuses a populated claim on a backend without in-place adoption.
var ErrPopulatedBackend = errors.New("populated relational claim requires PostgreSQL or MySQL")

// ErrPopulatedCensus refuses a populated claim when the live rows differ from the captured census.
var ErrPopulatedCensus = errors.New("live relational rows differ from the captured recovery census")

type relationalPopulatedReceipt struct {
	Version      int    `json:"version"`
	ClaimSHA256  string `json:"claim_sha256"`
	CensusSHA256 string `json:"census_sha256"`
}

// ClaimPopulatedRelational installs the import barrier on a populated PostgreSQL or MySQL database in place.
// The live rows must equal the captured census of expected in every class. The claim copies no rows.
// It retires the current activation and replay rows, keeps every historical receipt, and writes one closure receipt.
// restrict runs in the same transaction. Exact retries verify the closure receipt and do not repeat restrict.
// Scratch must be an existing private directory. The caller must fence every target writer.
func (db *DB) ClaimPopulatedRelational(ctx context.Context, expected SQLiteSnapshot, census RelationalRecoveryCensus, scratch string, identity RelationalImportIdentity, restrict func(context.Context, *sql.Conn) error) (resultErr error) {
	if db == nil || db.DB == nil {
		return ErrClosed
	}
	if db.dialect != TypePostgres && db.dialect != TypeMySQL {
		return ErrPopulatedBackend
	}
	if restrict == nil {
		return errors.New("populated relational claim requires a recovery restriction callback")
	}
	claim, err := inspectionClaim(ctx, expected, identity, RelationalReplayPosition{})
	if err != nil {
		return err
	}
	if !census.valid() {
		return ErrImportRestricted
	}
	key, receipt, err := newRelationalPopulatedReceipt(claim, census)
	if err != nil {
		return err
	}
	owner, err := db.acquireMigrationOwner(ctx)
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, owner.close()) }()
	cleanup, err := prepareMySQLTransfer(ctx, owner)
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, cleanup()) }()
	return db.lockedRelationalTransaction(ctx, owner, func() error {
		if err := validateRelationalSchema(ctx, owner.conn, db.dialect); err != nil {
			return err
		}
		marker, err := readRelationalImport(ctx, owner.conn)
		if err != nil {
			return err
		}
		if marker != "" {
			prior, err := db.readActivationReceipt(ctx, owner.conn, key)
			if err != nil || marker != string(claim) || prior != receipt {
				return errors.Join(ErrImportRestricted, err)
			}
			return nil
		}
		live, err := db.populatedRecoveryCensus(ctx, owner.conn, scratch)
		if err != nil {
			return err
		}
		if live != census {
			return ErrPopulatedCensus
		}
		// The control digest covers both current rows. Census equality is their exact preimage check.
		for _, name := range []string{relationalActivationCurrent, relationalReplayCurrent} {
			if _, err := owner.conn.ExecContext(ctx, db.Bind("DELETE FROM sqlstore_meta WHERE name=?"), name); err != nil {
				return err
			}
		}
		for _, row := range [][2]string{{relationalImportMarker, string(claim)}, {key, receipt}} {
			if _, err := owner.conn.ExecContext(ctx, db.Bind("INSERT INTO sqlstore_meta(name,value) VALUES(?,?)"), row[0], row[1]); err != nil {
				return err
			}
		}
		if err := restrict(ctx, owner.conn); err != nil {
			return err
		}
		if err := db.checkImportPosition(ctx, owner.conn, claim, RelationalReplayPosition{}); err != nil {
			return err
		}
		retained, err := db.readActivationReceipt(ctx, owner.conn, key)
		if err != nil || retained != receipt {
			return errors.Join(ErrImportRestricted, err)
		}
		return nil
	})
}

func newRelationalPopulatedReceipt(claim []byte, census RelationalRecoveryCensus) (string, string, error) {
	encoded, err := json.Marshal(census)
	if err != nil {
		return "", "", err
	}
	claimDigest, censusDigest := sha256.Sum256(claim), sha256.Sum256(encoded)
	receipt := relationalPopulatedReceipt{Version: 1, ClaimSHA256: hex.EncodeToString(claimDigest[:]), CensusSHA256: hex.EncodeToString(censusDigest[:])}
	value, err := json.Marshal(receipt)
	if err != nil {
		return "", "", err
	}
	return relationalPopulatedPrefix + receipt.ClaimSHA256, string(value), nil
}

// populatedRecoveryCensus exports the locked live rows through the portable copy into a private SQLite candidate.
// The census reads only the candidate, so server collation and sort order cannot change the digests.
func (db *DB) populatedRecoveryCensus(ctx context.Context, conn *sql.Conn, scratch string) (_ RelationalRecoveryCensus, resultErr error) {
	parent, err := productfiles.ExistingDirectory(scratch)
	if err != nil {
		return RelationalRecoveryCensus{}, err
	}
	root, err := parent.Open()
	if err != nil {
		return RelationalRecoveryCensus{}, err
	}
	defer func() { resultErr = errors.Join(resultErr, root.Close()) }()
	name := ".relational-census-" + rand.Text()
	if _, err := parent.CreateChild(name); err != nil {
		return RelationalRecoveryCensus{}, err
	}
	identity, err := root.Lstat(name)
	if err != nil {
		return RelationalRecoveryCensus{}, err
	}
	defer func() {
		current, err := root.Lstat(name)
		if err == nil {
			if !os.SameFile(identity, current) {
				err = errors.New("relational census directory identity changed")
			} else {
				err = errors.Join(root.RemoveAll(name), productfiles.SyncDirectory(root))
			}
		}
		resultErr = errors.Join(resultErr, err)
	}()
	file, err := root.OpenFile(filepath.Join(name, "starport.db"), os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
	if err != nil {
		return RelationalRecoveryCensus{}, err
	}
	if err := file.Close(); err != nil {
		return RelationalRecoveryCensus{}, err
	}
	candidate, err := Open(Config{Type: TypeSQLite, SQLite: SQLiteConfig{Path: filepath.Join(scratch, name, "starport.db")}})
	if err != nil {
		return RelationalRecoveryCensus{}, err
	}
	defer func() { resultErr = errors.Join(resultErr, candidate.Close()) }()
	if err := candidate.Migrate(ctx); err != nil {
		return RelationalRecoveryCensus{}, err
	}
	target, err := candidate.BeginTx(ctx, nil)
	if err != nil {
		return RelationalRecoveryCensus{}, err
	}
	defer func() { _ = target.Rollback() }()
	if err := copyRelationalImage(ctx, conn, db.dialect, target); err != nil {
		return RelationalRecoveryCensus{}, err
	}
	return sqliteRecoveryCensus(ctx, target)
}
