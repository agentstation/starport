package identity

import (
	"errors"
	"github.com/agentstation/starport/internal/authorization/revision"
	"strings"
	"testing"
)

func TestAuthorizationScalarReadsNeverTruncate(t *testing.T) {
	repos := newTestRepositories(t)
	db := repos.Users.(*userRepository).db
	authority := revision.NewSQL(db, nil)
	if _, err := authority.Initialize(t.Context()); err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"epoch\x00tail", "epoch\x00" + strings.Repeat("x", 1024)} {
		if _, err := db.ExecContext(t.Context(), `UPDATE authorization_revision SET epoch = ? WHERE id = 1`, value); err != nil {
			t.Fatal(err)
		}
		stamp, err := authority.Read(t.Context())
		if len(value) > 256 {
			if !errors.Is(err, revision.ErrCorrupt) {
				t.Fatalf("oversized epoch accepted: %+v, %v", stamp, err)
			}
		} else if err != nil || stamp.Epoch != value {
			t.Fatalf("epoch was truncated: %+v, %v", stamp, err)
		}
	}
	if _, err := db.ExecContext(t.Context(), `UPDATE authorization_revision SET epoch = 'epoch' WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	if _, err := repos.Users.Create(t.Context(), User{ID: "person", Subject: "person-subject", Email: "person@example.com", DisplayName: "Person"}); err != nil {
		t.Fatal(err)
	}
	if _, err := repos.AccountGrants.Add(t.Context(), AccountGrant{AccountID: "original", UserID: "person"}); err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"account\x00tail", "account\x00" + strings.Repeat("x", 1024)} {
		if _, err := db.ExecContext(t.Context(), `UPDATE account_grants SET account_id = ? WHERE user_id = 'person'`, value); err != nil {
			t.Fatal(err)
		}
		got, err := repos.AccountGrants.ResolveAccount(t.Context(), "person", "")
		if len(value) > maxIDLength {
			if !errors.Is(err, ErrMissingID) {
				t.Fatalf("oversized selection accepted: %q, %v", got, err)
			}
		} else if err != nil || got != value {
			t.Fatalf("account was truncated: %q, %v", got, err)
		}
	}
}
