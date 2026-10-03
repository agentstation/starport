package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/agentstation/starmap"
	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/stretchr/testify/require"

	"github.com/agentstation/starport/internal/apikey"
	runtimecatalog "github.com/agentstation/starport/internal/catalog"
	"github.com/agentstation/starport/internal/server"
	"github.com/agentstation/starport/internal/sqlstore"
	"github.com/agentstation/starport/internal/storage"
)

const promotionGatewayKey = "promotion-fixture-gateway-key"

func TestCatalogPromotionKeepsRequestPathInMemory(t *testing.T) {
	factories := explicitTestFactories(t)
	shared, err := factories.openStorage(storage.Config{})
	require.NoError(t, err)
	keys, err := apikey.Open(shared)
	require.NoError(t, err)
	key := testAPIKey()
	key.ID = "STARPORT_PROMOTION_TEST"
	hash := sha256.Sum256([]byte(promotionGatewayKey))
	key.Hash = hex.EncodeToString(hash[:])
	_, err = keys.Create(t.Context(), key)
	require.NoError(t, err)

	// The gateway reads one counted view of the shared store. Its catalog
	// runtime and the promotion command read the store directly, so the count
	// holds only the calls that the gateway itself makes.
	counted := &countingStore{KVStore: shared, bound: shared.(storage.TimeBoundStore)}
	factories.openStorage = func(storage.Config) (storage.KVStore, error) { return counted, nil }
	var gatewayCatalog *lifecycleCatalogRuntime
	factories.openCatalog = func(ctx context.Context, _ storage.KVStore, _ *sqlstore.DB, _ runtimecatalog.Settings, _ runtimecatalog.DeploymentLookup) (catalogRuntime, error) {
		gatewayCatalog, err = newLifecycleCatalogRuntime(ctx, shared)
		return gatewayCatalog, err
	}
	var httpServer *server.Server
	factories.newServer = func(cfg *server.Config, deps server.Dependencies) (httpRuntime, error) {
		var err error
		httpServer, err = server.New(cfg, deps)
		return httpServer, err
	}
	cfg := validProductionConfig(t)
	cfg.Storage.Mode = storage.StorageTypeValkey
	cfg.Storage.Valkey.URL = "redis://127.0.0.1:6379"
	cfg.Storage.Valkey.MaxConnections = 10
	useSharedRecipeWithLocalTestStores(t, cfg, &factories)
	application, err := New(cfg, withRuntimeFactories(factories))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, application.Close(context.Background())) })

	serve := func() {
		t.Helper()
		ready := httptest.NewRecorder()
		httpServer.Router().ServeHTTP(ready, httptest.NewRequest(http.MethodGet, "/health/ready", nil))
		require.Equal(t, http.StatusOK, ready.Code, ready.Body.String())
		require.True(t, application.admissionReady())
		models := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
		request.Header.Set("Authorization", "Bearer "+promotionGatewayKey)
		httpServer.Router().ServeHTTP(models, request)
		require.Equal(t, http.StatusOK, models.Code, models.Body.String())
		require.Contains(t, models.Body.String(), "gpt-4o-mini")
	}
	// The first request fills the in-memory authorization cache.
	serve()
	require.Positive(t, counted.calls.Load(), "the gateway reads storage through the counted view")
	counted.calls.Store(0)

	promoted := gatewayCatalog.state
	promoted.GenerationID += "-promoted"
	promoted.Sequence++
	receipt := runtimecatalog.PromotionReceipt{
		OperationID: "promote-1", DeploymentID: cfg.EffectivePaths().DeploymentID, Status: runtimecatalog.PromotionApplied,
		Previous:      runtimecatalog.PromotionIdentity{GenerationID: gatewayCatalog.state.GenerationID, Checksum: gatewayCatalog.state.PayloadChecksum, Revision: 4},
		Promoted:      runtimecatalog.PromotionIdentity{GenerationID: promoted.GenerationID, Checksum: promoted.PayloadChecksum, Revision: 5},
		InertRemovals: []catalogs.CatalogRemovalTarget{}, Actor: "operator", CreatedAt: time.Now().UTC(),
	}
	generation, err := starmap.EmbeddedGeneration()
	require.NoError(t, err)
	var recordedLeader string
	requests := &scriptedPromotionRequests{leader: "gateway-a", during: serve}
	requests.submit = func(request runtimecatalog.PromotionRequest) (runtimecatalog.PromotionRecord, error) {
		return pendingRecord(request), nil
	}
	requests.read = func(operationID string) (runtimecatalog.PromotionRecord, bool, error) {
		record := pendingRecord(requests.submitted[0])
		record.Status, record.Receipt = runtimecatalog.PromotionApplied, &receipt
		return record, true, nil
	}
	commandFactories := factories
	commandFactories.openStorage = func(storage.Config) (storage.KVStore, error) { return uncloseableStore{shared}, nil }
	commandFactories.openCatalog = func(context.Context, storage.KVStore, *sqlstore.DB, runtimecatalog.Settings, runtimecatalog.DeploymentLookup) (catalogRuntime, error) {
		return nil, errors.New("the promotion command must not open a catalog runtime")
	}
	commandFactories.openPromotionRequests = func(_ context.Context, store storage.KVStore, _ *sqlstore.DB, deployment string) (promotionRequests, error) {
		require.Equal(t, cfg.EffectivePaths().DeploymentID, deployment)
		require.Equal(t, uncloseableStore{shared}, store)
		return requests, nil
	}
	got, err := PromoteCatalogBaseline(t.Context(), cfg, runtimecatalog.PromotionRequest{OperationID: "promote-1", ExpectedRevision: 4}, time.Minute,
		func(record runtimecatalog.PromotionRecord, leader string) {
			require.Equal(t, runtimecatalog.PromotionPending, record.Status)
			recordedLeader = leader
		}, withRuntimeFactories(commandFactories))
	require.NoError(t, err)
	require.Equal(t, receipt, got)
	require.Equal(t, "gateway-a", recordedLeader, "the command names the lease holder that executes the request")
	require.Len(t, requests.submitted, 1)
	request := requests.submitted[0]
	require.Equal(t, uint64(4), request.ExpectedRevision)
	require.NotEmpty(t, request.Actor, "the command names the operator account")
	require.Equal(t, generation.Manifest.GenerationID, request.PackagedGenerationID, "the request names the packaged baseline of this binary")
	require.Positive(t, requests.reads, "the command waits for the outcome")

	// The gateway replays the promoted head and keeps answering from memory.
	require.NoError(t, application.activateRuntimeState(t.Context(), runtimecatalog.Candidate{State: promoted}))
	require.Equal(t, promoted.GenerationID, application.catalog.Current().GenerationID())
	serve()
	require.Zero(t, counted.calls.Load(), "request admission read storage during the promotion or the replay")
}

func TestCatalogPromotionRefusesLocalDeployment(t *testing.T) {
	cfg := validProductionConfig(t)
	opened := func(storage.Config) (storage.KVStore, error) {
		return nil, errors.New("a local deployment must not open storage for promotion")
	}
	refuse := func(options *buildOptions) {
		options.factories.openStorage = opened
		options.factories.openReadOnlyStorage = opened
	}
	_, err := PromoteCatalogBaseline(t.Context(), cfg, runtimecatalog.PromotionRequest{OperationID: "local-1"}, time.Minute, nil, refuse)
	require.ErrorIs(t, err, runtimecatalog.ErrBaselinePromotionFleetOnly)
	_, err = CatalogBaselineStatus(t.Context(), cfg, refuse)
	require.ErrorIs(t, err, runtimecatalog.ErrBaselinePromotionFleetOnly)
	cfg.Storage.Mode = storage.StorageTypeValkey
	_, err = CatalogBaselineStatus(t.Context(), cfg, refuse)
	require.ErrorIs(t, err, runtimecatalog.ErrBaselinePromotionFleetOnly, "shared key-value storage without PostgreSQL is not a fleet")
	cfg.Storage.SQL.Mode = sqlstore.TypePostgres
	_, err = PromoteCatalogBaseline(t.Context(), cfg, runtimecatalog.PromotionRequest{OperationID: "bad id"}, time.Minute, nil, refuse)
	require.Error(t, err)
	require.NotErrorIs(t, err, runtimecatalog.ErrBaselinePromotionFleetOnly, "an invalid operation ID fails before storage opens")
	_, err = PromoteCatalogBaseline(t.Context(), cfg, runtimecatalog.PromotionRequest{OperationID: "fleet-1"}, 0, nil, refuse)
	require.ErrorContains(t, err, "wait must be positive")
}

func TestCatalogPromotionWaitTimeoutNamesTheContinuation(t *testing.T) {
	for _, leader := range []string{"gateway-a", ""} {
		requests := &scriptedPromotionRequests{leader: leader}
		requests.submit = func(request runtimecatalog.PromotionRequest) (runtimecatalog.PromotionRecord, error) {
			return pendingRecord(request), nil
		}
		var recorded int
		_, err := awaitPromotion(t.Context(), requests, runtimecatalog.PromotionRequest{OperationID: "wait-1"}, 20*time.Millisecond,
			func(_ runtimecatalog.PromotionRecord, holder string) {
				recorded++
				require.Equal(t, leader, holder)
			})
		require.ErrorIs(t, err, ErrBaselinePromotionPending)
		require.Equal(t, 1, recorded, "the command reports the lease holder once")
		require.ErrorContains(t, err, "Request wait-1 stays recorded until "+promotionExpiry.Format(time.RFC3339))
		require.ErrorContains(t, err, "Run the command again with the same operation ID to continue the wait")
		if leader == "" {
			require.ErrorContains(t, err, "No gateway leads the fleet. Start a gateway with the new binary")
		} else {
			require.NotContains(t, err.Error(), "No gateway leads")
		}
	}
}

func TestCatalogPromotionReportsAnotherPendingOperation(t *testing.T) {
	requests := &scriptedPromotionRequests{leader: "gateway-a"}
	conflict := errors.New("promotion request first-1 is pending until 2026-10-03T12:00:00Z. Wait for it with its operation ID, or retry after its lifetime ends")
	requests.submit = func(runtimecatalog.PromotionRequest) (runtimecatalog.PromotionRecord, error) {
		return runtimecatalog.PromotionRecord{}, conflict
	}
	_, err := awaitPromotion(t.Context(), requests, runtimecatalog.PromotionRequest{OperationID: "second-1"}, time.Minute,
		func(runtimecatalog.PromotionRecord, string) { t.Fatal("a refused write records no request") })
	require.ErrorIs(t, err, conflict)
	require.Zero(t, requests.reads)
}

func TestCatalogPromotionResubmitsAnExpiredRequest(t *testing.T) {
	applied := runtimecatalog.PromotionReceipt{OperationID: "expired-1", Status: runtimecatalog.PromotionApplied}
	requests := &scriptedPromotionRequests{leader: "gateway-a"}
	requests.submit = func(request runtimecatalog.PromotionRequest) (runtimecatalog.PromotionRecord, error) {
		record := pendingRecord(request)
		if len(requests.submitted) > 1 {
			// The leader executes the written request again and returns the durable receipt.
			record.Status, record.Receipt = runtimecatalog.PromotionApplied, &applied
		}
		return record, nil
	}
	requests.read = func(string) (runtimecatalog.PromotionRecord, bool, error) {
		return runtimecatalog.PromotionRecord{}, false, nil
	}
	receipt, err := awaitPromotion(t.Context(), requests, runtimecatalog.PromotionRequest{OperationID: "expired-1"}, time.Minute, nil)
	require.NoError(t, err)
	require.Equal(t, applied, receipt)
	require.Len(t, requests.submitted, 2, "a missing record is written again with the same operation ID")
	require.Equal(t, requests.submitted[0], requests.submitted[1])
}

func TestCatalogBaselineStatusOpensReadOnlyObserver(t *testing.T) {
	factories := explicitTestFactories(t)
	cfg := validProductionConfig(t)
	cfg.Storage.Mode = storage.StorageTypeValkey
	cfg.Storage.Valkey.URL = "redis://127.0.0.1:6379"
	useSharedRecipeWithLocalTestStores(t, cfg, &factories)
	shared, err := factories.openStorage(storage.Config{})
	require.NoError(t, err)
	factories.openStorage = func(storage.Config) (storage.KVStore, error) {
		return nil, errors.New("baseline-status must not open writable storage")
	}
	factories.openReadOnlyStorage = func(storage.Config) (storage.KVStore, error) { return uncloseableStore{shared}, nil }
	factories.openCatalog = func(context.Context, storage.KVStore, *sqlstore.DB, runtimecatalog.Settings, runtimecatalog.DeploymentLookup) (catalogRuntime, error) {
		return nil, errors.New("baseline-status must not open a gateway catalog runtime")
	}
	expected := runtimecatalog.BaselineReport{DeploymentID: cfg.EffectivePaths().DeploymentID, HeadRevision: 7, Promotable: true}
	observer := &reportingObserver{report: expected}
	factories.openBaselineObserver = func(_ context.Context, store storage.KVStore, _ *sqlstore.DB, settings runtimecatalog.Settings, _ runtimecatalog.DeploymentLookup) (baselineObserver, error) {
		require.Equal(t, uncloseableStore{shared}, store)
		require.Equal(t, catalogSettings(cfg), settings, "the observer receives the gateway settings and moves the directories itself")
		return observer, nil
	}
	report, err := CatalogBaselineStatus(t.Context(), cfg, withRuntimeFactories(factories))
	require.NoError(t, err)
	require.Equal(t, expected, report)
	require.True(t, observer.closed, "the command closes the observer and removes its directory")
}

// promotionExpiry is the record lifetime end that scripted requests report.
var promotionExpiry = time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)

func pendingRecord(request runtimecatalog.PromotionRequest) runtimecatalog.PromotionRecord {
	return runtimecatalog.PromotionRecord{
		OperationID: request.OperationID, ExpectedRevision: request.ExpectedRevision, PackagedGenerationID: request.PackagedGenerationID,
		Actor: request.Actor, Status: runtimecatalog.PromotionPending, Expires: promotionExpiry,
	}
}

// scriptedPromotionRequests stands in for the shared request record.
// The catalog package proves the record and the leader execution.
type scriptedPromotionRequests struct {
	submit    func(runtimecatalog.PromotionRequest) (runtimecatalog.PromotionRecord, error)
	read      func(string) (runtimecatalog.PromotionRecord, bool, error)
	leader    string
	during    func()
	submitted []runtimecatalog.PromotionRequest
	reads     int
}

func (r *scriptedPromotionRequests) Submit(_ context.Context, request runtimecatalog.PromotionRequest) (runtimecatalog.PromotionRecord, error) {
	r.submitted = append(r.submitted, request)
	if r.during != nil {
		r.during()
	}
	return r.submit(request)
}

func (r *scriptedPromotionRequests) Read(_ context.Context, operationID string) (runtimecatalog.PromotionRecord, bool, error) {
	r.reads++
	if r.during != nil {
		r.during()
	}
	return r.read(operationID)
}

func (r *scriptedPromotionRequests) Leader(context.Context) (string, error) {
	return r.leader, nil
}

// reportingObserver stands in for the lease-free fleet catalog runtime.
type reportingObserver struct {
	report runtimecatalog.BaselineReport
	closed bool
}

func (o *reportingObserver) BaselineReport() (runtimecatalog.BaselineReport, error) {
	return o.report, nil
}

func (o *reportingObserver) Close(context.Context) error {
	o.closed = true
	return nil
}

// uncloseableStore lets the command close its storage handle without closing the gateway's store.
type uncloseableStore struct{ storage.KVStore }

func (uncloseableStore) Close() error { return nil }

// countingStore counts every call that reaches the store through it.
type countingStore struct {
	storage.KVStore
	bound storage.TimeBoundStore
	calls atomic.Int64
}

func (s *countingStore) Get(ctx context.Context, key string) ([]byte, error) {
	s.calls.Add(1)
	return s.KVStore.Get(ctx, key)
}

func (s *countingStore) GetBounded(ctx context.Context, key string, maxBytes int) ([]byte, error) {
	s.calls.Add(1)
	return s.KVStore.GetBounded(ctx, key, maxBytes)
}

func (s *countingStore) Set(ctx context.Context, key string, value []byte) error {
	s.calls.Add(1)
	return s.KVStore.Set(ctx, key, value)
}

func (s *countingStore) Delete(ctx context.Context, key string) error {
	s.calls.Add(1)
	return s.KVStore.Delete(ctx, key)
}

func (s *countingStore) Exists(ctx context.Context, key string) (bool, error) {
	s.calls.Add(1)
	return s.KVStore.Exists(ctx, key)
}

func (s *countingStore) SetWithTTL(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	s.calls.Add(1)
	return s.KVStore.SetWithTTL(ctx, key, value, ttl)
}

func (s *countingStore) GetTTL(ctx context.Context, key string) (time.Duration, error) {
	s.calls.Add(1)
	return s.KVStore.GetTTL(ctx, key)
}

func (s *countingStore) ExpireAt(ctx context.Context, key string, expireAt time.Time) error {
	s.calls.Add(1)
	return s.KVStore.ExpireAt(ctx, key, expireAt)
}

func (s *countingStore) Increment(ctx context.Context, key string, delta int64) (int64, error) {
	s.calls.Add(1)
	return s.KVStore.Increment(ctx, key, delta)
}

func (s *countingStore) Decrement(ctx context.Context, key string, delta int64) (int64, error) {
	s.calls.Add(1)
	return s.KVStore.Decrement(ctx, key, delta)
}

func (s *countingStore) CompareAndSwap(ctx context.Context, key string, old, newValue []byte) error {
	s.calls.Add(1)
	return s.KVStore.CompareAndSwap(ctx, key, old, newValue)
}

func (s *countingStore) CompareAndSwapBatch(ctx context.Context, mutations []storage.CompareAndSwapMutation) error {
	s.calls.Add(1)
	return s.KVStore.CompareAndSwapBatch(ctx, mutations)
}

func (s *countingStore) BatchGet(ctx context.Context, keys []string) (map[string][]byte, error) {
	s.calls.Add(1)
	return s.KVStore.BatchGet(ctx, keys)
}

func (s *countingStore) BatchSet(ctx context.Context, items map[string][]byte) error {
	s.calls.Add(1)
	return s.KVStore.BatchSet(ctx, items)
}

func (s *countingStore) BatchDelete(ctx context.Context, keys []string) error {
	s.calls.Add(1)
	return s.KVStore.BatchDelete(ctx, keys)
}

func (s *countingStore) BatchSetWithTTL(ctx context.Context, items map[string][]byte, ttl time.Duration) error {
	s.calls.Add(1)
	return s.KVStore.BatchSetWithTTL(ctx, items, ttl)
}

func (s *countingStore) ScanPage(ctx context.Context, prefix, cursor string, count int) (storage.KeyPage, error) {
	s.calls.Add(1)
	return s.KVStore.ScanPage(ctx, prefix, cursor, count)
}

func (s *countingStore) Scan(ctx context.Context, pattern string, limit int) ([]string, error) {
	s.calls.Add(1)
	return s.KVStore.Scan(ctx, pattern, limit)
}

func (s *countingStore) ScanWithPrefix(ctx context.Context, prefix string, limit int) ([]string, error) {
	s.calls.Add(1)
	return s.KVStore.ScanWithPrefix(ctx, prefix, limit)
}

func (s *countingStore) Ping(ctx context.Context) error {
	s.calls.Add(1)
	return s.KVStore.Ping(ctx)
}

func (s *countingStore) ReadWithLifetime(ctx context.Context, key string, maxBytes int) ([]byte, time.Duration, error) {
	s.calls.Add(1)
	return s.bound.ReadWithLifetime(ctx, key, maxBytes)
}

func (s *countingStore) ReadBatchWithLifetime(ctx context.Context, keys []string, maxBytes int) ([]storage.LifetimeValue, error) {
	s.calls.Add(1)
	return s.bound.ReadBatchWithLifetime(ctx, keys, maxBytes)
}

func (s *countingStore) AuthorityTime(ctx context.Context) (time.Time, error) {
	s.calls.Add(1)
	return s.bound.AuthorityTime(ctx)
}

func (s *countingStore) CompareAndSwapInWindow(ctx context.Context, mutations []storage.CompareAndSwapMutation, window storage.TimeWindow) error {
	s.calls.Add(1)
	return s.bound.CompareAndSwapInWindow(ctx, mutations, window)
}
