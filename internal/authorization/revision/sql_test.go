package revision_test

import (
	"database/sql"
	"errors"

	"sync"
	"testing"

	"github.com/agentstation/starport/internal/authorization/revision"
	"github.com/agentstation/starport/internal/identity"
	"github.com/agentstation/starport/internal/sqlstore"
)

func TestSQLRevisionRollsBackWithPolicy(t *testing.T) {
	db := revisionDatabase(t)
	revisions := revision.NewSQL(db, nil)
	before, err := revisions.Initialize(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), `CREATE TABLE policy_probe (id INTEGER PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	rejected := errors.New("reject candidate")
	err = revisions.Apply(t.Context(), func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(t.Context(), `INSERT INTO policy_probe (id) VALUES (1)`); err != nil {
			return err
		}
		return rejected
	})
	if !errors.Is(err, rejected) {
		t.Fatal(err)
	}
	after, err := revisions.Read(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Fatalf("failed write advanced revision: %+v", after)
	}
	var count int
	if err := db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM policy_probe`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("failed policy survived rollback")
	}
	if err := revisions.Apply(t.Context(), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(t.Context(), `INSERT INTO policy_probe (id) VALUES (1)`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	after, err = revisions.Read(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if after.Epoch != before.Epoch || after.Sequence != before.Sequence+1 {
		t.Fatalf("committed revision = %+v", after)
	}
}

func TestIdentityMutationsPublishSQLRevisions(t *testing.T) {
	db := revisionDatabase(t)
	revisions := revision.NewSQL(db, nil)
	previous, err := revisions.Initialize(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	advance := func() {
		t.Helper()
		next, err := revisions.Read(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if next.Epoch != previous.Epoch || next.Sequence != previous.Sequence+1 {
			t.Fatalf("revision did not advance once: before=%+v after=%+v", previous, next)
		}
		previous = next
	}
	repos, err := identity.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	user, err := repos.Users.Create(t.Context(), identity.User{ID: "user", Subject: "issuer:user"})
	if err != nil {
		t.Fatal(err)
	}
	advance()
	team, err := repos.Teams.Create(t.Context(), identity.Team{ID: "team", Name: "Team"})
	if err != nil {
		t.Fatal(err)
	}
	advance()
	_, err = repos.Memberships.Add(t.Context(), identity.Membership{UserID: "user", TeamID: "team"})
	if err != nil {
		t.Fatal(err)
	}
	advance()
	grant, err := repos.AccountGrants.Add(t.Context(), identity.AccountGrant{AccountID: "account", TeamID: "team"})
	if err != nil {
		t.Fatal(err)
	}
	advance()
	user.User.DisplayName = "Updated"
	user, err = repos.Users.Update(t.Context(), user.User, user.Revision)
	if err != nil {
		t.Fatal(err)
	}
	advance()
	team.Team.Name = "Updated"
	team, err = repos.Teams.Update(t.Context(), team.Team, team.Revision)
	if err != nil {
		t.Fatal(err)
	}
	advance()
	if err := repos.Memberships.Remove(t.Context(), "user", "team"); err != nil {
		t.Fatal(err)
	}
	advance()
	if err := repos.AccountGrants.Remove(t.Context(), grant); err != nil {
		t.Fatal(err)
	}
	advance()
	if err := repos.Users.Delete(t.Context(), "user", user.Revision); err != nil {
		t.Fatal(err)
	}
	advance()
	if err := repos.Teams.Delete(t.Context(), "team", team.Revision); err != nil {
		t.Fatal(err)
	}
	advance()
}

func TestIdentityDeleteFailurePreservesGrantsAndMembership(t *testing.T) {
	for _, owner := range []string{"user", "team"} {
		t.Run(owner, func(t *testing.T) {
			db := revisionDatabase(t)
			repos, err := identity.Open(db)
			if err != nil {
				t.Fatal(err)
			}
			user, err := repos.Users.Create(t.Context(), identity.User{ID: "user", Subject: "issuer:user"})
			if err != nil {
				t.Fatal(err)
			}
			team, err := repos.Teams.Create(t.Context(), identity.Team{ID: "team", Name: "Team"})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := repos.Memberships.Add(t.Context(), identity.Membership{UserID: "user", TeamID: "team"}); err != nil {
				t.Fatal(err)
			}
			grant := identity.AccountGrant{AccountID: "account"}
			if owner == "user" {
				grant.UserID = "user"
			} else {
				grant.TeamID = "team"
			}
			if _, err := repos.AccountGrants.Add(t.Context(), grant); err != nil {
				t.Fatal(err)
			}
			revisions := revision.NewSQL(db, nil)
			before, err := revisions.Initialize(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			statement := `CREATE TRIGGER refuse_owner_delete BEFORE DELETE ON users BEGIN SELECT RAISE(ABORT, 'owner deletion rejected'); END`
			if owner == "team" {
				statement = `CREATE TRIGGER refuse_owner_delete BEFORE DELETE ON teams BEGIN SELECT RAISE(ABORT, 'owner deletion rejected'); END`
			}
			if db.Dialect() == sqlstore.TypePostgres {
				if _, err := db.ExecContext(t.Context(), `CREATE FUNCTION reject_owner_delete() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'owner deletion rejected'; END; $$`); err != nil {
					t.Fatal(err)
				}
				statement = `CREATE TRIGGER refuse_owner_delete BEFORE DELETE ON users FOR EACH ROW EXECUTE FUNCTION reject_owner_delete()`
				if owner == "team" {
					statement = `CREATE TRIGGER refuse_owner_delete BEFORE DELETE ON teams FOR EACH ROW EXECUTE FUNCTION reject_owner_delete()`
				}
			}
			if _, err := db.ExecContext(t.Context(), statement); err != nil {
				t.Fatal(err)
			}
			if owner == "user" {
				err = repos.Users.Delete(t.Context(), "user", user.Revision)
			} else {
				err = repos.Teams.Delete(t.Context(), "team", team.Revision)
			}
			if err == nil {
				t.Fatal("fault did not stop owner deletion")
			}
			after, err := revisions.Read(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if after != before {
				t.Fatal("failed deletion advanced revision")
			}
			grants, err := repos.AccountGrants.ListByAccount(t.Context(), "account")
			if err != nil || len(grants) != 1 {
				t.Fatalf("grants = %v, %v", grants, err)
			}
			memberships, err := repos.Memberships.ListByUser(t.Context(), "user")
			if err != nil || len(memberships) != 1 {
				t.Fatalf("memberships = %v, %v", memberships, err)
			}
			drop := `DROP TRIGGER refuse_owner_delete`
			if db.Dialect() == sqlstore.TypePostgres {
				drop = `DROP TRIGGER refuse_owner_delete ON users`
				if owner == "team" {
					drop = `DROP TRIGGER refuse_owner_delete ON teams`
				}
			}
			if _, err := db.ExecContext(t.Context(), drop); err != nil {
				t.Fatal(err)
			}
			if owner == "user" {
				err = repos.Users.Delete(t.Context(), "user", user.Revision)
			} else {
				err = repos.Teams.Delete(t.Context(), "team", team.Revision)
			}
			if err != nil {
				t.Fatal(err)
			}
			grants, err = repos.AccountGrants.ListByAccount(t.Context(), "account")
			if err != nil || len(grants) != 0 {
				t.Fatalf("remaining grants = %v, %v", grants, err)
			}
			memberships, err = repos.Memberships.ListByUser(t.Context(), "user")
			if err != nil || len(memberships) != 0 {
				t.Fatalf("remaining memberships = %v, %v", memberships, err)
			}
		})
	}
}

func TestSQLConcurrentMutationsAdvanceDistinctRevisions(t *testing.T) {
	db := revisionDatabase(t)
	revisions := revision.NewSQL(db, nil)
	before, err := revisions.Initialize(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), `CREATE TABLE policy_probe (id INTEGER PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	var group sync.WaitGroup
	for id := range 8 {
		group.Go(func() {
			if err := revisions.Apply(t.Context(), func(tx *sql.Tx) error {
				_, err := tx.ExecContext(t.Context(), db.Bind(`INSERT INTO policy_probe (id) VALUES (?)`), id)
				return err
			}); err != nil {
				t.Error(err)
			}
		})
	}
	group.Wait()
	after, err := revisions.Read(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if after.Epoch != before.Epoch || after.Sequence != before.Sequence+8 {
		t.Fatalf("concurrent revision = %+v", after)
	}
	err = revisions.Apply(t.Context(), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(t.Context(), `INSERT INTO policy_probe (id) VALUES (0)`)
		return err
	})
	if err == nil {
		t.Fatal("duplicate policy insert succeeded")
	}
	final, err := revisions.Read(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if final != after {
		t.Fatal("duplicate policy insert advanced revision")
	}
}
