package revision_test

import (
	"testing"

	"github.com/agentstation/starport/internal/account"
	"github.com/agentstation/starport/internal/apikey"
	"github.com/agentstation/starport/internal/authorization/revision"
	"github.com/agentstation/starport/internal/repotest"
	"github.com/agentstation/starport/internal/storage"
)

func TestRepositoriesPublishEveryAuthorizationMutation(t *testing.T) {
	repotest.Run(t, func(t *testing.T, store storage.KVStore) {
		revisions := revision.NewKV(store, nil)
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
				t.Fatalf("mutation did not publish exactly one revision: before=%+v after=%+v", previous, next)
			}
			previous = next
		}
		accounts, err := account.Open(store)
		if err != nil {
			t.Fatal(err)
		}
		governing, err := accounts.Create(t.Context(), account.Account{ID: "tenant", Name: "Tenant", Active: true})
		if err != nil {
			t.Fatal(err)
		}
		advance()
		governing.Account.CredentialStrategy = account.StrategyBYOKOnly
		governing, err = accounts.Update(t.Context(), governing.Account, governing.Revision)
		if err != nil {
			t.Fatal(err)
		}
		advance()
		keys, err := apikey.Open(store)
		if err != nil {
			t.Fatal(err)
		}
		key, err := keys.Create(t.Context(), apikey.APIKey{ID: "key", Name: "Key", Hash: "hash", AccountID: "tenant", Scopes: []string{"chat:write"}, Active: true})
		if err != nil {
			t.Fatal(err)
		}
		advance()
		key.APIKey.Active = false
		key, err = keys.Update(t.Context(), key.APIKey, key.Revision)
		if err != nil {
			t.Fatal(err)
		}
		advance()
		if err := keys.Delete(t.Context(), key.APIKey.ID, key.Revision); err != nil {
			t.Fatal(err)
		}
		advance()
		if err := accounts.Delete(t.Context(), governing.Account.ID, governing.Revision); err != nil {
			t.Fatal(err)
		}
		advance()
	})
}
