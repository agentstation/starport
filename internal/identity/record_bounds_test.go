package identity

import (
	"crypto/rand"
	"errors"
	"github.com/agentstation/starport/internal/policyrecord"
	"github.com/agentstation/starport/internal/sqlstore"
	"os"
	"strings"
	"testing"
)

func TestIdentityRecordBoundsBeforeDecode(t *testing.T) {
	cfg := sqlstore.Config{Type: sqlstore.TypeSQLite}
	if address := os.Getenv("TEST_IDENTITY_POSTGRES_URL"); address != "" {
		cfg = sqlstore.Config{Type: sqlstore.TypePostgres, Postgres: sqlstore.PostgresConfig{URL: address}}
	}
	db, err := sqlstore.Open(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	suffix := rand.Text()
	userID, teamID, subject := "bound-user-"+suffix, "bound-team-"+suffix, "bound-subject-"+suffix
	repos, err := Open(db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repos.Users.Create(t.Context(), User{ID: userID, Subject: subject, Email: "bound@example.com", DisplayName: "Bound"}); err != nil {
		t.Fatal(err)
	}
	if _, err := repos.Teams.Create(t.Context(), Team{ID: teamID, Name: "Bound"}); err != nil {
		t.Fatal(err)
	}
	oversized := strings.Repeat("é", policyrecord.MaxBytes/2+1)
	if _, err := db.ExecContext(t.Context(), db.Bind(`UPDATE users SET record = ? WHERE id = ?`), oversized, userID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), db.Bind(`UPDATE teams SET record = ? WHERE id = ?`), oversized, teamID); err != nil {
		t.Fatal(err)
	}
	if _, err := repos.Users.GetByID(t.Context(), userID); !errors.Is(err, policyrecord.ErrTooLarge) {
		t.Fatalf("user ID = %v", err)
	}
	if _, err := repos.Users.GetBySubject(t.Context(), subject); !errors.Is(err, policyrecord.ErrTooLarge) {
		t.Fatalf("user subject = %v", err)
	}
	if _, err := repos.Teams.GetByID(t.Context(), teamID); !errors.Is(err, policyrecord.ErrTooLarge) {
		t.Fatalf("team = %v", err)
	}
	if _, err := repos.Users.GetByID(t.Context(), "missing"); !errors.Is(err, ErrUserNotFound) {
		t.Fatalf("missing user = %v", err)
	}
}
