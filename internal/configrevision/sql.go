package configrevision

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json/v2"
	"errors"
	"fmt"
	"maps"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/agentstation/starport/internal/audit"
	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/deployment"
	"github.com/agentstation/starport/internal/sqlstore"
)

// Store reads and writes the shared configuration of one deployment.
type Store struct {
	db           *sqlstore.DB
	trail        *audit.Repository
	sealer       Sealer
	deploymentID string
	namespace    string
	now          func() time.Time
}

// maxNamespaceBytes bounds the namespace column. The key prefix of a 256 byte
// deployment ID uses 355 bytes.
const maxNamespaceBytes = 512

// New binds a migrated database to one deployment and namespace. It reads
// and writes nothing. A nil sealer refuses source credentials.
func New(db *sqlstore.DB, trail *audit.Repository, sealer Sealer, deploymentID, namespace string) (*Store, error) {
	if db == nil || trail == nil {
		return nil, errors.New("shared configuration store requires a relational store and an audit trail")
	}
	if err := deployment.ValidateID(deploymentID); err != nil {
		return nil, err
	}
	if namespace == "" || len(namespace) > maxNamespaceBytes || strings.TrimSpace(namespace) != namespace || !utf8.ValidString(namespace) || strings.ContainsFunc(namespace, unicode.IsControl) {
		return nil, fmt.Errorf("shared configuration namespace requires 1 to %d UTF-8 bytes without surrounding whitespace or control characters", maxNamespaceBytes)
	}
	return &Store{db: db, trail: trail, sealer: sealer, deploymentID: deploymentID, namespace: namespace, now: time.Now}, nil
}

// Head returns the selected revision of the deployment. An absent head is
// ErrNotInitialized; when other deployments hold heads, it is a
// *NotInitializedError that names them. A store failure is an
// *UnavailableError. A head that names a different namespace is a
// *config.AuthorityMismatchError with the stored namespace and deployment ID.
func (s *Store) Head(ctx context.Context) (Head, error) {
	revision, err := s.read(ctx, false)
	return revision.Head, err
}

// Current returns the selected revision with its unsealed values.
func (s *Store) Current(ctx context.Context) (Revision, error) {
	return s.read(ctx, true)
}

// Preview validates a seed and returns the head that Initialize would write.
// It writes nothing.
func (s *Store) Preview(ctx context.Context, seed map[string]string) (Head, error) {
	if err := config.ValidateSharedValues(seed); err != nil {
		return Head{}, err
	}
	head, err := s.Head(ctx)
	if err == nil {
		return Head{}, &InitializedError{Head: head}
	}
	if !errors.Is(err, ErrNotInitialized) {
		return Head{}, err
	}
	return Head{
		DeploymentID: s.deploymentID, Namespace: s.namespace, Sequence: 1,
		Authority: AuthorityShared, Checksum: config.SharedValuesChecksum(seed),
	}, nil
}

// Initialize writes the head, revision 1, and its audit record in one
// transaction. A second initializer receives an *InitializedError with the
// stored head. A repeated operation ID returns the original revision.
func (s *Store) Initialize(ctx context.Context, seed map[string]string, actor, operationID string) (Revision, error) {
	if err := s.validateWrite(seed, actor, operationID); err != nil {
		return Revision{}, err
	}
	target := revisionTarget{sequence: 1, authority: AuthorityShared, checksum: config.SharedValuesChecksum(seed)}
	if revision, found, err := s.replay(ctx, operationID, target); found || err != nil {
		return revision, err
	}
	head, err := s.Head(ctx)
	if err == nil {
		return Revision{}, &InitializedError{Head: head}
	}
	if !errors.Is(err, ErrNotInitialized) {
		return Revision{}, err
	}
	revision := s.newRevision(target, seed, actor, operationID, "")
	err = s.write(ctx, ActionInitialize, nil, revision, func(tx *sql.Tx) (bool, error) {
		_, err := tx.ExecContext(ctx, s.db.Bind(`INSERT INTO deployment_configuration_head (deployment_id, namespace, sequence, revision_id) VALUES (?, ?, 1, ?)`),
			s.deploymentID, s.namespace, revision.RevisionID)
		return err == nil, err
	})
	if err == nil {
		return revision.Revision, nil
	}
	// A concurrent initializer can own the head. Only a durable read names it.
	if replayed, found, replayErr := s.replay(ctx, operationID, target); found || replayErr != nil {
		return replayed, replayErr
	}
	if head, readErr := s.Head(ctx); readErr == nil {
		return Revision{}, &InitializedError{Head: head}
	}
	return Revision{}, err
}

// Commit writes revision expectedSequence+1 and moves the head in one
// transaction with its audit record. A head that moved is a
// *StaleRevisionError. A repeated operation ID returns the original revision.
func (s *Store) Commit(ctx context.Context, expectedSequence int64, values map[string]string, actor, operationID string) (Revision, error) {
	return s.commit(ctx, expectedSequence, AuthorityShared, values, actor, operationID, nil)
}

// SaveFields commits a field save as revision expectedSequence+1. It is
// Commit with the field-save audit action and an audit subject that names the
// changed setting keys, never their values.
func (s *Store) SaveFields(ctx context.Context, expectedSequence int64, values map[string]string, settings []string, actor, operationID string) (Revision, error) {
	if settings == nil {
		settings = []string{}
	}
	return s.commit(ctx, expectedSequence, AuthorityShared, values, actor, operationID, settings)
}

// Migrate records an authority switch as a revision. To shared, it writes
// revision 1 from the seed, or a shared revision after a local one. To local,
// it records the final applied values as a local revision, which releases the
// shared authority.
func (s *Store) Migrate(ctx context.Context, target string, seed map[string]string, actor, operationID string) (Revision, error) {
	if err := s.validateWrite(seed, actor, operationID); err != nil {
		return Revision{}, err
	}
	if revision, found, err := s.operation(ctx, operationID, false); err != nil || found {
		if err == nil && (revision.Authority != target || (target == AuthorityShared && revision.Checksum != config.SharedValuesChecksum(seed))) {
			err = ErrOperationConflict
		}
		return revision, err
	}
	switch target {
	case AuthorityShared:
		head, err := s.Head(ctx)
		if errors.Is(err, ErrNotInitialized) {
			return s.Initialize(ctx, seed, actor, operationID)
		}
		if err != nil {
			return Revision{}, err
		}
		if head.Authority == AuthorityShared {
			return Revision{}, fmt.Errorf("deployment already uses shared configuration at revision %d", head.Sequence)
		}
		return s.commit(ctx, head.Sequence, AuthorityShared, seed, actor, operationID, nil)
	case AuthorityLocal:
		current, err := s.Current(ctx)
		if err != nil {
			return Revision{}, err
		}
		if current.Authority == AuthorityLocal {
			return Revision{}, fmt.Errorf("deployment already released shared configuration at revision %d", current.Sequence)
		}
		return s.commit(ctx, current.Sequence, AuthorityLocal, current.Values, actor, operationID, nil)
	default:
		return Revision{}, fmt.Errorf("migration target %q is not shared or local", target)
	}
}

// commit writes revision expected+1. Nil settings record a commit. Non-nil
// settings record a field save that names them.
func (s *Store) commit(ctx context.Context, expected int64, authority string, values map[string]string, actor, operationID string, settings []string) (Revision, error) {
	if err := s.validateWrite(values, actor, operationID); err != nil {
		return Revision{}, err
	}
	if expected <= 0 {
		return Revision{}, errors.New("commit requires the current positive revision sequence")
	}
	target := revisionTarget{sequence: expected + 1, authority: authority, checksum: config.SharedValuesChecksum(values)}
	if revision, found, err := s.replay(ctx, operationID, target); found || err != nil {
		return revision, err
	}
	head, err := s.Head(ctx)
	if err != nil {
		return Revision{}, err
	}
	if head.Sequence != expected {
		return Revision{}, &StaleRevisionError{Expected: expected, Current: head.Sequence}
	}
	revision := s.newRevision(target, values, actor, operationID, head.RevisionID)
	action := ActionCommit
	if settings != nil {
		action = config.ActionSave
	}
	err = s.write(ctx, action, settings, revision, func(tx *sql.Tx) (bool, error) {
		result, err := tx.ExecContext(ctx, s.db.Bind(`UPDATE deployment_configuration_head SET sequence = ?, revision_id = ? WHERE deployment_id = ? AND sequence = ?`),
			target.sequence, revision.RevisionID, s.deploymentID, expected)
		if err != nil {
			return false, err
		}
		changed, err := result.RowsAffected()
		return changed == 1, err
	})
	if err == nil {
		return revision.Revision, nil
	}
	if errors.Is(err, errHeadMoved) {
		current := int64(0)
		if head, readErr := s.Head(ctx); readErr == nil {
			current = head.Sequence
		}
		return Revision{}, &StaleRevisionError{Expected: expected, Current: current}
	}
	if replayed, found, replayErr := s.replay(ctx, operationID, target); found || replayErr != nil {
		return replayed, replayErr
	}
	return Revision{}, err
}

// errHeadMoved reports a head update that matched no row.
var errHeadMoved = errors.New("shared configuration head moved")

// write runs the head mutation, the revision insert, and the audit record in
// one transaction. moveHead reports false when the head did not move. The
// audit subject is the revision ID, with the setting keys of a field save.
func (s *Store) write(ctx context.Context, action string, settings []string, revision writtenRevision, moveHead func(*sql.Tx) (bool, error)) error {
	record, err := s.encode(revision.Authority, revision.Values)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return &UnavailableError{Err: err}
	}
	defer func() { _ = tx.Rollback() }()
	moved, err := moveHead(tx)
	if err != nil {
		return fmt.Errorf("write shared configuration head: %w", err)
	}
	if !moved {
		return errHeadMoved
	}
	if _, err := tx.ExecContext(ctx, s.db.Bind(`INSERT INTO deployment_configuration_revisions (revision_id, deployment_id, sequence, predecessor, operation_id, actor, checksum, record, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`),
		revision.RevisionID, s.deploymentID, revision.Sequence, revision.predecessor, revision.OperationID, revision.Actor, revision.Checksum, record, revision.CreatedAt); err != nil {
		return fmt.Errorf("write shared configuration revision: %w", err)
	}
	subject := revision.RevisionID
	if settings != nil {
		subject = config.AuditSubject(revision.RevisionID, settings)
	}
	if err := s.trail.RecordTx(ctx, tx, audit.Record{
		Actor: revision.Actor, Action: action, Subject: subject, Outcome: audit.OutcomeOK,
	}); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit shared configuration revision: %w", err)
	}
	return nil
}

// revisionTarget identifies what one operation ID committed.
type revisionTarget struct {
	sequence  int64
	authority string
	checksum  string
}

func (s *Store) newRevision(target revisionTarget, values map[string]string, actor, operationID, predecessor string) writtenRevision {
	return writtenRevision{
		Revision: Revision{
			Head: Head{
				DeploymentID: s.deploymentID, Namespace: s.namespace, Sequence: target.sequence,
				RevisionID: rand.Text(), Authority: target.authority, Checksum: target.checksum,
			},
			Values: maps.Clone(values), OperationID: operationID, Actor: actor,
			CreatedAt: s.now().UTC().Format(time.RFC3339Nano),
		},
		predecessor: predecessor,
	}
}

// writtenRevision is a revision with the predecessor that its row records.
type writtenRevision struct {
	Revision
	predecessor string
}

// replay returns the revision of a repeated operation ID. A different target
// is ErrOperationConflict.
func (s *Store) replay(ctx context.Context, operationID string, target revisionTarget) (Revision, bool, error) {
	revision, found, err := s.operation(ctx, operationID, false)
	if err != nil || !found {
		return Revision{}, false, err
	}
	if revision.Sequence != target.sequence || revision.Authority != target.authority || revision.Checksum != target.checksum {
		return Revision{}, true, ErrOperationConflict
	}
	return revision, true, nil
}

// Operation returns the revision that an operation ID committed, without its
// values. It reports false when the ID committed nothing.
func (s *Store) Operation(ctx context.Context, operationID string) (Revision, bool, error) {
	if err := validIdentifier("operation ID", operationID); err != nil {
		return Revision{}, false, err
	}
	return s.operation(ctx, operationID, false)
}

// OperationRevision returns the revision that an operation ID committed, with
// its unsealed values. A field save compares the values to tell an exact
// retry from a reused operation ID, because the checksum omits credentials.
func (s *Store) OperationRevision(ctx context.Context, operationID string) (Revision, bool, error) {
	if err := validIdentifier("operation ID", operationID); err != nil {
		return Revision{}, false, err
	}
	return s.operation(ctx, operationID, true)
}

// CheckSchema refuses a store behind the schema of this binary or behind an
// open import barrier. It changes nothing, so a field save never migrates.
func (s *Store) CheckSchema(ctx context.Context) error {
	if err := s.db.CheckSchemaCurrent(ctx); err != nil {
		return err
	}
	return s.db.CheckImportBarrier(ctx)
}

// RevisionAt returns the revision at one sequence of the deployment, with
// its unsealed values. It reports false when no revision has that sequence.
func (s *Store) RevisionAt(ctx context.Context, sequence int64) (Revision, bool, error) {
	revision := Revision{Head: Head{DeploymentID: s.deploymentID, Namespace: s.namespace, Sequence: sequence}}
	var record string
	err := s.db.QueryRowContext(ctx, s.db.Bind(`SELECT revision_id, operation_id, actor, checksum, record, created_at FROM deployment_configuration_revisions WHERE deployment_id = ? AND sequence = ?`), s.deploymentID, sequence).
		Scan(&revision.RevisionID, &revision.OperationID, &revision.Actor, &revision.Checksum, &record, &revision.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Revision{}, false, nil
	}
	if err != nil {
		return Revision{}, false, &UnavailableError{Err: err}
	}
	if err := s.decode(&revision, record, true); err != nil {
		return Revision{}, true, err
	}
	return revision, true, nil
}

func (s *Store) operation(ctx context.Context, operationID string, unseal bool) (Revision, bool, error) {
	var revision Revision
	var record string
	err := s.db.QueryRowContext(ctx, s.db.Bind(`SELECT revision_id, deployment_id, sequence, operation_id, actor, checksum, record, created_at FROM deployment_configuration_revisions WHERE operation_id = ?`), operationID).
		Scan(&revision.RevisionID, &revision.DeploymentID, &revision.Sequence, &revision.OperationID, &revision.Actor, &revision.Checksum, &record, &revision.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Revision{}, false, nil
	}
	if err != nil {
		return Revision{}, false, &UnavailableError{Err: err}
	}
	if revision.DeploymentID != s.deploymentID {
		return Revision{}, true, ErrOperationConflict
	}
	revision.Namespace = s.namespace
	if err := s.decode(&revision, record, unseal); err != nil {
		return Revision{}, true, err
	}
	return revision, true, nil
}

func (s *Store) read(ctx context.Context, unseal bool) (Revision, error) {
	selected := Head{DeploymentID: s.deploymentID}
	err := s.db.QueryRowContext(ctx, s.db.Bind(`SELECT namespace, sequence, revision_id FROM deployment_configuration_head WHERE deployment_id = ?`), s.deploymentID).
		Scan(&selected.Namespace, &selected.Sequence, &selected.RevisionID)
	if errors.Is(err, sql.ErrNoRows) {
		return Revision{}, s.notInitialized(ctx)
	}
	if err != nil {
		return Revision{}, &UnavailableError{Err: err}
	}
	if selected.Namespace != s.namespace {
		return Revision{}, &config.AuthorityMismatchError{Setting: config.NamespaceSetting, Configured: s.namespace, Stored: selected.Namespace, DeploymentID: selected.DeploymentID}
	}
	revision := Revision{Head: selected}
	var deploymentID, record string
	var sequence int64
	// Revisions are append-only, so the row that the head named stays valid.
	err = s.db.QueryRowContext(ctx, s.db.Bind(`SELECT deployment_id, sequence, operation_id, actor, checksum, record, created_at FROM deployment_configuration_revisions WHERE revision_id = ?`), selected.RevisionID).
		Scan(&deploymentID, &sequence, &revision.OperationID, &revision.Actor, &revision.Checksum, &record, &revision.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Revision{}, ErrCorrupt
	}
	if err != nil {
		return Revision{}, &UnavailableError{Err: err}
	}
	if deploymentID != s.deploymentID || sequence != selected.Sequence || selected.Sequence <= 0 {
		return Revision{}, ErrCorrupt
	}
	if err := s.decode(&revision, record, unseal); err != nil {
		return Revision{}, err
	}
	return revision, nil
}

// notInitialized reports an absent head. It names the deployment IDs of the
// other stored heads.
func (s *Store) notInitialized(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, s.db.Bind(`SELECT deployment_id FROM deployment_configuration_head WHERE deployment_id <> ? ORDER BY deployment_id`), s.deploymentID)
	if err != nil {
		return &UnavailableError{Err: err}
	}
	defer func() { _ = rows.Close() }()
	absent := &NotInitializedError{DeploymentID: s.deploymentID}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return &UnavailableError{Err: err}
		}
		if len(absent.Stored) < maxListedDeployments {
			absent.Stored = append(absent.Stored, id)
		}
		absent.Others++
	}
	if err := rows.Err(); err != nil {
		return &UnavailableError{Err: err}
	}
	if absent.Others == 0 {
		return ErrNotInitialized
	}
	return absent
}

// storedRecord is the revision record. Values holds deployment-scope settings
// without credentials. Sealed holds each source credential encrypted.
type storedRecord struct {
	Authority string            `json:"authority"`
	Values    map[string]string `json:"values"`
	Sealed    map[string]string `json:"sealed,omitempty"`
}

func (s *Store) encode(authority string, values map[string]string) (string, error) {
	record := storedRecord{Authority: authority, Values: map[string]string{}}
	for name, value := range values {
		if !config.SensitiveSharedSetting(name) {
			record.Values[name] = value
			continue
		}
		if record.Sealed == nil {
			record.Sealed = map[string]string{}
		}
		if value == "" {
			record.Sealed[name] = ""
			continue
		}
		if s.sealer == nil {
			return "", fmt.Errorf("shared configuration revision cannot store %s without STARPORT_SECURITY_MASTER_KEY", name)
		}
		sealed, err := s.sealer.EncryptCredential(value)
		if err != nil {
			return "", fmt.Errorf("seal %s: %w", name, err)
		}
		record.Sealed[name] = sealed
	}
	data, err := json.Marshal(record, json.Deterministic(true))
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func (s *Store) decode(revision *Revision, data string, unseal bool) error {
	var record storedRecord
	if err := json.Unmarshal([]byte(data), &record, json.RejectUnknownMembers(true)); err != nil {
		return fmt.Errorf("%w: %v", ErrCorrupt, err)
	}
	if record.Authority != AuthorityShared && record.Authority != AuthorityLocal {
		return ErrCorrupt
	}
	revision.Authority = record.Authority
	values := maps.Clone(record.Values)
	if values == nil {
		values = map[string]string{}
	}
	for name := range values {
		if config.SensitiveSharedSetting(name) {
			return ErrCorrupt
		}
	}
	if config.SharedValuesChecksum(values) != revision.Checksum {
		return ErrCorrupt
	}
	if !unseal {
		return nil
	}
	for name, sealed := range record.Sealed {
		if !config.SensitiveSharedSetting(name) {
			return ErrCorrupt
		}
		if sealed == "" {
			values[name] = ""
			continue
		}
		if s.sealer == nil {
			return &UnsealError{Setting: name}
		}
		value, err := s.sealer.DecryptCredential(sealed)
		if err != nil {
			return &UnsealError{Setting: name, Err: err}
		}
		values[name] = value
	}
	if err := config.ValidateSharedValues(values); err != nil {
		return fmt.Errorf("%w: %v", ErrCorrupt, err)
	}
	revision.Values = values
	return nil
}

func (s *Store) validateWrite(values map[string]string, actor, operationID string) error {
	if err := validIdentifier("actor", actor); err != nil {
		return err
	}
	if err := validIdentifier("operation ID", operationID); err != nil {
		return err
	}
	return config.ValidateSharedValues(values)
}
