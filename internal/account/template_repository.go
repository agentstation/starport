package account

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/agentstation/starport/internal/sqlstore"
)

// TemplateStorageSchemaVersion identifies the only supported template
// record schema.
const TemplateStorageSchemaVersion = 1

// ErrTemplateRepositoryRequired reports a missing relational store.
var ErrTemplateRepositoryRequired = errors.New("account template storage is required")

// TemplateRecord is one versioned template repository value.
type TemplateRecord struct {
	Revision uint64
	Template Template
}

// TemplateRepository is the durable account-template contract. Templates
// are a relational concept, so the repository rides sqlstore rather than
// the key-value store the accounts themselves live in.
type TemplateRepository interface {
	Create(context.Context, Template) (TemplateRecord, error)
	GetByID(context.Context, string) (TemplateRecord, error)
	List(context.Context, int, int) ([]TemplateRecord, error)
	Update(context.Context, Template, uint64) (TemplateRecord, error)
	Delete(context.Context, string, uint64) error
}

type templateRepository struct {
	db  *sqlstore.DB
	now func() time.Time
}

// templateRecord is the stored JSON document. The id and revision also
// live in their own columns so SQL can address and guard them; the record
// column stays the one source of the template's content.
type templateRecord struct {
	SchemaVersion int      `json:"schema_version"`
	Revision      uint64   `json:"revision"`
	Template      Template `json:"template"`
}

// OpenTemplates returns a sqlstore-backed template repository. The caller
// has already migrated the store; this constructor only refuses a nil one.
func OpenTemplates(db *sqlstore.DB) (TemplateRepository, error) {
	if db == nil {
		return nil, ErrTemplateRepositoryRequired
	}
	return &templateRepository{db: db, now: time.Now}, nil
}

func (r *templateRepository) Create(ctx context.Context, value Template) (TemplateRecord, error) {
	stored := templateRecord{
		SchemaVersion: TemplateStorageSchemaVersion,
		Revision:      1,
		Template:      cloneTemplate(value),
	}
	created := r.now().UTC()
	stored.Template.CreatedAt = created
	stored.Template.UpdatedAt = created
	if err := stored.Template.Validate(); err != nil {
		return TemplateRecord{}, err
	}
	data, err := json.Marshal(stored)
	if err != nil {
		return TemplateRecord{}, fmt.Errorf("encode template record: %w", err)
	}
	_, err = r.db.ExecContext(ctx,
		r.db.Bind(`INSERT INTO account_templates (id, revision, record) VALUES (?, ?, ?)`),
		stored.Template.ID, stored.Revision, string(data))
	if err != nil {
		// The insert has one unique constraint, so a failed insert with the
		// row present is the duplicate-create conflict, whatever the
		// dialect's word for it.
		if exists, existsErr := r.exists(ctx, stored.Template.ID); existsErr == nil && exists {
			return TemplateRecord{}, ErrTemplateConflict
		}
		return TemplateRecord{}, fmt.Errorf("create account template: %w", err)
	}
	return TemplateRecord{Revision: stored.Revision, Template: stored.Template}, nil
}

func (r *templateRepository) GetByID(ctx context.Context, id string) (TemplateRecord, error) {
	if id == "" {
		return TemplateRecord{}, ErrMissingID
	}
	var data string
	err := r.db.QueryRowContext(ctx,
		r.db.Bind(`SELECT record FROM account_templates WHERE id = ?`), id,
	).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return TemplateRecord{}, ErrTemplateNotFound
	}
	if err != nil {
		return TemplateRecord{}, fmt.Errorf("get account template: %w", err)
	}
	stored, err := decodeTemplate(data)
	if err != nil {
		return TemplateRecord{}, err
	}
	if stored.Template.ID != id {
		return TemplateRecord{}, fmt.Errorf("%w: template ID does not match its row", ErrCorruptTemplate)
	}
	return TemplateRecord{Revision: stored.Revision, Template: stored.Template}, nil
}

func (r *templateRepository) List(ctx context.Context, limit, offset int) ([]TemplateRecord, error) {
	if limit <= 0 {
		limit = defaultListLimit
	}
	if offset < 0 {
		offset = 0
	}
	rows, err := r.db.QueryContext(ctx,
		r.db.Bind(`SELECT record FROM account_templates ORDER BY id LIMIT ? OFFSET ?`),
		limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list account templates: %w", err)
	}
	defer func() { _ = rows.Close() }()
	records := make([]TemplateRecord, 0)
	for rows.Next() {
		var data string
		if err := rows.Scan(&data); err != nil {
			return nil, fmt.Errorf("read listed account template: %w", err)
		}
		stored, err := decodeTemplate(data)
		if err != nil {
			return nil, err
		}
		records = append(records, TemplateRecord{Revision: stored.Revision, Template: stored.Template})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list account templates: %w", err)
	}
	return records, nil
}

func (r *templateRepository) Update(ctx context.Context, value Template, expectedRevision uint64) (TemplateRecord, error) {
	if err := ValidateID(value.ID); err != nil {
		return TemplateRecord{}, err
	}
	current, err := r.GetByID(ctx, value.ID)
	if err != nil {
		return TemplateRecord{}, err
	}
	if current.Revision != expectedRevision {
		return TemplateRecord{}, ErrTemplateConflict
	}
	updated := templateRecord{
		SchemaVersion: TemplateStorageSchemaVersion,
		Revision:      current.Revision + 1,
		Template:      cloneTemplate(value),
	}
	// Creation time is a property of the record, not of the caller's
	// payload, and the timestamps are stamped before validation so the
	// check reads the record this call actually writes.
	updated.Template.CreatedAt = current.Template.CreatedAt
	updated.Template.UpdatedAt = r.now().UTC()
	if err := updated.Template.Validate(); err != nil {
		return TemplateRecord{}, err
	}
	data, err := json.Marshal(updated)
	if err != nil {
		return TemplateRecord{}, fmt.Errorf("encode template update: %w", err)
	}
	// The revision guard in the WHERE clause is the compare-and-swap: a
	// concurrent writer has moved the revision, the update matches no row,
	// and the caller hears a conflict instead of overwriting the winner.
	result, err := r.db.ExecContext(ctx,
		r.db.Bind(`UPDATE account_templates SET revision = ?, record = ? WHERE id = ? AND revision = ?`),
		updated.Revision, string(data), updated.Template.ID, expectedRevision)
	if err != nil {
		return TemplateRecord{}, fmt.Errorf("update account template: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return TemplateRecord{}, fmt.Errorf("update account template: %w", err)
	}
	if affected == 0 {
		return TemplateRecord{}, ErrTemplateConflict
	}
	return TemplateRecord{Revision: updated.Revision, Template: updated.Template}, nil
}

func (r *templateRepository) Delete(ctx context.Context, id string, expectedRevision uint64) error {
	if id == "" {
		return ErrMissingID
	}
	current, err := r.GetByID(ctx, id)
	if err != nil {
		return err
	}
	if expectedRevision != 0 && current.Revision != expectedRevision {
		return ErrTemplateConflict
	}
	result, err := r.db.ExecContext(ctx,
		r.db.Bind(`DELETE FROM account_templates WHERE id = ? AND revision = ?`),
		id, current.Revision)
	if err != nil {
		return fmt.Errorf("delete account template: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete account template: %w", err)
	}
	if affected == 0 {
		return ErrTemplateConflict
	}
	return nil
}

func (r *templateRepository) exists(ctx context.Context, id string) (bool, error) {
	var count int
	err := r.db.QueryRowContext(ctx,
		r.db.Bind(`SELECT COUNT(*) FROM account_templates WHERE id = ?`), id,
	).Scan(&count)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

func decodeTemplate(data string) (templateRecord, error) {
	var stored templateRecord
	if err := json.Unmarshal([]byte(data), &stored); err != nil {
		return templateRecord{}, fmt.Errorf("%w: %s", ErrCorruptTemplate, err)
	}
	if stored.SchemaVersion != TemplateStorageSchemaVersion {
		return templateRecord{}, fmt.Errorf(
			"%w: unsupported schema version %d",
			ErrCorruptTemplate,
			stored.SchemaVersion,
		)
	}
	if stored.Revision == 0 {
		return templateRecord{}, fmt.Errorf("%w: template revision is zero", ErrCorruptTemplate)
	}
	if err := stored.Template.Validate(); err != nil {
		return templateRecord{}, fmt.Errorf("%w: %s", ErrCorruptTemplate, err)
	}
	return stored, nil
}
