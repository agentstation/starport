package identity

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/agentstation/starport/internal/sqlstore"
)

// StorageSchemaVersion identifies the only supported identity record schema.
const StorageSchemaVersion = 1

const defaultListLimit = 1000

// ErrRepositoryRequired reports a missing relational store.
var ErrRepositoryRequired = errors.New("identity storage is required")

// UserRecord is one versioned user repository value.
type UserRecord struct {
	Revision uint64
	User     User
}

// TeamRecord is one versioned team repository value.
type TeamRecord struct {
	Revision uint64
	Team     Team
}

// UserRepository is the durable user contract. Users are relational — a
// subject lookup, memberships, and later grants all join on them — so the
// repository rides sqlstore.
type UserRepository interface {
	Create(context.Context, User) (UserRecord, error)
	GetByID(context.Context, string) (UserRecord, error)
	// GetBySubject resolves the external identity subject an acquisition
	// path hands back to the one user it names.
	GetBySubject(context.Context, string) (UserRecord, error)
	List(context.Context, int, int) ([]UserRecord, error)
	Update(context.Context, User, uint64) (UserRecord, error)
	Delete(context.Context, string, uint64) error
}

// TeamRepository is the durable team contract.
type TeamRepository interface {
	Create(context.Context, Team) (TeamRecord, error)
	GetByID(context.Context, string) (TeamRecord, error)
	List(context.Context, int, int) ([]TeamRecord, error)
	Update(context.Context, Team, uint64) (TeamRecord, error)
	Delete(context.Context, string, uint64) error
}

// MembershipRepository is the durable membership contract. A membership is
// a link row: it is added and removed, never edited, so it carries no
// revision.
type MembershipRepository interface {
	Add(context.Context, Membership) (Membership, error)
	Remove(ctx context.Context, userID, teamID string) error
	ListByUser(context.Context, string) ([]Membership, error)
	ListByTeam(context.Context, string) ([]Membership, error)
}

// AccountGrantRepository is the durable account-grant contract. A grant is
// a link row like a membership: added and removed, never edited.
type AccountGrantRepository interface {
	Add(context.Context, AccountGrant) (AccountGrant, error)
	// Remove deletes the exact grant its argument states; only the grantee
	// fields and the account matter, not the timestamp.
	Remove(context.Context, AccountGrant) error
	ListByAccount(context.Context, string) ([]AccountGrant, error)
	ListByUser(context.Context, string) ([]AccountGrant, error)
	ListByTeam(context.Context, string) ([]AccountGrant, error)
	// ReachableAccounts reports every account a user's grants reach: the
	// ones granted to the user directly and the ones granted to any team
	// the user belongs to, deduplicated and ordered.
	ReachableAccounts(ctx context.Context, userID string) ([]string, error)
}

// Repositories bundles the identity repositories one store opens.
type Repositories struct {
	Users         UserRepository
	Teams         TeamRepository
	Memberships   MembershipRepository
	AccountGrants AccountGrantRepository
}

// Open returns sqlstore-backed identity repositories. The caller has
// already migrated the store; this constructor only refuses a nil one.
func Open(db *sqlstore.DB) (Repositories, error) {
	if db == nil {
		return Repositories{}, ErrRepositoryRequired
	}
	now := time.Now
	return Repositories{
		Users:         &userRepository{db: db, now: now},
		Teams:         &teamRepository{db: db, now: now},
		Memberships:   &membershipRepository{db: db, now: now},
		AccountGrants: &accountGrantRepository{db: db, now: now},
	}, nil
}

// userRecord is the stored JSON document. The id, subject, and revision
// also live in their own columns so SQL can address and guard them; the
// record column stays the one source of the user's content.
type userRecord struct {
	SchemaVersion int    `json:"schema_version"`
	Revision      uint64 `json:"revision"`
	User          User   `json:"user"`
}

type userRepository struct {
	db  *sqlstore.DB
	now func() time.Time
}

func (r *userRepository) Create(ctx context.Context, value User) (UserRecord, error) {
	stored := userRecord{SchemaVersion: StorageSchemaVersion, Revision: 1, User: value}
	created := r.now().UTC()
	stored.User.CreatedAt = created
	stored.User.UpdatedAt = created
	if err := stored.User.Validate(); err != nil {
		return UserRecord{}, err
	}
	data, err := json.Marshal(stored)
	if err != nil {
		return UserRecord{}, fmt.Errorf("encode user record: %w", err)
	}
	// #nosec G701 -- The SQL is a compile-time constant; the OAuth-derived
	// values only ever ride placeholders.
	_, err = r.db.ExecContext(ctx,
		r.db.Bind(`INSERT INTO users (id, subject, revision, record) VALUES (?, ?, ?, ?)`),
		stored.User.ID, stored.User.Subject, stored.Revision, string(data))
	if err != nil {
		// The insert has two unique constraints — the id and the subject —
		// and violating either is the duplicate-create conflict, whatever
		// the dialect's word for it.
		if taken, takenErr := r.taken(ctx, stored.User.ID, stored.User.Subject); takenErr == nil && taken {
			return UserRecord{}, ErrUserConflict
		}
		return UserRecord{}, fmt.Errorf("create user: %w", err)
	}
	return UserRecord{Revision: stored.Revision, User: stored.User}, nil
}

func (r *userRepository) GetByID(ctx context.Context, id string) (UserRecord, error) {
	if id == "" {
		return UserRecord{}, ErrMissingID
	}
	return r.getWhere(ctx, `SELECT record FROM users WHERE id = ?`, id)
}

func (r *userRepository) GetBySubject(ctx context.Context, subject string) (UserRecord, error) {
	if subject == "" {
		return UserRecord{}, ErrMissingSubject
	}
	return r.getWhere(ctx, `SELECT record FROM users WHERE subject = ?`, subject)
}

// getWhere takes the whole query rather than a clause fragment so every SQL
// string in this file is a compile-time constant; the argument only ever
// rides a placeholder.
func (r *userRepository) getWhere(ctx context.Context, query, argument string) (UserRecord, error) {
	var data string
	// #nosec G701 -- Both callers pass a compile-time constant query; the
	// argument only ever rides the placeholder.
	err := r.db.QueryRowContext(ctx, r.db.Bind(query), argument).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return UserRecord{}, ErrUserNotFound
	}
	if err != nil {
		return UserRecord{}, fmt.Errorf("get user: %w", err)
	}
	stored, err := decodeUser(data)
	if err != nil {
		return UserRecord{}, err
	}
	return UserRecord{Revision: stored.Revision, User: stored.User}, nil
}

func (r *userRepository) List(ctx context.Context, limit, offset int) ([]UserRecord, error) {
	return listRecords(ctx, r.db,
		`SELECT record FROM users ORDER BY id LIMIT ? OFFSET ?`, "users",
		limit, offset, func(data string) (UserRecord, error) {
			stored, err := decodeUser(data)
			if err != nil {
				return UserRecord{}, err
			}
			return UserRecord{Revision: stored.Revision, User: stored.User}, nil
		})
}

func (r *userRepository) Update(ctx context.Context, value User, expectedRevision uint64) (UserRecord, error) {
	current, err := r.GetByID(ctx, value.ID)
	if err != nil {
		return UserRecord{}, err
	}
	if current.Revision != expectedRevision {
		return UserRecord{}, ErrUserConflict
	}
	updated := userRecord{
		SchemaVersion: StorageSchemaVersion,
		Revision:      current.Revision + 1,
		User:          value,
	}
	// The subject and creation time belong to the record, not the caller's
	// payload: a user's external identity never changes.
	updated.User.Subject = current.User.Subject
	updated.User.CreatedAt = current.User.CreatedAt
	updated.User.UpdatedAt = r.now().UTC()
	if err := updated.User.Validate(); err != nil {
		return UserRecord{}, err
	}
	data, err := json.Marshal(updated)
	if err != nil {
		return UserRecord{}, fmt.Errorf("encode user update: %w", err)
	}
	// The revision guard in the WHERE clause is the compare-and-swap.
	// #nosec G701 -- The SQL is a compile-time constant; the OAuth-derived
	// values only ever ride placeholders.
	result, err := r.db.ExecContext(ctx,
		r.db.Bind(`UPDATE users SET revision = ?, record = ? WHERE id = ? AND revision = ?`),
		updated.Revision, string(data), updated.User.ID, expectedRevision)
	if err != nil {
		return UserRecord{}, fmt.Errorf("update user: %w", err)
	}
	if err := oneRowMoved(result, ErrUserConflict); err != nil {
		return UserRecord{}, err
	}
	return UserRecord{Revision: updated.Revision, User: updated.User}, nil
}

func (r *userRepository) Delete(ctx context.Context, id string, expectedRevision uint64) error {
	current, err := r.GetByID(ctx, id)
	if err != nil {
		return err
	}
	if expectedRevision != 0 && current.Revision != expectedRevision {
		return ErrUserConflict
	}
	// Grants are access control, so they must never outlive the user they
	// name: a dangling grant would wait for whatever next claims the id.
	if _, err := r.db.ExecContext(ctx,
		r.db.Bind(`DELETE FROM account_grants WHERE user_id = ?`), id); err != nil {
		return fmt.Errorf("delete user account grants: %w", err)
	}
	result, err := r.db.ExecContext(ctx,
		r.db.Bind(`DELETE FROM users WHERE id = ? AND revision = ?`),
		id, current.Revision)
	if err != nil {
		return fmt.Errorf("delete user: %w", err)
	}
	return oneRowMoved(result, ErrUserConflict)
}

func (r *userRepository) taken(ctx context.Context, id, subject string) (bool, error) {
	var count int
	// #nosec G701 -- The SQL is a compile-time constant; the OAuth-derived
	// values only ever ride placeholders.
	err := r.db.QueryRowContext(ctx,
		r.db.Bind(`SELECT COUNT(*) FROM users WHERE id = ? OR subject = ?`), id, subject,
	).Scan(&count)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

func decodeUser(data string) (userRecord, error) {
	var stored userRecord
	if err := json.Unmarshal([]byte(data), &stored); err != nil {
		return userRecord{}, fmt.Errorf("%w: %s", ErrCorruptUser, err)
	}
	if stored.SchemaVersion != StorageSchemaVersion {
		return userRecord{}, fmt.Errorf("%w: unsupported schema version %d", ErrCorruptUser, stored.SchemaVersion)
	}
	if stored.Revision == 0 {
		return userRecord{}, fmt.Errorf("%w: user revision is zero", ErrCorruptUser)
	}
	if err := stored.User.Validate(); err != nil {
		return userRecord{}, fmt.Errorf("%w: %s", ErrCorruptUser, err)
	}
	return stored, nil
}

// teamRecord is the stored JSON document, shaped like userRecord.
type teamRecord struct {
	SchemaVersion int    `json:"schema_version"`
	Revision      uint64 `json:"revision"`
	Team          Team   `json:"team"`
}

type teamRepository struct {
	db  *sqlstore.DB
	now func() time.Time
}

func (r *teamRepository) Create(ctx context.Context, value Team) (TeamRecord, error) {
	stored := teamRecord{SchemaVersion: StorageSchemaVersion, Revision: 1, Team: value}
	created := r.now().UTC()
	stored.Team.CreatedAt = created
	stored.Team.UpdatedAt = created
	if err := stored.Team.Validate(); err != nil {
		return TeamRecord{}, err
	}
	data, err := json.Marshal(stored)
	if err != nil {
		return TeamRecord{}, fmt.Errorf("encode team record: %w", err)
	}
	_, err = r.db.ExecContext(ctx,
		r.db.Bind(`INSERT INTO teams (id, revision, record) VALUES (?, ?, ?)`),
		stored.Team.ID, stored.Revision, string(data))
	if err != nil {
		if exists, existsErr := r.exists(ctx, stored.Team.ID); existsErr == nil && exists {
			return TeamRecord{}, ErrTeamConflict
		}
		return TeamRecord{}, fmt.Errorf("create team: %w", err)
	}
	return TeamRecord{Revision: stored.Revision, Team: stored.Team}, nil
}

func (r *teamRepository) GetByID(ctx context.Context, id string) (TeamRecord, error) {
	if id == "" {
		return TeamRecord{}, ErrMissingID
	}
	var data string
	err := r.db.QueryRowContext(ctx,
		r.db.Bind(`SELECT record FROM teams WHERE id = ?`), id,
	).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return TeamRecord{}, ErrTeamNotFound
	}
	if err != nil {
		return TeamRecord{}, fmt.Errorf("get team: %w", err)
	}
	stored, err := decodeTeam(data)
	if err != nil {
		return TeamRecord{}, err
	}
	return TeamRecord{Revision: stored.Revision, Team: stored.Team}, nil
}

func (r *teamRepository) List(ctx context.Context, limit, offset int) ([]TeamRecord, error) {
	return listRecords(ctx, r.db,
		`SELECT record FROM teams ORDER BY id LIMIT ? OFFSET ?`, "teams",
		limit, offset, func(data string) (TeamRecord, error) {
			stored, err := decodeTeam(data)
			if err != nil {
				return TeamRecord{}, err
			}
			return TeamRecord{Revision: stored.Revision, Team: stored.Team}, nil
		})
}

func (r *teamRepository) Update(ctx context.Context, value Team, expectedRevision uint64) (TeamRecord, error) {
	current, err := r.GetByID(ctx, value.ID)
	if err != nil {
		return TeamRecord{}, err
	}
	if current.Revision != expectedRevision {
		return TeamRecord{}, ErrTeamConflict
	}
	updated := teamRecord{
		SchemaVersion: StorageSchemaVersion,
		Revision:      current.Revision + 1,
		Team:          value,
	}
	updated.Team.CreatedAt = current.Team.CreatedAt
	updated.Team.UpdatedAt = r.now().UTC()
	if err := updated.Team.Validate(); err != nil {
		return TeamRecord{}, err
	}
	data, err := json.Marshal(updated)
	if err != nil {
		return TeamRecord{}, fmt.Errorf("encode team update: %w", err)
	}
	result, err := r.db.ExecContext(ctx,
		r.db.Bind(`UPDATE teams SET revision = ?, record = ? WHERE id = ? AND revision = ?`),
		updated.Revision, string(data), updated.Team.ID, expectedRevision)
	if err != nil {
		return TeamRecord{}, fmt.Errorf("update team: %w", err)
	}
	if err := oneRowMoved(result, ErrTeamConflict); err != nil {
		return TeamRecord{}, err
	}
	return TeamRecord{Revision: updated.Revision, Team: updated.Team}, nil
}

func (r *teamRepository) Delete(ctx context.Context, id string, expectedRevision uint64) error {
	current, err := r.GetByID(ctx, id)
	if err != nil {
		return err
	}
	if expectedRevision != 0 && current.Revision != expectedRevision {
		return ErrTeamConflict
	}
	// Members belong to the team, so removing the team removes them: a
	// membership must never outlive either end it ties.
	if _, err := r.db.ExecContext(ctx,
		r.db.Bind(`DELETE FROM team_memberships WHERE team_id = ?`), id); err != nil {
		return fmt.Errorf("delete team memberships: %w", err)
	}
	// Grants are access control, so they go with the team for the same
	// reason: nothing may keep reaching an account through a team that is
	// gone.
	if _, err := r.db.ExecContext(ctx,
		r.db.Bind(`DELETE FROM account_grants WHERE team_id = ?`), id); err != nil {
		return fmt.Errorf("delete team account grants: %w", err)
	}
	result, err := r.db.ExecContext(ctx,
		r.db.Bind(`DELETE FROM teams WHERE id = ? AND revision = ?`),
		id, current.Revision)
	if err != nil {
		return fmt.Errorf("delete team: %w", err)
	}
	return oneRowMoved(result, ErrTeamConflict)
}

func (r *teamRepository) exists(ctx context.Context, id string) (bool, error) {
	var count int
	err := r.db.QueryRowContext(ctx,
		r.db.Bind(`SELECT COUNT(*) FROM teams WHERE id = ?`), id,
	).Scan(&count)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

func decodeTeam(data string) (teamRecord, error) {
	var stored teamRecord
	if err := json.Unmarshal([]byte(data), &stored); err != nil {
		return teamRecord{}, fmt.Errorf("%w: %s", ErrCorruptTeam, err)
	}
	if stored.SchemaVersion != StorageSchemaVersion {
		return teamRecord{}, fmt.Errorf("%w: unsupported schema version %d", ErrCorruptTeam, stored.SchemaVersion)
	}
	if stored.Revision == 0 {
		return teamRecord{}, fmt.Errorf("%w: team revision is zero", ErrCorruptTeam)
	}
	if err := stored.Team.Validate(); err != nil {
		return teamRecord{}, fmt.Errorf("%w: %s", ErrCorruptTeam, err)
	}
	return stored, nil
}

type membershipRepository struct {
	db  *sqlstore.DB
	now func() time.Time
}

func (r *membershipRepository) Add(ctx context.Context, value Membership) (Membership, error) {
	if err := value.Validate(); err != nil {
		return Membership{}, err
	}
	value.CreatedAt = r.now().UTC()
	// Both ends must exist: a membership names a real user on a real team.
	if _, err := (&userRepository{db: r.db, now: r.now}).GetByID(ctx, value.UserID); err != nil {
		return Membership{}, err
	}
	if _, err := (&teamRepository{db: r.db, now: r.now}).GetByID(ctx, value.TeamID); err != nil {
		return Membership{}, err
	}
	_, err := r.db.ExecContext(ctx,
		r.db.Bind(`INSERT INTO team_memberships (user_id, team_id, created_at) VALUES (?, ?, ?)`),
		value.UserID, value.TeamID, value.CreatedAt.Format(time.RFC3339Nano))
	if err != nil {
		if exists, existsErr := r.exists(ctx, value.UserID, value.TeamID); existsErr == nil && exists {
			return Membership{}, ErrMembershipConflict
		}
		return Membership{}, fmt.Errorf("add membership: %w", err)
	}
	return value, nil
}

func (r *membershipRepository) Remove(ctx context.Context, userID, teamID string) error {
	if !validID(userID) || !validID(teamID) {
		return ErrMissingID
	}
	result, err := r.db.ExecContext(ctx,
		r.db.Bind(`DELETE FROM team_memberships WHERE user_id = ? AND team_id = ?`),
		userID, teamID)
	if err != nil {
		return fmt.Errorf("remove membership: %w", err)
	}
	return oneRowMoved(result, ErrMembershipNotFound)
}

func (r *membershipRepository) ListByUser(ctx context.Context, userID string) ([]Membership, error) {
	if userID == "" {
		return nil, ErrMissingID
	}
	return r.list(ctx, `user_id = ? ORDER BY team_id`, userID)
}

func (r *membershipRepository) ListByTeam(ctx context.Context, teamID string) ([]Membership, error) {
	if teamID == "" {
		return nil, ErrMissingID
	}
	return r.list(ctx, `team_id = ? ORDER BY user_id`, teamID)
}

func (r *membershipRepository) list(ctx context.Context, clause, argument string) ([]Membership, error) {
	rows, err := r.db.QueryContext(ctx,
		r.db.Bind(`SELECT user_id, team_id, created_at FROM team_memberships WHERE `+clause),
		argument)
	if err != nil {
		return nil, fmt.Errorf("list memberships: %w", err)
	}
	defer func() { _ = rows.Close() }()
	memberships := make([]Membership, 0)
	for rows.Next() {
		var userID, teamID, created string
		if err := rows.Scan(&userID, &teamID, &created); err != nil {
			return nil, fmt.Errorf("read listed membership: %w", err)
		}
		createdAt, err := time.Parse(time.RFC3339Nano, created)
		if err != nil {
			return nil, fmt.Errorf("read listed membership: %w", err)
		}
		memberships = append(memberships, Membership{UserID: userID, TeamID: teamID, CreatedAt: createdAt})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list memberships: %w", err)
	}
	return memberships, nil
}

func (r *membershipRepository) exists(ctx context.Context, userID, teamID string) (bool, error) {
	var count int
	err := r.db.QueryRowContext(ctx,
		r.db.Bind(`SELECT COUNT(*) FROM team_memberships WHERE user_id = ? AND team_id = ?`),
		userID, teamID,
	).Scan(&count)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

type accountGrantRepository struct {
	db  *sqlstore.DB
	now func() time.Time
}

func (r *accountGrantRepository) Add(ctx context.Context, value AccountGrant) (AccountGrant, error) {
	if err := value.Validate(); err != nil {
		return AccountGrant{}, err
	}
	value.CreatedAt = r.now().UTC()
	// The grantee must exist: a grant names a real user or a real team. The
	// account is not checked here because accounts live outside this store;
	// the row only names one, like a shared credential's grant list does.
	if value.UserID != "" {
		if _, err := (&userRepository{db: r.db, now: r.now}).GetByID(ctx, value.UserID); err != nil {
			return AccountGrant{}, err
		}
	}
	if value.TeamID != "" {
		if _, err := (&teamRepository{db: r.db, now: r.now}).GetByID(ctx, value.TeamID); err != nil {
			return AccountGrant{}, err
		}
	}
	_, err := r.db.ExecContext(ctx,
		r.db.Bind(`INSERT INTO account_grants (account_id, user_id, team_id, created_at) VALUES (?, ?, ?, ?)`),
		value.AccountID, value.UserID, value.TeamID, value.CreatedAt.Format(time.RFC3339Nano))
	if err != nil {
		if exists, existsErr := r.exists(ctx, value); existsErr == nil && exists {
			return AccountGrant{}, ErrAccountGrantConflict
		}
		return AccountGrant{}, fmt.Errorf("add account grant: %w", err)
	}
	return value, nil
}

func (r *accountGrantRepository) Remove(ctx context.Context, value AccountGrant) error {
	if err := value.Validate(); err != nil {
		return err
	}
	result, err := r.db.ExecContext(ctx,
		r.db.Bind(`DELETE FROM account_grants WHERE account_id = ? AND user_id = ? AND team_id = ?`),
		value.AccountID, value.UserID, value.TeamID)
	if err != nil {
		return fmt.Errorf("remove account grant: %w", err)
	}
	return oneRowMoved(result, ErrAccountGrantNotFound)
}

func (r *accountGrantRepository) ListByAccount(ctx context.Context, accountID string) ([]AccountGrant, error) {
	if accountID == "" {
		return nil, ErrMissingID
	}
	return r.list(ctx, `account_id = ? ORDER BY user_id, team_id`, accountID)
}

func (r *accountGrantRepository) ListByUser(ctx context.Context, userID string) ([]AccountGrant, error) {
	if userID == "" {
		return nil, ErrMissingID
	}
	return r.list(ctx, `user_id = ? ORDER BY account_id`, userID)
}

func (r *accountGrantRepository) ListByTeam(ctx context.Context, teamID string) ([]AccountGrant, error) {
	if teamID == "" {
		return nil, ErrMissingID
	}
	return r.list(ctx, `team_id = ? ORDER BY account_id`, teamID)
}

func (r *accountGrantRepository) ReachableAccounts(ctx context.Context, userID string) ([]string, error) {
	if userID == "" {
		return nil, ErrMissingID
	}
	// UNION deduplicates, so an account granted to the user and to one of
	// their teams appears once. A grant row's empty grantee side can never
	// match a real id or a membership, so each branch reads only its kind.
	rows, err := r.db.QueryContext(ctx, r.db.Bind(
		`SELECT account_id FROM account_grants WHERE user_id = ?
		 UNION
		 SELECT g.account_id FROM account_grants g
		 JOIN team_memberships m ON m.team_id = g.team_id
		 WHERE m.user_id = ?
		 ORDER BY account_id`),
		userID, userID)
	if err != nil {
		return nil, fmt.Errorf("resolve reachable accounts: %w", err)
	}
	defer func() { _ = rows.Close() }()
	accounts := make([]string, 0)
	for rows.Next() {
		var accountID string
		if err := rows.Scan(&accountID); err != nil {
			return nil, fmt.Errorf("read reachable account: %w", err)
		}
		accounts = append(accounts, accountID)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("resolve reachable accounts: %w", err)
	}
	return accounts, nil
}

func (r *accountGrantRepository) list(ctx context.Context, clause, argument string) ([]AccountGrant, error) {
	rows, err := r.db.QueryContext(ctx,
		r.db.Bind(`SELECT account_id, user_id, team_id, created_at FROM account_grants WHERE `+clause),
		argument)
	if err != nil {
		return nil, fmt.Errorf("list account grants: %w", err)
	}
	defer func() { _ = rows.Close() }()
	grants := make([]AccountGrant, 0)
	for rows.Next() {
		var accountID, userID, teamID, created string
		if err := rows.Scan(&accountID, &userID, &teamID, &created); err != nil {
			return nil, fmt.Errorf("read listed account grant: %w", err)
		}
		createdAt, err := time.Parse(time.RFC3339Nano, created)
		if err != nil {
			return nil, fmt.Errorf("read listed account grant: %w", err)
		}
		grants = append(grants, AccountGrant{
			AccountID: accountID, UserID: userID, TeamID: teamID, CreatedAt: createdAt,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list account grants: %w", err)
	}
	return grants, nil
}

func (r *accountGrantRepository) exists(ctx context.Context, value AccountGrant) (bool, error) {
	var count int
	err := r.db.QueryRowContext(ctx,
		r.db.Bind(`SELECT COUNT(*) FROM account_grants WHERE account_id = ? AND user_id = ? AND team_id = ?`),
		value.AccountID, value.UserID, value.TeamID,
	).Scan(&count)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// listRecords pages one record column and decodes each row, so the user
// and team listings share the loop instead of restating it.
func listRecords[T any](
	ctx context.Context,
	db *sqlstore.DB,
	query, noun string,
	limit, offset int,
	decode func(string) (T, error),
) ([]T, error) {
	limit, offset = listWindow(limit, offset)
	rows, err := db.QueryContext(ctx, db.Bind(query), limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list %s: %w", noun, err)
	}
	defer func() { _ = rows.Close() }()
	records := make([]T, 0)
	for rows.Next() {
		var data string
		if err := rows.Scan(&data); err != nil {
			return nil, fmt.Errorf("read listed %s record: %w", noun, err)
		}
		record, err := decode(data)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list %s: %w", noun, err)
	}
	return records, nil
}

func listWindow(limit, offset int) (int, int) {
	if limit <= 0 {
		limit = defaultListLimit
	}
	if offset < 0 {
		offset = 0
	}
	return limit, offset
}

func oneRowMoved(result sql.Result, conflict error) error {
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return conflict
	}
	return nil
}
