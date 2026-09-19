package authorization

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/account"
	"github.com/agentstation/starport/internal/apikey"
	"github.com/agentstation/starport/internal/authorization/revision"
	"github.com/agentstation/starport/internal/identity"
	"github.com/agentstation/starport/internal/repotest"
	"github.com/agentstation/starport/internal/sqlstore"
	"github.com/agentstation/starport/internal/storage"
)

func TestRepositorySourceRealStoresAndLocalMutation(t *testing.T) {
	repotest.Run(t, func(t *testing.T, store storage.KVStore) {
		db, err := sqlstore.Open(sqlstore.Config{Type: sqlstore.TypeSQLite, SQLite: sqlstore.SQLiteConfig{Path: filepath.Join(t.TempDir(), "identity.db")}})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := db.Close(); err != nil {
				t.Error(err)
			}
		})
		if err := db.Migrate(t.Context()); err != nil {
			t.Fatal(err)
		}
		kv, sql := revision.NewKV(store, nil), revision.NewSQL(db, nil)
		ks, err := kv.Initialize(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		ss, err := sql.Initialize(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		kf, sf := NewFence("kv", ks.Epoch), NewFence("sql", ss.Epoch)
		authorities, err := NewAuthoritySet(kf, sf)
		if err != nil {
			t.Fatal(err)
		}
		accounts, err := account.Open(store, account.WithAuthorizationFence(kf.BeginMutation))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := accounts.EnsureDefault(t.Context()); err != nil {
			t.Fatal(err)
		}
		keys, err := apikey.Open(store, apikey.WithAuthorizationFence(kf.BeginMutation))
		if err != nil {
			t.Fatal(err)
		}
		identities, err := identity.Open(db, identity.WithAuthorizationFence(sf.BeginMutation))
		if err != nil {
			t.Fatal(err)
		}
		team, err := identities.Teams.Create(t.Context(), identity.Team{ID: "team", Name: "team"})
		if err != nil {
			t.Fatal(err)
		}
		key, err := keys.Create(t.Context(), apikey.APIKey{ID: "key", Name: "test-key", Scopes: []string{"chat:write"}, Hash: "test-hash", TeamID: team.Team.ID, Active: true})
		if err != nil {
			t.Fatal(err)
		}
		source, err := NewRepositorySource(RepositorySources{Keys: keys, Accounts: accounts, Teams: identities.Teams, KV: kv, SQL: sql, KVAuthority: "kv", SQLAuthority: "sql"}, authorities, func() (time.Time, bool) { return time.Now(), true }, 5*time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		cache, err := NewCache(source, authorities, cacheTestLimits(), source.clock, testElapsedClock(source.clock))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(cache.Close)
		old, err := cache.Resolve(t.Context(), Identity{Subject: key.APIKey.Hash})
		if err != nil {
			t.Fatal(err)
		}
		if old.Team() == nil || old.Team().Team.ID != team.Team.ID {
			t.Fatal("team dependency missing")
		}
		if err := identities.Teams.Delete(t.Context(), team.Team.ID, team.Revision); err != nil {
			t.Fatal(err)
		}
		if err := old.Permit().Check(time.Now(), true); !errors.Is(err, ErrWithdrawn) {
			t.Fatalf("deleted team still admitted: %v", err)
		}
		if _, err := cache.Resolve(t.Context(), Identity{Subject: key.APIKey.Hash}); !errors.Is(err, identity.ErrTeamNotFound) {
			t.Fatalf("missing team = %v", err)
		}
		key.APIKey.TeamID = ""
		if _, err := keys.Update(t.Context(), key.APIKey, key.Revision); err != nil {
			t.Fatal(err)
		}
		recovered, err := cache.Resolve(t.Context(), Identity{Subject: key.APIKey.Hash})
		if err != nil {
			t.Fatal(err)
		}
		if recovered.Team() != nil {
			t.Fatal("explicit team removal retained old dependency")
		}
		// A warm lookup must not consult any durable source.
		source.keys = keyFunc(func(context.Context, string) (apikey.Record, error) {
			t.Error("warm durable key read")
			return apikey.Record{}, ErrUnavailable
		})
		if _, err := cache.Resolve(t.Context(), Identity{Subject: key.APIKey.Hash}); err != nil {
			t.Fatal(err)
		}
	})
}
