package recovery

import (
	"github.com/agentstation/starport/internal/identity"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/account"
	"github.com/agentstation/starport/internal/apikey"
	"github.com/agentstation/starport/internal/limits"
	"github.com/agentstation/starport/internal/limits/reservation"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

func inspectReferenceFixture(t *testing.T, source BundleSources, request BundleRequest, destination string) (ReferenceReport, error) {
	t.Helper()
	manifest, err := BackupBundle(t.Context(), destination, source, request)
	require.NoError(t, err)
	digest, err := manifest.Digest()
	require.NoError(t, err)
	_, result, err := InspectBundleReferences(t.Context(), destination, digest, filepath.Dir(destination), source.Encryption)
	return result, err
}

func TestBackupGatewayKeyReferences(t *testing.T) {
	for _, mode := range []string{"valid", "expired-key", "missing-index", "wrong-count", "orphan-index", "deleted-initial", "missing-owners", "expiring-record"} {
		t.Run(mode, func(t *testing.T) {
			source, request, destination := backupBundleFixture(t)
			kv, transfer, _ := kvTransferStores(t, storage.StorageTypeBadger)
			source.KV = transfer
			accounts, err := account.Open(kv)
			require.NoError(t, err)
			owner, err := accounts.Create(t.Context(), account.Account{ID: "owner", Name: "Owner", Active: true})
			require.NoError(t, err)
			keys, err := apikey.Open(kv)
			require.NoError(t, err)
			key := apikey.APIKey{ID: "key", Name: "test-key", Hash: "opaque-test-hash", AccountID: "owner", Scopes: []string{"chat:write"}, Active: true, CreatedAt: time.Now().Add(-time.Hour)}
			if mode == "expired-key" {
				expires := time.Now().Add(-time.Minute)
				key.ExpiresAt = &expires
				key.Active = false
			}
			if mode == "missing-owners" {
				key.TeamID = "deleted-team"
			}
			created, err := keys.CreateInitial(t.Context(), key)
			require.NoError(t, err)
			for _, prefix := range []string{"identity:v1:hash:", "identity:v1:key:"} {
				ids, err := kv.ScanWithPrefix(t.Context(), prefix, 0)
				require.NoError(t, err)
				require.Len(t, ids, 1)
				if mode == "missing-index" && strings.Contains(prefix, ":hash:") {
					require.NoError(t, kv.Delete(t.Context(), ids[0]))
				}
				if mode == "orphan-index" && strings.Contains(prefix, ":hash:") {
					data, err := kv.Get(t.Context(), ids[0])
					require.NoError(t, err)
					require.NoError(t, kv.Set(t.Context(), prefix+"foreign", data))
				}
				if mode == "expiring-record" && strings.Contains(prefix, ":key:") {
					require.NoError(t, kv.ExpireAt(t.Context(), ids[0], time.Now().Add(time.Hour)))
				}
			}
			if mode == "wrong-count" {
				require.NoError(t, kv.Set(t.Context(), "identity:v1:collection", []byte(`{"schema_version":1,"revision":2,"count":2}`)))
			}
			if mode == "deleted-initial" {
				require.NoError(t, keys.Delete(t.Context(), key.ID, created.Revision))
			}
			if mode == "missing-owners" {
				require.NoError(t, accounts.Delete(t.Context(), "owner", owner.Revision))
			}
			report, err := inspectReferenceFixture(t, source, request, destination)
			switch mode {
			case "valid", "expired-key":
				require.NoError(t, err)
				require.Equal(t, apikey.RecoveryReport{Keys: 1, HashIndexes: 1}, report.GatewayKeys)
			case "deleted-initial":
				require.NoError(t, err)
				require.Equal(t, apikey.RecoveryReport{MissingInitialKeys: 1}, report.GatewayKeys)
			case "missing-owners":
				require.NoError(t, err)
				require.Equal(t, apikey.RecoveryReport{Keys: 1, HashIndexes: 1, MissingAccounts: 1, MissingTeams: 1}, report.GatewayKeys)
			default:
				require.Error(t, err)
			}
		})
	}
}

func TestBackupBudgetReferencesPreserveUncertainCapacity(t *testing.T) {
	for _, mode := range []string{"uncertain", "missing-window", "expiring-attempt", "wrong-attempt-key", "correction", "missing-correction"} {
		t.Run(mode, func(t *testing.T) {
			source, request, destination := backupBundleFixture(t)
			kv, transfer, _ := kvTransferStores(t, storage.StorageTypeBadger)
			source.KV = transfer
			budgets, err := reservation.Open(kv.(storage.TimeBoundStore))
			require.NoError(t, err)
			meter := reservation.Meter{Scope: limits.ScopeAccount, Holder: "owner", Dimension: limits.DimensionSpend, Interval: limits.IntervalDay}
			require.NoError(t, budgets.EstablishWindow(t.Context(), meter, time.Now(), 0, reservation.History{ID: "history", Proof: "fixture-history"}))
			attempt := reservation.Attempt{ID: "attempt", RequestID: "request", AccountID: "owner", KeyID: "key", OfferingID: "provider/model", CatalogGeneration: "generation", Operation: "chat", Rules: []reservation.Rule{{Meter: meter, Limit: 1000, PolicyRevision: "policy", HistoryID: "history"}}, Valuation: reservation.Valuation{Version: reservation.ArithmeticVersion, Components: []reservation.Component{{Unit: "output", Price: reservation.Price{USD: "0.000000001", PerUnits: 1}}}}, Bound: reservation.Quantities{"output": 100}}
			_, err = budgets.Reserve(t.Context(), attempt)
			require.NoError(t, err)
			require.NoError(t, budgets.Begin(t.Context(), attempt.ID))
			require.NoError(t, budgets.MarkUncertain(t.Context(), attempt.ID, "response-lost"))
			if mode == "correction" || mode == "missing-correction" {
				prior, err := budgets.Inspect(t.Context(), attempt.ID)
				require.NoError(t, err)
				_, err = budgets.Correct(t.Context(), attempt.ID, reservation.Correction{ID: "repair", ExpectedBinding: reservation.CorrectionBinding(*prior), Actor: "operator", EvidenceReference: "provider/no-charge", Reason: "provider-confirmed", Evidence: reservation.Evidence{ID: "no-charge", NoCharge: true}})
				require.NoError(t, err)
			}
			for _, kind := range []string{"meter", "attempt", "correction"} {
				ids, err := kv.ScanWithPrefix(t.Context(), "budget:v1:"+kind+":", 0)
				require.NoError(t, err)
				if len(ids) == 0 {
					continue
				}
				if (mode == "missing-window" && kind == "meter") || (mode == "missing-correction" && kind == "correction") {
					require.NoError(t, kv.Delete(t.Context(), ids[0]))
				}
				if mode == "expiring-attempt" && kind == "attempt" {
					require.NoError(t, kv.ExpireAt(t.Context(), ids[0], time.Now().Add(time.Hour)))
				}
				if mode == "wrong-attempt-key" && kind == "attempt" {
					data, err := kv.Get(t.Context(), ids[0])
					require.NoError(t, err)
					require.NoError(t, kv.Set(t.Context(), "budget:v1:attempt:foreign", data))
				}
			}
			before, err := budgets.Inspect(t.Context(), attempt.ID)
			if mode != "expiring-attempt" {
				require.NoError(t, err)
			}
			report, err := inspectReferenceFixture(t, source, request, destination)
			if mode != "uncertain" && mode != "correction" {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			if mode == "uncertain" {
				require.EqualValues(t, 1, report.HeldReservations)
			} else {
				require.Zero(t, report.HeldReservations)
			}
			after, err := budgets.Inspect(t.Context(), attempt.ID)
			require.NoError(t, err)
			require.Equal(t, before, after, "verification cannot settle, refund, or repeat an attempt")
		})
	}
}

func TestBackupReportsUnknownAccountAndKeyHistory(t *testing.T) {
	for _, mode := range []string{"intact", "missing", "different"} {
		t.Run(mode, func(t *testing.T) {
			source, request, destination := backupBundleFixture(t)
			kv, transfer, _ := kvTransferStores(t, storage.StorageTypeBadger)
			source.KV = transfer
			accounts, err := account.Open(kv)
			require.NoError(t, err)
			_, err = accounts.Create(t.Context(), account.Account{ID: "owner", Name: "Owner", Active: true, Limits: &limits.Limits{Spend: &limits.Budget{Limit: 1000, Interval: limits.IntervalDay}}})
			require.NoError(t, err)
			keys, err := apikey.Open(kv)
			require.NoError(t, err)
			_, err = keys.CreateInitial(t.Context(), apikey.APIKey{ID: "key", Name: "Key", Hash: "opaque-hash", Scopes: []string{"chat:write"}, AccountID: "owner", Active: true, Limits: &limits.Limits{Tokens: &limits.Budget{Limit: 1000, Interval: limits.IntervalDay}}})
			require.NoError(t, err)
			histories, err := kv.ScanWithPrefix(t.Context(), "budget:v1:history:", 0)
			require.NoError(t, err)
			require.Len(t, histories, 2)
			if mode == "missing" {
				require.NoError(t, kv.BatchDelete(t.Context(), histories))
			}
			// Simulate a separately restored history that does not match the retained policy.
			if mode == "different" {
				require.NoError(t, kv.BatchDelete(t.Context(), histories))
				budgets, err := reservation.Open(kv.(storage.TimeBoundStore))
				require.NoError(t, err)
				for _, meter := range []reservation.Meter{{Scope: limits.ScopeAccount, Holder: "owner", Dimension: limits.DimensionSpend, Interval: limits.IntervalDay}, {Scope: limits.ScopeKey, Holder: "key", Dimension: limits.DimensionTokens, Interval: limits.IntervalDay}} {
					require.NoError(t, budgets.EstablishWindow(t.Context(), meter, time.Now(), 100, reservation.History{ID: "other-history", Proof: "reconciliation"}))
				}
			}
			report, err := inspectReferenceFixture(t, source, request, destination)
			require.NoError(t, err)
			expected := int64(0)
			if mode != "intact" {
				expected = 1
			}
			require.Equal(t, expected, report.UnknownAccountBudgetHistories)
			require.Equal(t, expected, report.GatewayKeys.UnknownBudgetHistories)
			remaining, err := kv.ScanWithPrefix(t.Context(), "budget:v1:history:", 0)
			require.NoError(t, err)
			if mode == "missing" {
				require.Empty(t, remaining, "verification must not create budget capacity")
			}
		})
	}
}

func TestBackupTeamBudgetOrigins(t *testing.T) {
	for _, mode := range []string{"fresh", "consumed", "initialized", "deleted", "missing-origin", "invalid-flag", "receipt-unconsumed", "receipt-wrong-origin"} {
		t.Run(mode, func(t *testing.T) {
			source, request, destination := backupBundleFixture(t)
			kv, transfer, _ := kvTransferStores(t, storage.StorageTypeBadger)
			source.KV = transfer
			people, err := identity.Open(source.SQL)
			require.NoError(t, err)
			team, err := people.Teams.Create(t.Context(), identity.Team{ID: "team", Name: "Team", Budget: &limits.TeamBudget{Limit: 1000, Interval: limits.IntervalDay}})
			require.NoError(t, err)
			history := people.Teams.(identity.TeamBudgetHistory)
			if mode == "consumed" {
				claimed, err := history.ClaimBudgetHistory(t.Context(), team.Team.ID, team.Team.Budget.Interval, team.Team.Budget.HistoryID)
				require.NoError(t, err)
				require.True(t, claimed)
			}
			if mode == "initialized" || mode == "deleted" || strings.HasPrefix(mode, "receipt-") {
				budgets, err := reservation.Open(kv.(storage.TimeBoundStore))
				require.NoError(t, err)
				require.NoError(t, budgets.InitializeTeamHistory(t.Context(), history, team.Team.ID, team.Team.Budget.Interval, team.Team.Budget.HistoryID))
			}
			if mode == "deleted" {
				require.NoError(t, people.Teams.Delete(t.Context(), team.Team.ID, team.Revision))
			}
			query := map[string]string{"missing-origin": "DELETE FROM team_budget_origins", "invalid-flag": "UPDATE team_budget_origins SET initialize_allowed=2", "receipt-unconsumed": "UPDATE team_budget_origins SET initialize_allowed=1", "receipt-wrong-origin": "UPDATE team_budget_origins SET history_id='different'"}[mode]
			if query != "" {
				_, err := source.SQL.ExecContext(t.Context(), query)
				require.NoError(t, err)
			}
			report, err := inspectReferenceFixture(t, source, request, destination)
			if query != "" {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.EqualValues(t, 1, report.Identity.BudgetOrigins)
			expected := int64(0)
			if mode == "fresh" || mode == "consumed" {
				expected = 1
			}
			require.Equal(t, expected, report.Identity.UnknownBudgetHistories)
			if mode == "deleted" {
				require.Zero(t, report.Identity.Teams)
			}
			var allowed int
			require.NoError(t, source.SQL.QueryRowContext(t.Context(), "SELECT initialize_allowed FROM team_budget_origins WHERE team_id='team'").Scan(&allowed))
			if mode == "fresh" {
				require.Equal(t, 1, allowed, "verification must not consume an initialization grant")
			} else {
				require.Zero(t, allowed)
			}
		})
	}
}
