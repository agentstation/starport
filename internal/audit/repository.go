package audit

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/agentstation/starport/internal/sqlstore"
)

const (
	// DefaultRetention bounds how far back the trail reaches: 400 days, past
	// an annual compliance review with margin for the review to run late.
	DefaultRetention = 400 * 24 * time.Hour
	// MaxListLimit caps one page. It matches the usage listing's cap, so
	// every admin listing honors the same ceiling.
	MaxListLimit = 1000
	// defaultListLimit is the page size a query without a limit gets.
	defaultListLimit = 100
)

// ErrStoreRequired reports a missing relational store.
var ErrStoreRequired = errors.New("audit storage is required")

// ErrInvalidQuery reports a query the repository refuses to run: a bad
// cursor or a limit outside its bounds.
var ErrInvalidQuery = errors.New("invalid audit query")

// Query filters one page of the trail. Zero values place no filter.
type Query struct {
	// Action keeps only records with this exact action.
	Action string
	// Actor keeps only records with this exact actor.
	Actor string
	// Since and Until bound the time window, inclusive of Since and
	// exclusive of Until.
	Since time.Time
	Until time.Time
	// Limit caps the page, defaulting to defaultListLimit and refusing
	// values above MaxListLimit.
	Limit int
	// Cursor resumes a walk from a previous page's NextCursor.
	Cursor string
}

// Page is one bounded read of the trail, newest first.
type Page struct {
	Records []Record
	// NextCursor resumes the walk, or is empty on the last page.
	NextCursor string
}

// Repository is the sqlstore-backed trail. It writes one row per mutation
// and prunes rows past the retention window on each write.
type Repository struct {
	db        *sqlstore.DB
	retention time.Duration
	now       func() time.Time
}

// Open returns a repository over an already-migrated store. A retention at
// or below zero selects the default window.
func Open(db *sqlstore.DB, retention time.Duration) (*Repository, error) {
	if db == nil {
		return nil, ErrStoreRequired
	}
	if retention <= 0 {
		retention = DefaultRetention
	}
	return &Repository{db: db, retention: retention, now: time.Now}, nil
}

// Record appends one audit record and prunes entries past the retention
// window. A zero time takes the clock's now.
func (r *Repository) Record(ctx context.Context, record Record) error {
	occurredAt := record.Time
	if occurredAt.IsZero() {
		occurredAt = r.now()
	}
	if _, err := r.db.ExecContext(ctx,
		r.db.Bind(`INSERT INTO audit_log (occurred_at, actor, action, subject, outcome, request_id) VALUES (?, ?, ?, ?, ?, ?)`),
		occurredAt.UTC().Format(time.RFC3339Nano),
		record.Actor, record.Action, record.Subject, record.Outcome, record.RequestID); err != nil {
		return fmt.Errorf("record audit entry: %w", err)
	}
	// RFC 3339 strings in UTC order lexicographically, so the cutoff can be
	// compared as text the way the rows are stored.
	cutoff := r.now().UTC().Add(-r.retention).Format(time.RFC3339Nano)
	if _, err := r.db.ExecContext(ctx,
		r.db.Bind(`DELETE FROM audit_log WHERE occurred_at < ?`), cutoff); err != nil {
		return fmt.Errorf("prune audit log: %w", err)
	}
	return nil
}

// List returns one page of the trail, newest first, honoring the query's
// filters and cursor.
func (r *Repository) List(ctx context.Context, query Query) (Page, error) {
	limit := query.Limit
	if limit <= 0 {
		limit = defaultListLimit
	}
	if limit > MaxListLimit {
		return Page{}, fmt.Errorf("%w: limit must not exceed %d", ErrInvalidQuery, MaxListLimit)
	}

	conditions := []string{"1=1"}
	arguments := []any{}
	if query.Action != "" {
		conditions = append(conditions, "action = ?")
		arguments = append(arguments, query.Action)
	}
	if query.Actor != "" {
		conditions = append(conditions, "actor = ?")
		arguments = append(arguments, query.Actor)
	}
	if !query.Since.IsZero() {
		conditions = append(conditions, "occurred_at >= ?")
		arguments = append(arguments, query.Since.UTC().Format(time.RFC3339Nano))
	}
	if !query.Until.IsZero() {
		conditions = append(conditions, "occurred_at < ?")
		arguments = append(arguments, query.Until.UTC().Format(time.RFC3339Nano))
	}
	if query.Cursor != "" {
		cursor, err := strconv.ParseInt(query.Cursor, 10, 64)
		if err != nil || cursor <= 0 {
			return Page{}, fmt.Errorf("%w: bad cursor", ErrInvalidQuery)
		}
		conditions = append(conditions, "id < ?")
		arguments = append(arguments, cursor)
	}
	// One extra row separates "page full" from "more pages exist".
	arguments = append(arguments, limit+1)

	statement := `SELECT id, occurred_at, actor, action, subject, outcome, request_id FROM audit_log WHERE ` +
		strings.Join(conditions, " AND ") + ` ORDER BY id DESC LIMIT ?`
	rows, err := r.db.QueryContext(ctx, r.db.Bind(statement), arguments...)
	if err != nil {
		return Page{}, fmt.Errorf("read audit log: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var records []Record
	for rows.Next() {
		var record Record
		var occurred string
		if err := rows.Scan(&record.ID, &occurred, &record.Actor,
			&record.Action, &record.Subject, &record.Outcome, &record.RequestID); err != nil {
			return Page{}, fmt.Errorf("scan audit entry: %w", err)
		}
		occurredAt, err := time.Parse(time.RFC3339Nano, occurred)
		if err != nil {
			return Page{}, fmt.Errorf("parse audit entry time: %w", err)
		}
		record.Time = occurredAt.UTC()
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return Page{}, fmt.Errorf("read audit log: %w", err)
	}

	page := Page{Records: records}
	if len(records) > limit {
		page.Records = records[:limit]
		page.NextCursor = strconv.FormatInt(page.Records[limit-1].ID, 10)
	}
	return page, nil
}
