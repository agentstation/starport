package app

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/apikey"
	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/limits"
	"github.com/agentstation/starport/internal/recovery"
	"github.com/agentstation/starport/internal/sqlstore"
	"github.com/agentstation/starport/internal/storage"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"
)

type recoveryGatewayFixture struct {
	input   budgetFleetInput
	config  *config.Config
	store   storage.KVStore
	db      *sqlstore.DB
	witness *recovery.Witness
	calls   atomic.Int64
}

// newRecoveryGatewayFixture explicitly approves an isolated test deployment.
// It owns one namespace, one SQL schema, and a local provider with no paid calls.
func newRecoveryGatewayFixture(t *testing.T, budgets bool) *recoveryGatewayFixture {
	t.Helper()
	valkey, postgres := os.Getenv("TEST_VALKEY_URL"), os.Getenv("TEST_POSTGRES_URL")
	if valkey == "" || postgres == "" {
		t.Skip("UNVERIFIED: gateway history barriers require native Valkey and PostgreSQL")
	}
	fixture := &recoveryGatewayFixture{}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer sk-test-key" {
			t.Error("unexpected fixture destination or credential")
			http.Error(w, "unexpected fixture request", http.StatusBadRequest)
			return
		}
		fixture.calls.Add(1)
		budgetReply(w)
	}))
	t.Cleanup(upstream.Close)
	admin, err := sqlstore.Open(sqlstore.Config{Type: sqlstore.TypePostgres, Postgres: sqlstore.PostgresConfig{URL: postgres}})
	require.NoError(t, err)
	schema := "gateway_recovery_" + strings.ToLower(rand.Text())
	quoted := pgx.Identifier{schema}.Sanitize()
	_, err = admin.ExecContext(t.Context(), "CREATE SCHEMA "+quoted)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, err := admin.ExecContext(context.Background(), "DROP SCHEMA "+quoted+" CASCADE")
		require.NoError(t, err)
		require.NoError(t, admin.Close())
	})
	parsed, err := url.Parse(postgres)
	require.NoError(t, err)
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	fixture.input = budgetFleetInput{Valkey: valkey, Postgres: parsed.String(), Deployment: "gateway-recovery-" + rand.Text(), Upstream: upstream.URL}
	fixture.config = budgetFleetConfig(t, fixture.input)
	fixture.store, err = storage.Open(fixture.config.RuntimeStorage())
	require.NoError(t, err)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		keys, err := fixture.store.ScanWithPrefix(ctx, "", 10000)
		require.NoError(t, err)
		if len(keys) != 0 {
			require.NoError(t, fixture.store.BatchDelete(ctx, keys))
		}
		require.NoError(t, fixture.store.Close())
	})
	// Fresh physical initialization is a separate contract. This shared fixture
	// explicitly approves only its unused deployment namespace and SQL schema.
	approveTestFleet(t, fixture.config, fixture.store)
	keys, err := apikey.Open(fixture.store)
	require.NoError(t, err)
	key := testAPIKey()
	sum := sha256.Sum256([]byte(performanceGatewayKey))
	key.Hash = hex.EncodeToString(sum[:])
	key.Limits = nil
	if budgets {
		key.Limits = &limits.Limits{Tokens: &limits.Budget{Limit: 200_000, Interval: limits.IntervalDay}}
	}
	_, err = keys.Create(t.Context(), key)
	require.NoError(t, err)
	fixture.db, err = sqlstore.Open(fixture.config.Storage.RuntimeSQL())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, fixture.db.Close()) })
	fixture.witness, err = recovery.New(fixture.db)
	require.NoError(t, err)
	// A positive normal constructor creates the deployment's ordinary repositories.
	// The fresh refusal cases start only after this control closes cleanly.
	application, err := New(fixture.config)
	require.NoError(t, err)
	require.NoError(t, application.Close(t.Context()))
	return fixture
}

func recoveryGatewayRecords(t *testing.T, store storage.KVStore) map[string][]byte {
	t.Helper()
	const maximumKeys = 10000
	keys, err := store.ScanWithPrefix(t.Context(), "", maximumKeys+1)
	require.NoError(t, err)
	// The adapter stops at either the final native cursor or its result limit.
	// Fewer than the requested limit proves this scan reached the final cursor.
	require.Less(t, len(keys), maximumKeys+1, "native census exceeded its complete bounded fixture limit")
	result := make(map[string][]byte, len(keys))
	for _, key := range keys {
		body, err := store.Get(t.Context(), key)
		if errors.Is(err, storage.ErrNotFound) {
			continue
		}
		require.NoError(t, err)
		result[key] = body
	}
	return result
}

func TestRecoveryFreshGatewayRejectsUnapprovedHistoryBeforeEffects(t *testing.T) {
	for _, mode := range []string{"missing-sql-approval", "missing-native-approval", "stale-native-epoch", "replacement-incarnation"} {
		t.Run(mode, func(t *testing.T) {
			fixture := newRecoveryGatewayFixture(t, false)
			input, target := fixture.input, fixture.store
			switch mode {
			case "missing-sql-approval":
				_, err := fixture.db.ExecContext(t.Context(), fixture.db.Bind("DELETE FROM catalog_recovery WHERE deployment_id = ?"), input.Deployment)
				require.NoError(t, err)
			case "missing-native-approval":
				require.NoError(t, target.Delete(t.Context(), "recovery:authority:v1"))
			case "stale-native-epoch":
				original, err := target.Get(t.Context(), "recovery:authority:v1")
				require.NoError(t, err)
				approval, err := fixture.witness.Approved(t.Context(), input.Deployment)
				require.NoError(t, err)
				closed, err := fixture.witness.Close(t.Context(), approval)
				require.NoError(t, err)
				_, err = fixture.witness.ApproveAuthority(t.Context(), target.(storage.IncarnationProvider), closed, approval.BackendID, "fixture-independent-reviewed-history", "fixture-next-epoch")
				require.NoError(t, err)
				// Fault injection restores only the actual prior record, not invented permission.
				require.NoError(t, target.Set(t.Context(), "recovery:authority:v1", original))
			case "replacement-incarnation":
				input.Valkey = os.Getenv("TEST_VALKEY_REPLACEMENT_URL")
				if input.Valkey == "" {
					t.Skip("UNVERIFIED: distinct native replacement Valkey is required")
				}
				cfg := *fixture.config
				cfg.Storage.Valkey.URL = input.Valkey
				replacement, err := storage.Open(cfg.RuntimeStorage())
				require.NoError(t, err)
				t.Cleanup(func() {
					keys, err := replacement.ScanWithPrefix(context.Background(), "", 10000)
					require.NoError(t, err)
					require.NoError(t, replacement.BatchDelete(context.Background(), keys))
					require.NoError(t, replacement.Close())
				})
				oldID, err := target.(storage.IncarnationProvider).ObserveIncarnation(t.Context())
				require.NoError(t, err)
				newID, err := replacement.(storage.IncarnationProvider).ObserveIncarnation(t.Context())
				require.NoError(t, err)
				require.NotEqual(t, oldID, newID)
				copyBudgetFleetSnapshot(t, target, replacement)
				target = replacement
			}
			before := recoveryGatewayRecords(t, target)
			beforeApproval, beforeApprovalError := fixture.witness.Current(t.Context(), input.Deployment)
			child, reply := startRecoveryGatewayProcess(t, input)
			require.False(t, reply.Started)
			require.False(t, reply.OtherError, "refusal must identify recovery authority")
			if mode == "replacement-incarnation" {
				require.True(t, reply.Incarnation)
			} else {
				require.True(t, reply.Closed || reply.Conflict)
			}
			<-child.done
			require.NoError(t, child.waitErr)
			require.True(t, maps.EqualFunc(before, recoveryGatewayRecords(t, target), bytes.Equal), "failed fresh construction cannot repair or mutate retained native history")
			afterApproval, afterApprovalError := fixture.witness.Current(t.Context(), input.Deployment)
			require.Equal(t, beforeApproval, afterApproval)
			require.Equal(t, errors.Is(beforeApprovalError, recovery.ErrClosed), errors.Is(afterApprovalError, recovery.ErrClosed))
			if beforeApprovalError == nil {
				require.NoError(t, afterApprovalError)
			}
			require.Zero(t, fixture.calls.Load())
		})
	}
}

func TestRecoveryOldWarmedGatewayRefusesAfterKnownClosure(t *testing.T) {
	replacementURL := os.Getenv("TEST_VALKEY_REPLACEMENT_URL")
	if replacementURL == "" {
		t.Skip("UNVERIFIED: old reachable primary proof requires a distinct replacement Valkey")
	}
	for _, budgets := range []bool{true, false} {
		t.Run(map[bool]string{true: "required-budget", false: "confirmed-no-budget"}[budgets], func(t *testing.T) {
			fixture := newRecoveryGatewayFixture(t, budgets)
			child, started := startRecoveryGatewayProcess(t, fixture.input)
			require.True(t, started.Started)
			status, body := recoveryGatewayRequest(t, started.BaseURL)
			require.Equal(t, http.StatusOK, status, body)
			require.EqualValues(t, 1, fixture.calls.Load())
			policy := child.exchange(t, "policy")
			if budgets {
				require.Equal(t, 1, policy.Rules)
			} else {
				require.Zero(t, policy.Rules)
			}
			approval, err := fixture.witness.Approved(t.Context(), fixture.input.Deployment)
			require.NoError(t, err)
			closed, err := fixture.witness.Close(t.Context(), approval)
			require.NoError(t, err)
			observed := child.exchange(t, "observe")
			require.True(t, observed.Closed)
			require.True(t, observed.Reachable, "old backend stays reachable during admission refusal")
			recoveryGatewayHealth(t, started.BaseURL)
			status, body = recoveryGatewayRequest(t, started.BaseURL)
			require.Equal(t, http.StatusServiceUnavailable, status, body)
			require.EqualValues(t, 1, fixture.calls.Load(), "known closure must prevent provider dispatch without terminating the gateway")
			// Copy only this controlled namespace while the old process stays alive.
			// No request is active during this explicit test-owner replacement.
			replacementConfig := *fixture.config
			replacementConfig.Storage.Valkey.URL = replacementURL
			replacement, err := storage.Open(replacementConfig.RuntimeStorage())
			require.NoError(t, err)
			t.Cleanup(func() {
				keys, err := replacement.ScanWithPrefix(context.Background(), "", 10000)
				require.NoError(t, err)
				if len(keys) != 0 {
					require.NoError(t, replacement.BatchDelete(context.Background(), keys))
				}
				require.NoError(t, replacement.Close())
			})
			newBackend := replacement.(storage.IncarnationProvider)
			newIdentity, err := newBackend.ObserveIncarnation(t.Context())
			require.NoError(t, err)
			require.NotEqual(t, approval.BackendID, newIdentity)
			copyBudgetFleetSnapshot(t, fixture.store, replacement)
			_, err = fixture.witness.ApproveAuthority(t.Context(), newBackend, closed, newIdentity, "fixture-independent-reviewed-history", "fixture-new-epoch")
			require.NoError(t, err)
			observed = child.exchange(t, "observe")
			require.True(t, observed.Conflict)
			require.True(t, observed.Reachable)
			recoveryGatewayHealth(t, started.BaseURL)
			status, body = recoveryGatewayRequest(t, started.BaseURL)
			require.Equal(t, http.StatusServiceUnavailable, status, body)
			require.EqualValues(t, 1, fixture.calls.Load())
			child.finish(t)
		})
	}
}
