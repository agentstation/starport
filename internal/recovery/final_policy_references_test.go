package recovery

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/account"
	"github.com/agentstation/starport/internal/apikey"
	"github.com/agentstation/starport/internal/credentials"
	"github.com/agentstation/starport/internal/identity"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

func TestReferencesRefuseUntouchedUnknownPolicyFields(t *testing.T) {
	for _, mode := range []string{"account-field", "account-expiry", "gateway-key-field", "gateway-index-field", "gateway-collection-field", "gateway-initial-field", "credential-field", "credential-expiry", "sql-user-field", "sql-team-field", "sql-template-field"} {
		t.Run(mode, func(t *testing.T) {
			source, request, destination := backupBundleFixture(t)
			kv, transfer, _ := kvTransferStores(t, storage.StorageTypeBadger)
			source.KV = transfer
			accounts, err := account.Open(kv)
			require.NoError(t, err)
			_, err = accounts.Create(t.Context(), account.Account{ID: "owner", Name: "Owner", Active: true})
			require.NoError(t, err)
			keys, err := apikey.Open(kv)
			require.NoError(t, err)
			_, err = keys.CreateInitial(t.Context(), apikey.APIKey{ID: "key", Name: "Key", Hash: "retained-hash", AccountID: "owner", Active: true, Scopes: []string{"chat:write"}, CreatedAt: time.Now().UTC()})
			require.NoError(t, err)
			secrets, err := credentials.Open(kv)
			require.NoError(t, err)
			encrypted, err := source.Encryption.EncryptCredential(`{"api_key":"private-fixture-secret"}`)
			require.NoError(t, err)
			_, err = secrets.Create(t.Context(), credentials.ProviderKey{Scope: "owner", Provider: "provider", EncryptedCredential: encrypted})
			require.NoError(t, err)
			people, err := identity.Open(source.SQL)
			require.NoError(t, err)
			_, err = people.Users.Create(t.Context(), identity.User{ID: "user", Subject: "private-subject"})
			require.NoError(t, err)
			_, err = people.Teams.Create(t.Context(), identity.Team{ID: "team", Name: "Team"})
			require.NoError(t, err)
			templates, err := account.OpenTemplates(source.SQL)
			require.NoError(t, err)
			_, err = templates.Create(t.Context(), account.Template{ID: "template", Name: "Template"})
			require.NoError(t, err)
			if strings.HasPrefix(mode, "sql-") {
				query, update := `SELECT record FROM users WHERE id='user'`, `UPDATE users SET record=? WHERE id='user'`
				if mode == "sql-team-field" {
					query, update = `SELECT record FROM teams WHERE id='team'`, `UPDATE teams SET record=? WHERE id='team'`
				}
				if mode == "sql-template-field" {
					query, update = `SELECT record FROM account_templates WHERE id='template'`, `UPDATE account_templates SET record=? WHERE id='template'`
				}
				var value string
				require.NoError(t, source.SQL.QueryRowContext(t.Context(), query).Scan(&value))
				value = strings.TrimSuffix(value, "}") + `,"future_permission":true}`
				_, err = source.SQL.ExecContext(t.Context(), update, value)
				require.NoError(t, err)
			} else {
				prefix := map[string]string{"account-field": account.StoragePrefix, "account-expiry": account.StoragePrefix, "gateway-key-field": "identity:v1:key:", "gateway-index-field": "identity:v1:hash:", "gateway-collection-field": "identity:v1:collection", "gateway-initial-field": "identity:v1:initial", "credential-field": credentials.ProviderCredentialStoragePrefix, "credential-expiry": credentials.ProviderCredentialStoragePrefix}[mode]
				matches, err := kv.ScanWithPrefix(t.Context(), prefix, 0)
				require.NoError(t, err)
				require.Len(t, matches, 1)
				if strings.HasSuffix(mode, "expiry") {
					require.NoError(t, kv.ExpireAt(t.Context(), matches[0], time.Now().Add(time.Hour)))
				} else {
					value, err := kv.Get(t.Context(), matches[0])
					require.NoError(t, err)
					value = append(value[:len(value)-1], []byte(`,"future_permission":true}`)...)
					require.NoError(t, kv.Set(t.Context(), matches[0], value))
				}
			}
			manifest, err := BackupBundle(t.Context(), destination, source, request)
			require.NoError(t, err)
			digest, err := manifest.Digest()
			require.NoError(t, err)
			_, _, err = InspectBundleReferences(t.Context(), destination, digest, filepath.Dir(destination), source.Encryption)
			require.Error(t, err, "closed graph inspection must inspect records unchanged by replay")
			if err != nil {
				require.NotContains(t, err.Error(), "private-fixture-secret")
			}
		})
	}
}
