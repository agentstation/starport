package app

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json/v2"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/account"
	"github.com/agentstation/starport/internal/apikey"
	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/identity"
	"github.com/agentstation/starport/internal/limits"
	"github.com/agentstation/starport/internal/limits/reservation"
	"github.com/agentstation/starport/internal/server"
	"github.com/agentstation/starport/internal/sqlstore"
	"github.com/agentstation/starport/internal/storage"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/require"
)

// Each measured authority method delegates to one synchronous native EVAL.
// SQL tracing counts actual driver queries without recording arguments or rows.
type budgetOperationCounts struct {
	reads, clocks, writes atomic.Int64
	queries, approvals    atomic.Int64
}

type budgetOperationSample struct {
	KVReads      int64 `json:"budget_read_eval"`
	KVClocks     int64 `json:"budget_clock_eval"`
	KVWrites     int64 `json:"budget_write_eval"`
	SQLQueries   int64 `json:"sql_queries"`
	SQLApprovals int64 `json:"sql_approval_queries"`
}

func (c *budgetOperationCounts) sample() budgetOperationSample {
	return budgetOperationSample{c.reads.Load(), c.clocks.Load(), c.writes.Load(), c.queries.Load(), c.approvals.Load()}
}

func (s budgetOperationSample) since(previous budgetOperationSample) budgetOperationSample {
	return budgetOperationSample{s.KVReads - previous.KVReads, s.KVClocks - previous.KVClocks, s.KVWrites - previous.KVWrites, s.SQLQueries - previous.SQLQueries, s.SQLApprovals - previous.SQLApprovals}
}

func (c *budgetOperationCounts) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	c.queries.Add(1)
	if strings.Contains(data.SQL, "FROM catalog_recovery") {
		c.approvals.Add(1)
	}
	return ctx
}

func (*budgetOperationCounts) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

type measuredBudgetStore struct {
	storage.KVStore
	storage.IncarnationProvider
	storage.PubSubProvider
	counts *budgetOperationCounts
}

func (s *measuredBudgetStore) BindIncarnation(ctx context.Context, id string) (storage.IncarnationStore, error) {
	bound, err := s.IncarnationProvider.BindIncarnation(ctx, id)
	if err != nil {
		return nil, err
	}
	return &measuredBudgetAuthority{IncarnationStore: bound, timed: bound.(storage.TimeBoundStore), counts: s.counts}, nil
}

type measuredBudgetAuthority struct {
	storage.IncarnationStore
	timed  storage.TimeBoundStore
	counts *budgetOperationCounts
}

func (s *measuredBudgetAuthority) ReadWithLifetime(ctx context.Context, key string, bound int) ([]byte, time.Duration, error) {
	if strings.HasPrefix(key, "budget:v1:") {
		s.counts.reads.Add(1)
	}
	return s.timed.ReadWithLifetime(ctx, key, bound)
}

func (s *measuredBudgetAuthority) AuthorityTime(ctx context.Context) (time.Time, error) {
	s.counts.clocks.Add(1)
	return s.timed.AuthorityTime(ctx)
}

func (s *measuredBudgetAuthority) ReadBatchWithLifetime(ctx context.Context, keys []string, bound int) ([]storage.LifetimeValue, error) {
	for _, key := range keys {
		if strings.HasPrefix(key, "budget:v1:") {
			s.counts.reads.Add(1)
			break
		}
	}
	return s.timed.ReadBatchWithLifetime(ctx, keys, bound)
}

func (s *measuredBudgetAuthority) CompareAndSwapInWindow(ctx context.Context, mutations []storage.CompareAndSwapMutation, window storage.TimeWindow) error {
	for _, mutation := range mutations {
		if strings.HasPrefix(mutation.Key, "budget:v1:") {
			s.counts.writes.Add(1)
			break
		}
	}
	return s.timed.CompareAndSwapInWindow(ctx, mutations, window)
}

func (c *budgetOperationCounts) instrument(t *testing.T, options *buildOptions) {
	t.Helper()
	openKV, openDB := options.factories.openStorage, options.factories.openSQL
	options.factories.openStorage = func(cfg storage.Config) (storage.KVStore, error) {
		store, err := openKV(cfg)
		if err != nil {
			return nil, err
		}
		return &measuredBudgetStore{KVStore: store, IncarnationProvider: store.(storage.IncarnationProvider), PubSubProvider: store.(storage.PubSubProvider), counts: c}, nil
	}
	options.factories.openSQL = func(cfg config.StorageConfig) (*sqlstore.DB, error) {
		db, err := openDB(cfg)
		if err != nil {
			return nil, err
		}
		connection, err := pgx.ParseConfig(cfg.SQL.Postgres.URL)
		require.NoError(t, err)
		require.NoError(t, db.DB.Close())
		connection.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
		connection.Tracer = c
		db.DB = sql.OpenDB(stdlib.GetConnector(*connection))
		db.SetMaxOpenConns(16)
		db.SetMaxIdleConns(4)
		db.SetConnMaxLifetime(30 * time.Minute)
		require.NoError(t, db.Ping(t.Context()))
		return db, nil
	}
}

func TestProductionBudgetBackendOperations(t *testing.T) {
	valkey, postgres := os.Getenv("TEST_VALKEY_URL"), os.Getenv("TEST_POSTGRES_URL")
	if valkey == "" || postgres == "" {
		t.Skip("UNVERIFIED: TEST_VALKEY_URL and TEST_POSTGRES_URL are required")
	}
	var counts budgetOperationCounts
	var calls atomic.Int64
	atDispatch := make(chan budgetOperationSample, 2)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer sk-test-key" {
			t.Error("unexpected provider destination or credential")
			http.Error(w, "unexpected provider request", http.StatusBadRequest)
			return
		}
		calls.Add(1)
		atDispatch <- counts.sample()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"operation-fixture","object":"chat.completion","model":"gpt-4o-mini","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":8,"completion_tokens":3,"total_tokens":11,"prompt_tokens_details":{"cached_tokens":0}}}`)
	}))
	t.Cleanup(upstream.Close)
	admin, err := sqlstore.Open(config.StorageConfig{SQL: config.SQLConfig{Mode: sqlstore.TypePostgres, Postgres: config.SQLPostgresConfig{URL: postgres}}}.RuntimeSQL())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, admin.Close()) })
	schemaName := "budget_operations_" + strings.ToLower(rand.Text())
	schema := pgx.Identifier{schemaName}.Sanitize()
	_, err = admin.ExecContext(t.Context(), "CREATE SCHEMA "+schema)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, err := admin.ExecContext(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
		require.NoError(t, err)
	})
	parsed, err := url.Parse(postgres)
	require.NoError(t, err)
	query := parsed.Query()
	query.Set("search_path", schemaName)
	parsed.RawQuery = query.Encode()
	cfg := budgetFleetConfig(t, budgetFleetInput{Valkey: valkey, Postgres: parsed.String(), Deployment: "budget-operations-" + rand.Text(), Upstream: upstream.URL})
	seed, err := openStorage(cfg.RuntimeStorage())
	require.NoError(t, err)
	approveTestFleet(t, cfg, seed)
	keys, err := apikey.Open(seed)
	require.NoError(t, err)
	_, err = keys.Create(t.Context(), testAPIKey())
	require.NoError(t, err)
	require.NoError(t, seed.Close())
	t.Cleanup(func() {
		cleanup, err := openStorage(cfg.RuntimeStorage())
		require.NoError(t, err)
		defer func() { require.NoError(t, cleanup.Close()) }()
		keys, err := cleanup.ScanWithPrefix(context.Background(), "", 0)
		require.NoError(t, err)
		require.NoError(t, cleanup.BatchDelete(context.Background(), keys))
	})
	var deps server.Dependencies
	application, err := New(cfg, func(options *buildOptions) {
		counts.instrument(t, options)
		build := options.factories.newServer
		options.factories.newServer = func(settings *server.Config, dependencies server.Dependencies) (httpRuntime, error) {
			deps = dependencies
			return build(settings, dependencies)
		}
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, application.Close(context.Background())) })
	budgets := func() *limits.Limits {
		return &limits.Limits{Tokens: &limits.Budget{Limit: 1_000_000, Interval: limits.IntervalDay}, Spend: &limits.Budget{Limit: 1_000_000_000, Interval: limits.IntervalDay}}
	}
	_, err = deps.Accounts.Create(t.Context(), account.Account{ID: "operations-account", Name: "Operations account", Limits: budgets(), Active: true, CreatedAt: time.Now().UTC()})
	require.NoError(t, err)
	team, err := deps.Identity.Teams.Create(t.Context(), identity.Team{ID: "operations-team", Name: "Operations team", Budget: &limits.TeamBudget{Limit: 1_000_000_000, Interval: limits.IntervalDay}})
	require.NoError(t, err)
	key := testAPIKey()
	key.ID, key.AccountID, key.TeamID, key.Limits = "operations-key", "operations-account", team.Team.ID, budgets()
	digest := sha256.Sum256([]byte(performanceGatewayKey))
	key.Hash = hex.EncodeToString(digest[:])
	_, err = deps.APIKeys.Create(t.Context(), key)
	require.NoError(t, err)
	gateway := httptest.NewServer(application.httpServer.(*server.Server).Router())
	t.Cleanup(gateway.Close)
	fixture := &performanceFixture{application: application, gateway: gateway, client: &http.Client{Timeout: 15 * time.Second}}
	require.Equal(t, http.StatusOK, budgetDispatch(t, fixture))
	<-atDispatch // Populate permission and pricing state before the measured request.
	before := counts.sample()
	require.Equal(t, http.StatusOK, budgetDispatch(t, fixture))
	admitted := <-atDispatch
	finished := counts.sample()
	admission, settlement := admitted.since(before), finished.since(admitted)
	require.Positive(t, admission.KVReads)
	require.Positive(t, admission.KVClocks)
	require.Positive(t, admission.KVWrites)
	require.Positive(t, settlement.KVReads)
	require.Positive(t, settlement.KVWrites)
	require.Equal(t, 2*admission.KVWrites, admission.SQLApprovals, "each budget write checks independent approval before and after its native transaction")
	require.Equal(t, 2*settlement.KVWrites, settlement.SQLApprovals)
	require.Equal(t, admission.SQLApprovals, admission.SQLQueries, "warm admission must not reload unrelated SQL state")
	require.Equal(t, settlement.SQLApprovals, settlement.SQLQueries)
	require.LessOrEqual(t, finished.since(before).KVReads, int64(5), "group meter and history reads within each atomic attempt")
	require.EqualValues(t, 2, calls.Load())
	attempts, err := application.store.ScanWithPrefix(t.Context(), "budget:v1:attempt:", 100)
	require.NoError(t, err)
	require.Len(t, attempts, 2)
	for _, key := range attempts {
		data, err := application.store.Get(t.Context(), key)
		require.NoError(t, err)
		var record reservation.Record
		require.NoError(t, json.Unmarshal(data, &record))
		require.Equal(t, reservation.Settled, record.State)
		require.Len(t, record.Bindings, 5)
		require.NotNil(t, record.NanoUSD)
		for _, binding := range record.Bindings {
			window, err := application.budget.ledger.Window(t.Context(), binding.Rule.Meter, record.AdmittedAt)
			require.NoError(t, err)
			require.Zero(t, window.Reserved)
			if binding.Rule.Meter.Dimension == limits.DimensionTokens {
				require.EqualValues(t, 22, window.Consumed)
			} else {
				require.Equal(t, 2**record.NanoUSD, window.Consumed)
			}
		}
	}
	evidence, err := json.Marshal(map[string]budgetOperationSample{"warm_admission": admission, "settlement": settlement, "total": finished.since(before)})
	require.NoError(t, err)
	t.Logf("BUDGET_BACKEND_OPERATIONS %s", evidence)
}
