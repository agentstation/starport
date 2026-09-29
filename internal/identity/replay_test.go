package identity

import (
	"context"
	"database/sql"
	"encoding/json/v2"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/limits"
	"github.com/agentstation/starport/internal/sqlstore"
	"github.com/stretchr/testify/require"
)

func identityReplayValues() (UserRecord, TeamRecord, RecoveryBudgetOrigin, Membership, AccountGrant, AccountGrant) {
	at := time.Date(2026, 9, 29, 0, 0, 0, 123456789, time.UTC)
	user := UserRecord{Revision: 7, User: User{ID: "person", Subject: "provider:private-subject", Email: "private@example.com", CreatedAt: at, UpdatedAt: at}}
	team := TeamRecord{Revision: 4, Team: Team{ID: "team", Name: "Private team", Budget: &limits.TeamBudget{Limit: 1000, Interval: limits.IntervalDay, HistoryID: "original-history"}, CreatedAt: at, UpdatedAt: at}}
	origin := RecoveryBudgetOrigin{TeamID: team.Team.ID, HistoryID: "original-history", InitializeAllowed: false}
	member := Membership{UserID: user.User.ID, TeamID: team.Team.ID, CreatedAt: at}
	direct := AccountGrant{AccountID: "account", UserID: user.User.ID, CreatedAt: at}
	group := AccountGrant{AccountID: "account", TeamID: team.Team.ID, CreatedAt: at}
	return user, team, origin, member, direct, group
}
func recoveryEvents(user *UserRecord, team *TeamRecord, origin *RecoveryBudgetOrigin, member *Membership, direct, group *AccountGrant) []RecoveryIdentityEvent {
	return []RecoveryIdentityEvent{
		{Origin: &RecoveryOriginChange{After: origin}},
		{User: &RecoveryUserChange{After: user}},
		{Team: &RecoveryTeamChange{After: team}},
		{Membership: &RecoveryMembershipChange{After: member}},
		{Grant: &RecoveryGrantChange{After: direct}},
		{Grant: &RecoveryGrantChange{After: group}},
	}
}
func TestIdentityReplayNativeOrderedLifecycle(t *testing.T) {
	for _, backend := range []string{"sqlite", "postgres", "mysql"} {
		t.Run(backend, func(t *testing.T) {
			db, snapshot, identity := identityReplayTarget(t, backend)
			user, team, origin, member, direct, group := identityReplayValues()
			first, err := NewRecoveryTransition(recoveryEvents(&user, &team, &origin, &member, &direct, &group))
			require.NoError(t, err)
			digest, err := first.Digest()
			require.NoError(t, err)
			step := sqlstore.RelationalReplayStep{Sequence: 1, EvidenceSHA256: strings.Repeat("a", 64), TransitionSHA256: digest}
			apply := func(ctx context.Context, conn *sql.Conn) error { return ApplyRecoveryTransition(ctx, db, conn, first) }
			receipt, err := db.ReplayRelationalImport(t.Context(), snapshot, identity, step, apply)
			require.NoError(t, err)
			repos, err := Open(db)
			require.NoError(t, err)
			got, err := repos.Users.GetByID(t.Context(), user.User.ID)
			require.NoError(t, err)
			require.Equal(t, user, got)
			found, err := repos.Memberships.ListByUser(t.Context(), user.User.ID)
			require.NoError(t, err)
			require.Equal(t, []Membership{member}, found)
			grants, err := repos.AccountGrants.ReachableAccounts(t.Context(), user.User.ID)
			require.NoError(t, err)
			require.Equal(t, []string{"account"}, grants)
			var revisions int
			require.NoError(t, db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM authorization_revision`).Scan(&revisions))
			require.Zero(t, revisions, "coordinator owns authority revision")
			later := user
			later.Revision = 9
			later.User.DisplayName = "Later private name"
			later.User.UpdatedAt = later.User.UpdatedAt.Add(time.Hour)
			next, err := NewRecoveryTransition([]RecoveryIdentityEvent{{User: &RecoveryUserChange{Before: &user, After: &later}}, {Grant: &RecoveryGrantChange{Before: &direct}}, {Membership: &RecoveryMembershipChange{Before: &member}}})
			require.NoError(t, err)
			nextDigest, err := next.Digest()
			require.NoError(t, err)
			second := sqlstore.RelationalReplayStep{Sequence: 2, PreviousSHA256: receipt, EvidenceSHA256: strings.Repeat("b", 64), TransitionSHA256: nextDigest}
			interrupted := errors.New("interrupted identity replay")
			_, err = db.ReplayRelationalImport(t.Context(), snapshot, identity, second, func(ctx context.Context, conn *sql.Conn) error {
				require.NoError(t, ApplyRecoveryTransition(ctx, db, conn, next))
				return interrupted
			})
			require.ErrorIs(t, err, interrupted)
			got, err = repos.Users.GetByID(t.Context(), user.User.ID)
			require.NoError(t, err)
			require.Equal(t, user, got)
			last, err := db.ReplayRelationalImport(t.Context(), snapshot, identity, second, func(ctx context.Context, conn *sql.Conn) error { return ApplyRecoveryTransition(ctx, db, conn, next) })
			require.NoError(t, err)
			old, err := db.ReplayRelationalImport(t.Context(), snapshot, identity, step, func(context.Context, *sql.Conn) error { return errors.New("old callback must not run") })
			require.NoError(t, err)
			require.Equal(t, receipt, old)
			grants, err = repos.AccountGrants.ReachableAccounts(t.Context(), user.User.ID)
			require.NoError(t, err)
			require.Empty(t, grants, "withdrawals remain effective after old retry")
			deletion, err := NewRecoveryTransition([]RecoveryIdentityEvent{{Grant: &RecoveryGrantChange{Before: &group}}, {Team: &RecoveryTeamChange{Before: &team}}, {User: &RecoveryUserChange{Before: &later}}})
			require.NoError(t, err)
			deletionDigest, err := deletion.Digest()
			require.NoError(t, err)
			third := sqlstore.RelationalReplayStep{Sequence: 3, PreviousSHA256: last, EvidenceSHA256: strings.Repeat("c", 64), TransitionSHA256: deletionDigest}
			last, err = db.ReplayRelationalImport(t.Context(), snapshot, identity, third, func(ctx context.Context, conn *sql.Conn) error {
				return ApplyRecoveryTransition(ctx, db, conn, deletion)
			})
			require.NoError(t, err)
			_, err = repos.Teams.GetByID(t.Context(), team.Team.ID)
			require.ErrorIs(t, err, ErrTeamNotFound)
			var history string
			var allowed int
			require.NoError(t, db.QueryRowContext(t.Context(), db.Bind(`SELECT history_id,initialize_allowed FROM team_budget_origins WHERE team_id=?`), team.Team.ID).Scan(&history, &allowed))
			require.Equal(t, origin.HistoryID, history)
			require.Zero(t, allowed)
			recreated := team
			recreated.Revision = 1
			recreated.Team.CreatedAt = recreated.Team.CreatedAt.Add(2 * time.Hour)
			recreated.Team.UpdatedAt = recreated.Team.CreatedAt
			recreated.Team.Budget = team.Team.Budget.Clone()
			recreated.Team.Budget.HistoryID = "recreated-history"
			create, err := NewRecoveryTransition([]RecoveryIdentityEvent{{Team: &RecoveryTeamChange{After: &recreated}}})
			require.NoError(t, err)
			createDigest, err := create.Digest()
			require.NoError(t, err)
			_, err = db.ReplayRelationalImport(t.Context(), snapshot, identity, sqlstore.RelationalReplayStep{Sequence: 4, PreviousSHA256: last, EvidenceSHA256: strings.Repeat("d", 64), TransitionSHA256: createDigest}, func(ctx context.Context, conn *sql.Conn) error { return ApplyRecoveryTransition(ctx, db, conn, create) })
			require.NoError(t, err)
			require.NoError(t, db.QueryRowContext(t.Context(), db.Bind(`SELECT history_id,initialize_allowed FROM team_budget_origins WHERE team_id=?`), team.Team.ID).Scan(&history, &allowed))
			require.Equal(t, origin.HistoryID, history)
			require.Zero(t, allowed)
			require.ErrorIs(t, db.CheckImportBarrier(t.Context()), sqlstore.ErrImportRestricted)
		})
	}
}

func TestIdentityReplayCanonicalPrivatePayload(t *testing.T) {
	user, team, origin, member, direct, group := identityReplayValues()
	first, err := NewRecoveryTransition(recoveryEvents(&user, &team, &origin, &member, &direct, &group))
	require.NoError(t, err)
	digest, err := first.Digest()
	require.NoError(t, err)
	user.User.CreatedAt = user.User.CreatedAt.In(time.FixedZone("offset", 3600))
	user.User.UpdatedAt = user.User.CreatedAt
	repeated, err := NewRecoveryTransition(recoveryEvents(&user, &team, &origin, &member, &direct, &group))
	require.NoError(t, err)
	other, err := repeated.Digest()
	require.NoError(t, err)
	require.Equal(t, digest, other)
	user.User.Email = "changed-after-construction"
	retained, err := first.Digest()
	require.NoError(t, err)
	require.Equal(t, digest, retained)
	for _, format := range []string{"%v", "%+v", "%#v", "%s", "%q"} {
		require.NotContains(t, fmt.Sprintf(format, first), "private@example.com")
		require.Contains(t, fmt.Sprintf(format, first), "recovery transition")
	}
	// fmt bypasses Formatter for a mismatched pointer verb on a value struct.
	// Pointer fields must still hide private identity values in that diagnostic.
	for _, value := range []any{first, &first} {
		for _, format := range []string{"%p", "%+p", "%#p"} {
			printed := fmt.Sprintf(format, value)
			require.NotContains(t, printed, "private@example.com")
			require.NotContains(t, printed, "provider:private-subject")
			require.NotContains(t, printed, "person")
		}
	}
	data, err := json.Marshal(first)
	require.NoError(t, err)
	var decoded RecoveryTransition
	require.NoError(t, json.Unmarshal(data, &decoded))
	got, err := decoded.Digest()
	require.NoError(t, err)
	require.Equal(t, digest, got)
	for _, invalid := range [][]byte{[]byte(`{}`), []byte(`{"version":1,"events":[],"unknown":true}`), []byte(strings.Repeat("x", maxIdentityReplayBytes+1))} {
		require.Error(t, decoded.UnmarshalJSON(invalid))
	}
}

func TestIdentityReplayNativeRefusesConflictingEvidence(t *testing.T) {
	for _, backend := range []string{"sqlite", "postgres", "mysql"} {
		t.Run(backend, func(t *testing.T) {
			db, snapshot, identity := identityReplayTarget(t, backend)
			user, team, origin, member, direct, group := identityReplayValues()
			initial, err := NewRecoveryTransition(recoveryEvents(&user, &team, &origin, &member, &direct, &group))
			require.NoError(t, err)
			digest, err := initial.Digest()
			require.NoError(t, err)
			receipt, err := db.ReplayRelationalImport(t.Context(), snapshot, identity, sqlstore.RelationalReplayStep{Sequence: 1, EvidenceSHA256: strings.Repeat("a", 64), TransitionSHA256: digest}, func(ctx context.Context, conn *sql.Conn) error {
				return ApplyRecoveryTransition(ctx, db, conn, initial)
			})
			require.NoError(t, err)
			for _, mode := range []string{"subject-collision", "stale-user", "wrong-user-content", "wrong-grant-time", "wrong-membership-time", "linked-user-delete", "linked-team-delete", "missing-origin", "missing-user", "missing-team", "missing-origin-delete"} {
				t.Run(mode, func(t *testing.T) {
					events := []RecoveryIdentityEvent{}
					switch mode {
					case "subject-collision":
						collision := user
						collision.User.ID = "collision"
						events = append(events, RecoveryIdentityEvent{Grant: &RecoveryGrantChange{Before: &direct}}, RecoveryIdentityEvent{User: &RecoveryUserChange{After: &collision}})
					case "stale-user", "wrong-user-content":
						before := user
						after := user
						after.Revision = 10
						after.User.UpdatedAt = after.User.UpdatedAt.Add(time.Hour)
						if mode == "stale-user" {
							before.Revision = 6
						} else {
							before.User.Email = "wrong@example.com"
						}
						events = append(events, RecoveryIdentityEvent{User: &RecoveryUserChange{Before: &before, After: &after}})
					case "wrong-grant-time":
						wrong := direct
						wrong.CreatedAt = wrong.CreatedAt.Add(time.Second)
						events = append(events, RecoveryIdentityEvent{Grant: &RecoveryGrantChange{Before: &wrong}})
					case "wrong-membership-time":
						wrong := member
						wrong.CreatedAt = wrong.CreatedAt.Add(time.Second)
						events = append(events, RecoveryIdentityEvent{Membership: &RecoveryMembershipChange{Before: &wrong}})
					case "linked-user-delete":
						events = append(events, RecoveryIdentityEvent{User: &RecoveryUserChange{Before: &user}})
					case "linked-team-delete":
						events = append(events, RecoveryIdentityEvent{Team: &RecoveryTeamChange{Before: &team}})
					case "missing-origin":
						other := team
						other.Team.ID = "another-team"
						events = append(events, RecoveryIdentityEvent{Team: &RecoveryTeamChange{After: &other}})
					case "missing-origin-delete":
						_, err := db.ExecContext(t.Context(), db.Bind(`DELETE FROM team_budget_origins WHERE team_id=?`), team.Team.ID)
						require.NoError(t, err)
						t.Cleanup(func() {
							_, err := db.ExecContext(context.Background(), db.Bind(`INSERT INTO team_budget_origins(team_id,history_id,initialize_allowed) VALUES(?,?,0)`), team.Team.ID, origin.HistoryID)
							require.NoError(t, err)
						})
						events = append(events, RecoveryIdentityEvent{Grant: &RecoveryGrantChange{Before: &group}}, RecoveryIdentityEvent{Membership: &RecoveryMembershipChange{Before: &member}}, RecoveryIdentityEvent{Team: &RecoveryTeamChange{Before: &team}})
					case "missing-user":
						value := member
						value.UserID = "absent"
						events = append(events, RecoveryIdentityEvent{Membership: &RecoveryMembershipChange{After: &value}})
					case "missing-team":
						value := group
						value.TeamID = "absent"
						events = append(events, RecoveryIdentityEvent{Grant: &RecoveryGrantChange{After: &value}})
					}
					transition, err := NewRecoveryTransition(events)
					require.NoError(t, err)
					digest, err := transition.Digest()
					require.NoError(t, err)
					_, err = db.ReplayRelationalImport(t.Context(), snapshot, identity, sqlstore.RelationalReplayStep{Sequence: 2, PreviousSHA256: receipt, EvidenceSHA256: strings.Repeat("b", 64), TransitionSHA256: digest}, func(ctx context.Context, conn *sql.Conn) error {
						return ApplyRecoveryTransition(ctx, db, conn, transition)
					})
					require.ErrorIs(t, err, ErrReplayConflict)
					require.NotContains(t, err.Error(), user.User.Subject)
					repos, err := Open(db)
					require.NoError(t, err)
					actual, err := repos.Users.GetByID(t.Context(), user.User.ID)
					require.NoError(t, err)
					require.Equal(t, user, actual)
					links, err := repos.AccountGrants.ListByUser(t.Context(), user.User.ID)
					require.NoError(t, err)
					require.Equal(t, []AccountGrant{direct}, links)
				})
			}
		})
	}
}

func TestIdentityReplayRejectsInvalidTypedTransitions(t *testing.T) {
	user, team, origin, member, direct, _ := identityReplayValues()
	for _, mode := range []string{"empty-event", "mixed-event", "subject-replacement", "creation-replacement", "revision-regression", "revision-overflow", "zero-time", "control-id", "team-history-replacement", "interval-history-reuse", "membership-update", "grant-update", "origin-delete", "origin-reset", "origin-history-replacement"} {
		t.Run(mode, func(t *testing.T) {
			before, after := user, user
			after.Revision++
			after.User.UpdatedAt = after.User.UpdatedAt.Add(time.Hour)
			event := RecoveryIdentityEvent{User: &RecoveryUserChange{Before: &before, After: &after}}
			switch mode {
			case "empty-event":
				event = RecoveryIdentityEvent{}
			case "mixed-event":
				event.Team = &RecoveryTeamChange{After: &team}
			case "subject-replacement":
				after.User.Subject = "other-subject"
			case "creation-replacement":
				after.User.CreatedAt = after.User.CreatedAt.Add(time.Second)
			case "revision-regression":
				after.Revision = before.Revision
			case "revision-overflow":
				after.Revision = 1 << 63
			case "zero-time":
				after.User.CreatedAt = time.Time{}
			case "control-id":
				after.User.ID = "bad\x00id"
			case "team-history-replacement", "interval-history-reuse":
				old, next := team, team
				next.Revision++
				next.Team.Budget = team.Team.Budget.Clone()
				if mode == "team-history-replacement" {
					next.Team.Budget.HistoryID = "other"
				} else {
					next.Team.Budget.Interval = limits.IntervalWeek
				}
				event = RecoveryIdentityEvent{Team: &RecoveryTeamChange{Before: &old, After: &next}}
			case "membership-update":
				event = RecoveryIdentityEvent{Membership: &RecoveryMembershipChange{Before: &member, After: &member}}
			case "grant-update":
				event = RecoveryIdentityEvent{Grant: &RecoveryGrantChange{Before: &direct, After: &direct}}
			case "origin-delete":
				event = RecoveryIdentityEvent{Origin: &RecoveryOriginChange{Before: &origin}}
			case "origin-reset":
				next := origin
				next.InitializeAllowed = true
				event = RecoveryIdentityEvent{Origin: &RecoveryOriginChange{Before: &origin, After: &next}}
			case "origin-history-replacement":
				next := origin
				next.HistoryID = "fresh"
				event = RecoveryIdentityEvent{Origin: &RecoveryOriginChange{Before: &origin, After: &next}}
			}
			_, err := NewRecoveryTransition([]RecoveryIdentityEvent{event})
			require.ErrorIs(t, err, ErrReplayConflict)
		})
	}
	_, err := NewRecoveryTransition(make([]RecoveryIdentityEvent, 65))
	require.Error(t, err)
	var zero RecoveryTransition
	_, err = zero.Digest()
	require.Error(t, err)
	require.Error(t, ApplyRecoveryTransition(nil, nil, nil, zero))
}
