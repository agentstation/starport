package revision

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"math"

	"github.com/agentstation/starport/internal/sqlstore"
)

// SQL commits identity changes and their authority revision in one transaction.
type SQL struct {
	db    *sqlstore.DB
	begin func() func()
}

// NewSQL binds a migrated database without reading or creating state.
func NewSQL(db *sqlstore.DB, begin func() func()) *SQL { return &SQL{db: db, begin: begin} }

// Read returns the relational authority's durable epoch and sequence.
func (s *SQL) Read(ctx context.Context) (Stamp, error) {
	var stamp Stamp
	var epoch sql.NullString
	err := s.db.QueryRowContext(ctx, boundedEpochQuery(s.db.Dialect(), true)).Scan(&epoch, &stamp.Sequence)
	if err != nil {
		return Stamp{}, err
	}
	stamp.Epoch = epoch.String
	if !epoch.Valid || stamp.Epoch == "" || len(stamp.Epoch) > 256 || stamp.Sequence == 0 || stamp.Sequence > math.MaxInt64 {
		return Stamp{}, ErrCorrupt
	}
	return stamp, nil
}

// Initialize creates the relational authority marker once.
func (s *SQL) Initialize(ctx context.Context) (Stamp, error) {
	stamp, err := s.Read(ctx)
	if !errors.Is(err, sql.ErrNoRows) {
		return stamp, err
	}
	stamp = Stamp{Epoch: rand.Text(), Sequence: 1}
	_, err = s.db.ExecContext(ctx, s.db.Bind(`INSERT INTO authorization_revision (id, epoch, sequence) VALUES (1, ?, 1)`), stamp.Epoch)
	if err != nil {
		// A concurrent initializer may own the row. Only a valid durable read wins.
		if existing, readErr := s.Read(ctx); readErr == nil {
			return existing, nil
		}
		return Stamp{}, err
	}
	return stamp, nil
}

// Apply serializes identity mutations through the revision row.
// Callback failure rolls back the marker and every policy change.
func (s *SQL) Apply(ctx context.Context, mutate func(*sql.Tx) error) error {
	if s.begin != nil {
		finish := s.begin()
		defer finish()
	}
	if _, err := s.Initialize(ctx); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	result, err := tx.ExecContext(ctx, s.db.Bind(`UPDATE authorization_revision SET sequence = sequence + 1 WHERE id = 1 AND sequence > 0 AND sequence < ?`), int64(math.MaxInt64))
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed != 1 {
		return ErrCorrupt
	}
	var epoch sql.NullString
	if err := tx.QueryRowContext(ctx, boundedEpochQuery(s.db.Dialect(), false)).Scan(&epoch); err != nil {
		return err
	}
	if !epoch.Valid || epoch.String == "" || len(epoch.String) > 256 {
		return ErrCorrupt
	}
	if err := mutate(tx); err != nil {
		return err
	}
	return tx.Commit()
}

// boundedEpochQuery returns complete valid-size epochs and suppresses oversized ones.
func boundedEpochQuery(dialect string, sequence bool) string {
	size := "OCTET_LENGTH(epoch)"
	if dialect == sqlstore.TypeSQLite {
		size = "LENGTH(CAST(epoch AS BLOB))"
	}
	projection := "CASE WHEN " + size + " > 256 THEN NULL ELSE epoch END"
	if sequence {
		projection += ", sequence"
	}
	return "SELECT " + projection + " FROM authorization_revision WHERE id = 1"
}
