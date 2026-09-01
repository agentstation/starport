package identity

import (
	"context"
	"errors"
	"testing"

	"github.com/agentstation/starport/internal/limits"
	"github.com/agentstation/starport/internal/sqlstore"
)

// newTestRepositories opens an in-memory relational store, migrates it, and
// returns the identity repositories on it — the same composition the
// runtime builds, minus the file.
func newTestRepositories(t *testing.T) Repositories {
	t.Helper()
	db, err := sqlstore.Open(sqlstore.Config{Type: sqlstore.TypeSQLite})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	repositories, err := Open(db)
	if err != nil {
		t.Fatal(err)
	}
	return repositories
}

func TestOpenRefusesNilStore(t *testing.T) {
	if _, err := Open(nil); !errors.Is(err, ErrRepositoryRequired) {
		t.Fatalf("Open(nil) = %v, want ErrRepositoryRequired", err)
	}
}

// TestUserRepositoryCRUD proves the durable contract: what was created is
// what reads back — by id and by external subject — an update moves the
// revision without moving the subject, and a delete removes the row.
func TestUserRepositoryCRUD(t *testing.T) {
	repositories := newTestRepositories(t)
	users := repositories.Users
	ctx := context.Background()

	created, err := users.Create(ctx, User{
		ID:          "u-1",
		Subject:     "google:114380",
		Email:       "ada@example.com",
		DisplayName: "Ada",
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.Revision != 1 {
		t.Fatalf("create revision = %d", created.Revision)
	}
	if created.User.CreatedAt.IsZero() || created.User.UpdatedAt.IsZero() {
		t.Fatal("create must stamp timestamps")
	}

	byID, err := users.GetByID(ctx, "u-1")
	if err != nil {
		t.Fatal(err)
	}
	if byID.User.Email != "ada@example.com" || byID.User.DisplayName != "Ada" {
		t.Fatalf("read back %+v", byID.User)
	}

	bySubject, err := users.GetBySubject(ctx, "google:114380")
	if err != nil {
		t.Fatal(err)
	}
	if bySubject.User.ID != "u-1" {
		t.Fatalf("subject resolved to %q", bySubject.User.ID)
	}

	edited := byID.User
	edited.DisplayName = "Ada Lovelace"
	edited.Subject = "attacker:overwrite"
	updated, err := users.Update(ctx, edited, byID.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Revision != 2 {
		t.Fatalf("update revision = %d", updated.Revision)
	}
	if updated.User.Subject != "google:114380" {
		t.Fatal("update must never move the external subject")
	}
	if !updated.User.CreatedAt.Equal(byID.User.CreatedAt) {
		t.Fatal("update must preserve creation time")
	}

	listed, err := users.List(ctx, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 || listed[0].User.DisplayName != "Ada Lovelace" {
		t.Fatalf("list = %+v", listed)
	}

	if err := users.Delete(ctx, "u-1", updated.Revision); err != nil {
		t.Fatal(err)
	}
	if _, err := users.GetByID(ctx, "u-1"); !errors.Is(err, ErrUserNotFound) {
		t.Fatalf("get after delete = %v", err)
	}
}

// TestUserRepositoryConflicts proves both unique constraints and the
// revision guard surface as ErrUserConflict.
func TestUserRepositoryConflicts(t *testing.T) {
	repositories := newTestRepositories(t)
	users := repositories.Users
	ctx := context.Background()

	first, err := users.Create(ctx, User{ID: "u-1", Subject: "google:1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := users.Create(ctx, User{ID: "u-1", Subject: "google:other"}); !errors.Is(err, ErrUserConflict) {
		t.Fatalf("duplicate id = %v", err)
	}
	if _, err := users.Create(ctx, User{ID: "u-2", Subject: "google:1"}); !errors.Is(err, ErrUserConflict) {
		t.Fatalf("duplicate subject = %v", err)
	}
	if _, err := users.Update(ctx, first.User, first.Revision+7); !errors.Is(err, ErrUserConflict) {
		t.Fatalf("stale revision = %v", err)
	}
	if err := users.Delete(ctx, "u-1", first.Revision+7); !errors.Is(err, ErrUserConflict) {
		t.Fatalf("stale delete = %v", err)
	}
	if _, err := users.GetBySubject(ctx, "nobody:0"); !errors.Is(err, ErrUserNotFound) {
		t.Fatalf("unknown subject = %v", err)
	}
}

// TestTeamRepositoryCRUD proves the team contract mirrors the user one.
func TestTeamRepositoryCRUD(t *testing.T) {
	repositories := newTestRepositories(t)
	teams := repositories.Teams
	ctx := context.Background()

	created, err := teams.Create(ctx, Team{ID: "t-1", Name: "Platform"})
	if err != nil {
		t.Fatal(err)
	}
	if created.Revision != 1 {
		t.Fatalf("create revision = %d", created.Revision)
	}
	if _, err := teams.Create(ctx, Team{ID: "t-1", Name: "Twin"}); !errors.Is(err, ErrTeamConflict) {
		t.Fatalf("duplicate id = %v", err)
	}

	edited := created.Team
	edited.Name = "Platform Guild"
	updated, err := teams.Update(ctx, edited, created.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Revision != 2 || updated.Team.Name != "Platform Guild" {
		t.Fatalf("update = %+v", updated)
	}
	if _, err := teams.Update(ctx, edited, created.Revision); !errors.Is(err, ErrTeamConflict) {
		t.Fatalf("stale revision = %v", err)
	}

	listed, err := teams.List(ctx, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 {
		t.Fatalf("list = %+v", listed)
	}

	if err := teams.Delete(ctx, "t-1", 0); err != nil {
		t.Fatal(err)
	}
	if _, err := teams.GetByID(ctx, "t-1"); !errors.Is(err, ErrTeamNotFound) {
		t.Fatalf("get after delete = %v", err)
	}
}

// TestTeamRepositoryBudgetRoundTrip proves a team budget survives storage,
// clears on update, and refuses an invalid shape before it lands.
func TestTeamRepositoryBudgetRoundTrip(t *testing.T) {
	repositories := newTestRepositories(t)
	teams := repositories.Teams
	ctx := context.Background()

	budget := &limits.TeamBudget{Limit: 5_000_000_000, Interval: limits.IntervalMonth}
	if _, err := teams.Create(ctx, Team{ID: "t-1", Name: "Platform", Budget: budget}); err != nil {
		t.Fatal(err)
	}

	stored, err := teams.GetByID(ctx, "t-1")
	if err != nil {
		t.Fatal(err)
	}
	if stored.Team.Budget == nil || *stored.Team.Budget != *budget {
		t.Fatalf("stored budget = %+v, want %+v", stored.Team.Budget, budget)
	}

	cleared := stored.Team
	cleared.Budget = nil
	updated, err := teams.Update(ctx, cleared, stored.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Team.Budget != nil {
		t.Fatalf("cleared budget = %+v", updated.Team.Budget)
	}

	invalid := Team{ID: "t-2", Name: "Broke",
		Budget: &limits.TeamBudget{Limit: -1, Interval: limits.IntervalDay}}
	if _, err := teams.Create(ctx, invalid); !errors.Is(err, limits.ErrInvalidBudgetLimit) {
		t.Fatalf("invalid budget create = %v", err)
	}
}

// TestMembershipRepository proves a membership ties two existing rows, is
// listed from both ends, refuses a duplicate, and never outlives its team.
func TestMembershipRepository(t *testing.T) {
	repositories := newTestRepositories(t)
	ctx := context.Background()

	if _, err := repositories.Users.Create(ctx, User{ID: "u-1", Subject: "google:1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := repositories.Users.Create(ctx, User{ID: "u-2", Subject: "google:2"}); err != nil {
		t.Fatal(err)
	}
	team, err := repositories.Teams.Create(ctx, Team{ID: "t-1", Name: "Platform"})
	if err != nil {
		t.Fatal(err)
	}

	memberships := repositories.Memberships
	if _, err := memberships.Add(ctx, Membership{UserID: "ghost", TeamID: "t-1"}); !errors.Is(err, ErrUserNotFound) {
		t.Fatalf("ghost user = %v", err)
	}
	if _, err := memberships.Add(ctx, Membership{UserID: "u-1", TeamID: "ghost"}); !errors.Is(err, ErrTeamNotFound) {
		t.Fatalf("ghost team = %v", err)
	}

	added, err := memberships.Add(ctx, Membership{UserID: "u-1", TeamID: "t-1"})
	if err != nil {
		t.Fatal(err)
	}
	if added.CreatedAt.IsZero() {
		t.Fatal("add must stamp creation time")
	}
	if _, err := memberships.Add(ctx, Membership{UserID: "u-1", TeamID: "t-1"}); !errors.Is(err, ErrMembershipConflict) {
		t.Fatalf("duplicate membership = %v", err)
	}
	if _, err := memberships.Add(ctx, Membership{UserID: "u-2", TeamID: "t-1"}); err != nil {
		t.Fatal(err)
	}

	byTeam, err := memberships.ListByTeam(ctx, "t-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(byTeam) != 2 || byTeam[0].UserID != "u-1" || byTeam[1].UserID != "u-2" {
		t.Fatalf("by team = %+v", byTeam)
	}
	byUser, err := memberships.ListByUser(ctx, "u-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(byUser) != 1 || byUser[0].TeamID != "t-1" {
		t.Fatalf("by user = %+v", byUser)
	}

	if err := memberships.Remove(ctx, "u-1", "t-1"); err != nil {
		t.Fatal(err)
	}
	if err := memberships.Remove(ctx, "u-1", "t-1"); !errors.Is(err, ErrMembershipNotFound) {
		t.Fatalf("second remove = %v", err)
	}

	// Deleting the team sweeps the remaining membership with it.
	if err := repositories.Teams.Delete(ctx, "t-1", team.Revision); err != nil {
		t.Fatal(err)
	}
	remaining, err := memberships.ListByUser(ctx, "u-2")
	if err != nil {
		t.Fatal(err)
	}
	if len(remaining) != 0 {
		t.Fatalf("membership outlived its team: %+v", remaining)
	}
}
