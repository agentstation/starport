package identity

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json/v2"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/agentstation/starport/internal/sqlstore"
)

const maxIdentityReplayBytes = 256 << 10

// ErrReplayConflict refuses identity recovery with missing or conflicting evidence.
var ErrReplayConflict = errors.New("identity recovery evidence conflicts with retained state")

// RecoveryUserChange carries exact independently retained user revisions.
type RecoveryUserChange struct {
	Before *UserRecord `json:"before"`
	After  *UserRecord `json:"after"`
}

// RecoveryTeamChange carries exact independently retained team revisions.
type RecoveryTeamChange struct {
	Before *TeamRecord `json:"before"`
	After  *TeamRecord `json:"after"`
}

// RecoveryMembershipChange adds or removes one exact membership tuple.
type RecoveryMembershipChange struct {
	Before *Membership `json:"before"`
	After  *Membership `json:"after"`
}

// RecoveryGrantChange adds or removes one exact account grant tuple.
type RecoveryGrantChange struct {
	Before *AccountGrant `json:"before"`
	After  *AccountGrant `json:"after"`
}

// RecoveryBudgetOrigin preserves the one-use initialization disposition after team deletion.
type RecoveryBudgetOrigin struct {
	TeamID            string `json:"team_id"`
	HistoryID         string `json:"history_id"`
	InitializeAllowed bool   `json:"initialize_allowed"`
}

// RecoveryOriginChange supplies explicit origin evidence. Origins never disappear.
type RecoveryOriginChange struct {
	Before *RecoveryBudgetOrigin `json:"before"`
	After  *RecoveryBudgetOrigin `json:"after"`
}

// RecoveryIdentityEvent contains exactly one ordered owner transition.
type RecoveryIdentityEvent struct {
	User       *RecoveryUserChange       `json:"user,omitempty"`
	Team       *RecoveryTeamChange       `json:"team,omitempty"`
	Membership *RecoveryMembershipChange `json:"membership,omitempty"`
	Grant      *RecoveryGrantChange      `json:"grant,omitempty"`
	Origin     *RecoveryOriginChange     `json:"origin,omitempty"`
}
type recoveryIdentityPayload struct {
	Version int                     `json:"version"`
	Events  []RecoveryIdentityEvent `json:"events"`
}

// RecoveryTransition holds private identity evidence for an ordered SQL replay step.
// Its digest binds content. The coordinator must verify provenance and interval completeness.
type RecoveryTransition struct{ payload recoveryIdentityPayload }

func (RecoveryTransition) String() string { return "<private identity recovery transition>" }

// GoString prevents diagnostic formatting from exposing private evidence.
func (RecoveryTransition) GoString() string { return "<private identity recovery transition>" }

// Format redacts supported diagnostic formatting.
func (RecoveryTransition) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("<private identity recovery transition>"))
}

// MarshalJSON serializes private operator evidence. Do not expose it in public APIs or logs.
func (r RecoveryTransition) MarshalJSON() ([]byte, error) {
	return json.Marshal(r.payload, json.Deterministic(true))
}

// UnmarshalJSON accepts only bounded typed owner events.
func (r *RecoveryTransition) UnmarshalJSON(data []byte) error {
	if r == nil || len(data) > maxIdentityReplayBytes {
		return ErrReplayConflict
	}
	var payload recoveryIdentityPayload
	if json.Unmarshal(data, &payload, json.RejectUnknownMembers(true)) != nil || normalizeIdentityPayload(&payload) != nil {
		return ErrReplayConflict
	}
	r.payload = payload
	return nil
}

// NewRecoveryTransition copies and validates ordered independent identity events.
func NewRecoveryTransition(events []RecoveryIdentityEvent) (RecoveryTransition, error) {
	if len(events) == 0 || len(events) > 64 {
		return RecoveryTransition{}, ErrReplayConflict
	}
	data, err := json.Marshal(recoveryIdentityPayload{Version: 1, Events: events}, json.Deterministic(true))
	if err != nil {
		return RecoveryTransition{}, ErrReplayConflict
	}
	var result RecoveryTransition
	err = result.UnmarshalJSON(data)
	return result, err
}

// Digest returns the canonical typed-input digest for RelationalReplayStep.
func (r RecoveryTransition) Digest() (string, error) {
	data, err := r.MarshalJSON()
	if err != nil {
		return "", ErrReplayConflict
	}
	var validated RecoveryTransition
	if validated.UnmarshalJSON(data) != nil {
		return "", ErrReplayConflict
	}
	canonical, err := validated.MarshalJSON()
	if err != nil {
		return "", ErrReplayConflict
	}
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:]), nil
}

func normalizeIdentityPayload(p *recoveryIdentityPayload) error {
	if p.Version != 1 || len(p.Events) == 0 || len(p.Events) > 64 {
		return ErrReplayConflict
	}
	for _, event := range p.Events {
		count := 0
		if event.User != nil {
			count++
			if normalizeRecoveryUser(event.User) != nil {
				return ErrReplayConflict
			}
		}
		if event.Team != nil {
			count++
			if normalizeRecoveryTeam(event.Team) != nil {
				return ErrReplayConflict
			}
		}
		if event.Membership != nil {
			count++
			if normalizeRecoveryMembership(event.Membership) != nil {
				return ErrReplayConflict
			}
		}
		if event.Grant != nil {
			count++
			if normalizeRecoveryGrant(event.Grant) != nil {
				return ErrReplayConflict
			}
		}
		if event.Origin != nil {
			count++
			if validateRecoveryOrigin(event.Origin) != nil {
				return ErrReplayConflict
			}
		}
		if count != 1 {
			return ErrReplayConflict
		}
	}
	return nil
}
func validRecoveryText(value string, maximum int, empty bool) bool {
	return (empty || value != "") && len(value) <= maximum && utf8.ValidString(value) && strings.IndexFunc(value, func(r rune) bool { return r < 32 || r == 127 }) < 0
}
func recoveryTimes(created, updated *time.Time) bool {
	*created = created.UTC()
	*updated = updated.UTC()
	return !created.IsZero() && !updated.IsZero() && !updated.Before(*created)
}
func normalizeRecoveryUser(change *RecoveryUserChange) error {
	if change.Before == nil && change.After == nil {
		return ErrReplayConflict
	}
	for _, value := range []*UserRecord{change.Before, change.After} {
		if value != nil {
			u := &value.User
			if value.Revision == 0 || value.Revision > math.MaxInt64 || u.Validate() != nil || !validRecoveryText(u.ID, maxIDLength, false) || !validRecoveryText(u.Subject, maxIDLength, false) || !validRecoveryText(u.Email, maxNameLength, true) || !validRecoveryText(u.DisplayName, maxNameLength, true) || !recoveryTimes(&u.CreatedAt, &u.UpdatedAt) {
				return ErrReplayConflict
			}
		}
	}
	if change.Before != nil && change.After != nil {
		a, b := change.Before, change.After
		if a.User.ID != b.User.ID || a.User.Subject != b.User.Subject || a.User.CreatedAt != b.User.CreatedAt || b.Revision <= a.Revision || b.User.UpdatedAt.Before(a.User.UpdatedAt) {
			return ErrReplayConflict
		}
	}
	return nil
}
func normalizeRecoveryTeam(change *RecoveryTeamChange) error {
	if change.Before == nil && change.After == nil {
		return ErrReplayConflict
	}
	for _, value := range []*TeamRecord{change.Before, change.After} {
		if value != nil {
			team := &value.Team
			if value.Revision == 0 || value.Revision > math.MaxInt64 || team.Validate() != nil || !validRecoveryText(team.ID, maxIDLength, false) || !validRecoveryText(team.Name, maxNameLength, false) || !recoveryTimes(&team.CreatedAt, &team.UpdatedAt) {
				return ErrReplayConflict
			}
			if team.Budget != nil && !validRecoveryText(team.Budget.HistoryID, 256, true) {
				return ErrReplayConflict
			}
		}
	}
	if change.Before != nil && change.After != nil {
		a, b := change.Before, change.After
		if a.Team.ID != b.Team.ID || a.Team.CreatedAt != b.Team.CreatedAt || b.Revision <= a.Revision || b.Team.UpdatedAt.Before(a.Team.UpdatedAt) {
			return ErrReplayConflict
		}
		if a.Team.Budget != nil && b.Team.Budget != nil {
			if a.Team.Budget.Interval == b.Team.Budget.Interval && a.Team.Budget.HistoryID != b.Team.Budget.HistoryID {
				return ErrReplayConflict
			}
			if a.Team.Budget.Interval != b.Team.Budget.Interval && a.Team.Budget.HistoryID == b.Team.Budget.HistoryID {
				return ErrReplayConflict
			}
		}
	}
	return nil
}
func normalizeRecoveryMembership(change *RecoveryMembershipChange) error {
	if (change.Before == nil) == (change.After == nil) {
		return ErrReplayConflict
	}
	value := change.Before
	if value == nil {
		value = change.After
	}
	value.CreatedAt = value.CreatedAt.UTC()
	if value.Validate() != nil || value.CreatedAt.IsZero() || !validRecoveryText(value.UserID, maxIDLength, false) || !validRecoveryText(value.TeamID, maxIDLength, false) {
		return ErrReplayConflict
	}
	return nil
}
func normalizeRecoveryGrant(change *RecoveryGrantChange) error {
	if (change.Before == nil) == (change.After == nil) {
		return ErrReplayConflict
	}
	value := change.Before
	if value == nil {
		value = change.After
	}
	value.CreatedAt = value.CreatedAt.UTC()
	if value.Validate() != nil || value.CreatedAt.IsZero() || !validRecoveryText(value.AccountID, maxIDLength, false) || !validRecoveryText(value.UserID, maxIDLength, true) || !validRecoveryText(value.TeamID, maxIDLength, true) {
		return ErrReplayConflict
	}
	return nil
}
func validateRecoveryOrigin(change *RecoveryOriginChange) error {
	if change.After == nil {
		return ErrReplayConflict
	}
	for _, value := range []*RecoveryBudgetOrigin{change.Before, change.After} {
		if value != nil && (!validRecoveryText(value.TeamID, maxIDLength, false) || !validRecoveryText(value.HistoryID, 256, !value.InitializeAllowed)) {
			return ErrReplayConflict
		}
	}
	if change.Before != nil && (change.Before.TeamID != change.After.TeamID || change.Before.HistoryID != change.After.HistoryID || !change.Before.InitializeAllowed && change.After.InitializeAllowed) {
		return ErrReplayConflict
	}
	return nil
}

// ApplyRecoveryTransition uses only the coordinator's closed replay connection.
// Call it inside ReplayRelationalImport. It does not start or commit a transaction.
// The coordinator must update authorization revisions and verify cross-store policy before activation.
func ApplyRecoveryTransition(ctx context.Context, db *sqlstore.DB, conn *sql.Conn, transition RecoveryTransition) error {
	if ctx == nil || db == nil || conn == nil {
		return ErrReplayConflict
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, err := transition.Digest(); err != nil {
		return err
	}
	for _, event := range transition.payload.Events {
		var err error
		switch {
		case event.User != nil:
			err = applyRecoveryUser(ctx, db, conn, *event.User)
		case event.Team != nil:
			err = applyRecoveryTeam(ctx, db, conn, *event.Team)
		case event.Membership != nil:
			err = applyRecoveryMembership(ctx, db, conn, *event.Membership)
		case event.Grant != nil:
			err = applyRecoveryGrant(ctx, db, conn, *event.Grant)
		case event.Origin != nil:
			err = applyRecoveryOrigin(ctx, db, conn, *event.Origin)
		}
		if err != nil {
			return err
		}
	}
	for _, event := range transition.payload.Events {
		if event.Team != nil && event.Team.After != nil {
			if err := requireRecoveryTeamOrigin(ctx, db, conn, event.Team.After.Team.ID); err != nil {
				return err
			}
		}
	}
	return nil
}
