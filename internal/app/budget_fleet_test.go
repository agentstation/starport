package app

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/v2"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/agentstation/starmap/pkg/catalogs"
	starmaperrors "github.com/agentstation/starmap/pkg/errors"
	starmapruntime "github.com/agentstation/starmap/runtime"
	"github.com/agentstation/starport/internal/account"
	"github.com/agentstation/starport/internal/apikey"
	runtimecatalog "github.com/agentstation/starport/internal/catalog"
	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/credentials"
	"github.com/agentstation/starport/internal/identity"
	"github.com/agentstation/starport/internal/limits"
	"github.com/agentstation/starport/internal/limits/reservation"
	"github.com/agentstation/starport/internal/providers"
	"github.com/agentstation/starport/internal/providers/keyring"
	"github.com/agentstation/starport/internal/recovery"
	"github.com/agentstation/starport/internal/server"
	"github.com/agentstation/starport/internal/sqlstore"
	"github.com/agentstation/starport/internal/storage"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"
)

const (
	budgetFleetChild = "STARPORT_BUDGET_FLEET_CHILD"
	// These limits bound race-instrumented gateway construction and fixture life.
	budgetFleetReadinessTimeout = 90 * time.Second
	budgetFleetChildTimeout     = 5 * time.Minute
)

type budgetFleetInput struct{ Valkey, Postgres, Deployment, Upstream, Ready string }

func budgetFleetConfig(t *testing.T, input budgetFleetInput) *config.Config {
	t.Helper()
	cfg := validProductionConfig(t)
	cfg.Storage.Mode, cfg.Storage.Valkey.URL = "valkey", input.Valkey
	cfg.Storage.Valkey.MaxConnections, cfg.Storage.Valkey.MinIdleConns = 10, 1
	cfg.Storage.Valkey.DialTimeout, cfg.Storage.Valkey.ReadTimeout, cfg.Storage.Valkey.WriteTimeout = time.Second, time.Second, time.Second
	cfg.Storage.SQL.Mode, cfg.Storage.SQL.Postgres.URL = sqlstore.TypePostgres, input.Postgres
	cfg.Catalog.StateDirectory = filepath.Join(t.TempDir(), "catalog")
	cfg.Files.Backend = config.BlobBackendObjectStore
	cfg.Files.ObjectStore.Bucket, cfg.Files.ObjectStore.Region, cfg.Files.ObjectStore.Endpoint = "unused-budget-fixture", "us-east-1", "http://127.0.0.1:1"
	cfg.Identity.OAuth.GitHub.ClientID, cfg.Identity.OAuth.GitHub.ClientSecret = "fixture", "fixture"
	provider := cfg.Providers[catalogs.ProviderIDOpenAI]
	provider.BaseURL, provider.CredentialReferences, provider.Timeout = input.Upstream, nil, 30*time.Second
	cfg.Providers[catalogs.ProviderIDOpenAI] = provider
	loaded, err := config.NewLoader().WithPaths(config.PathsForConfigDir(t.TempDir())).WithEnvFiles().WithEnvironment(map[string]string{"STARPORT_DEPLOYMENT_ID": input.Deployment, "OPENAI_API_KEY": "sk-test-key", "STARPORT_CATALOG_ACQUISITION_SOURCES": ""}).Load(t.Context(), func(target *config.Config) {
		catalogSettings := target.Catalog
		catalogSettings.Source = cfg.Catalog.Source
		catalogSettings.StateDirectory = cfg.Catalog.StateDirectory
		catalogSettings.AcquisitionEnabled = false
		*target = *cfg
		target.Catalog = catalogSettings
	})
	require.NoError(t, err)
	bundled, err := runtimecatalog.Bundled()
	require.NoError(t, err)
	catalogProvider, err := bundled.Provider(catalogs.ProviderIDOpenAI)
	require.NoError(t, err)
	policy, err := providers.CompileDestinationPolicy(catalogProvider, string(keyring.SourceEnvironment), catalogProvider.Credentials.Inference.Alternatives[0], input.Upstream, nil)
	require.NoError(t, err)
	loaded.InferenceDestinationApprovals, err = credentials.NewDestinationApprovals(nil, policy)
	require.NoError(t, err)
	return loaded
}

func runBudgetFleetChild(t *testing.T, encoded string) {
	t.Helper()
	var input budgetFleetInput
	require.NoError(t, json.Unmarshal([]byte(encoded), &input))
	application, err := New(budgetFleetConfig(t, input))
	require.NoError(t, err)
	defer func() { require.NoError(t, application.Close(context.Background())) }()
	runtime, ok := application.httpServer.(*server.Server)
	require.True(t, ok)
	gateway := httptest.NewServer(runtime.Router())
	defer gateway.Close()
	require.NoError(t, os.WriteFile(input.Ready, []byte(gateway.URL), 0600))
	// The parent terminates this process after inspecting dispatch and durable state.
	<-t.Context().Done()
}

type budgetFleetProcess struct {
	command *exec.Cmd
	output  bytes.Buffer
	base    string
	stopped bool
	done    chan struct{}
}

func startBudgetFleetProcess(t *testing.T, input budgetFleetInput) *budgetFleetProcess {
	t.Helper()
	input.Ready = filepath.Join(t.TempDir(), "ready")
	encoded, err := json.Marshal(input)
	require.NoError(t, err)
	childTimeout := budgetFleetChildTimeout
	if deadline, ok := t.Deadline(); ok {
		childTimeout = min(childTimeout, time.Until(deadline))
	}
	child := &budgetFleetProcess{command: exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestProductionBudgetAcrossProcesses$", "-test.timeout="+childTimeout.String()), done: make(chan struct{})}
	child.command.Env = append(os.Environ(), budgetFleetChild+"="+string(encoded))
	child.command.Stdout, child.command.Stderr = &child.output, &child.output
	// Catalog setup and adoption allocate complete generations in the parent.
	// Release their temporary memory before another instrumented gateway starts.
	debug.FreeOSMemory()
	require.NoError(t, child.command.Start())
	go func() {
		_ = child.command.Wait()
		close(child.done)
	}()
	t.Cleanup(func() { child.stop() })
	deadline := time.NewTimer(budgetFleetReadinessTimeout)
	defer deadline.Stop()
	poll := time.NewTicker(10 * time.Millisecond)
	defer poll.Stop()
	for {
		data, _ := os.ReadFile(input.Ready)
		if len(data) != 0 {
			child.base = string(data)
			return child
		}
		select {
		case <-child.done:
			t.Fatalf("gateway process exited before readiness: %s", child.output.String())
		case <-deadline.C:
			child.stop()
			t.Fatalf("gateway process did not become ready: %s", child.output.String())
		case <-t.Context().Done():
			child.stop()
			t.Fatalf("gateway readiness canceled: %v", t.Context().Err())
		case <-poll.C:
		}
	}
}

func (p *budgetFleetProcess) stop() {
	if !p.stopped {
		_ = p.command.Process.Kill()
		<-p.done
		p.stopped = true
	}
}

func TestProductionBudgetAcrossProcesses(t *testing.T) {
	if input := os.Getenv(budgetFleetChild); input != "" {
		runBudgetFleetChild(t, input)
		return
	}
	valkey, postgres := os.Getenv("TEST_VALKEY_URL"), os.Getenv("TEST_POSTGRES_URL")
	if valkey == "" || postgres == "" {
		t.Skip("UNVERIFIED: TEST_VALKEY_URL and TEST_POSTGRES_URL are required")
	}
	for _, mode := range []string{"concurrent-settlement", "dispatched-process-loss", "backend-replacement"} {
		t.Run(mode, func(t *testing.T) {
			if mode == "backend-replacement" && os.Getenv("TEST_VALKEY_REPLACEMENT_URL") == "" {
				t.Skip("UNVERIFIED: TEST_VALKEY_REPLACEMENT_URL is required")
			}
			entered, release := make(chan struct{}, 2), make(chan struct{})
			var calls atomic.Int64
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer sk-test-key" {
					t.Error("unexpected provider path or authentication")
					http.Error(w, "unexpected request", http.StatusBadRequest)
					return
				}
				calls.Add(1)
				entered <- struct{}{}
				select {
				case <-release:
					w.Header().Set("Content-Type", "application/json")
					_, _ = io.WriteString(w, `{"id":"fleet-usage","object":"chat.completion","model":"gpt-4o-mini","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":8,"completion_tokens":3,"total_tokens":11,"prompt_tokens_details":{"cached_tokens":0}}}`)
				case <-r.Context().Done():
				}
			}))
			t.Cleanup(upstream.Close)
			t.Cleanup(func() {
				select {
				case <-release:
				default:
					close(release)
				}
			})
			admin, err := sqlstore.Open(config.StorageConfig{SQL: config.SQLConfig{Mode: sqlstore.TypePostgres, Postgres: config.SQLPostgresConfig{URL: postgres}}}.RuntimeSQL())
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, admin.Close()) })
			schemaName := "budget_fleet_" + strings.ToLower(rand.Text())
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
			input := budgetFleetInput{Valkey: valkey, Postgres: parsed.String(), Deployment: "budget-fleet-" + rand.Text(), Upstream: upstream.URL}
			cfg := budgetFleetConfig(t, input)
			seed, err := openStorage(cfg.RuntimeStorage())
			require.NoError(t, err)
			approveTestFleet(t, cfg, seed)
			bootstrap, err := apikey.Open(seed)
			require.NoError(t, err)
			_, err = bootstrap.Create(t.Context(), testAPIKey())
			require.NoError(t, err)
			require.NoError(t, seed.Close())
			t.Cleanup(func() {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				cleanup, err := openStorage(cfg.RuntimeStorage())
				require.NoError(t, err)
				defer func() { require.NoError(t, cleanup.Close()) }()
				keys, err := cleanup.ScanWithPrefix(ctx, "", 0)
				require.NoError(t, err)
				require.NoError(t, cleanup.BatchDelete(ctx, keys))
			})
			fleetHead := seedBudgetFleet(t, cfg, mode == "backend-replacement")
			store, ledger := inspectBudgetFleet(t, cfg)
			first, second := startBudgetFleetProcess(t, input), startBudgetFleetProcess(t, input)
			client := &http.Client{Timeout: 20 * time.Second}
			request := func(base string) (int, string, error) {
				return budgetRequest(t.Context(), &performanceFixture{gateway: &httptest.Server{URL: base}, client: client})
			}
			type response struct {
				status int
				body   string
				err    error
			}
			pending := make(chan response, 1)
			go func() { status, body, err := request(first.base); pending <- response{status, body, err} }()
			select {
			case <-entered:
			case result := <-pending:
				t.Fatalf("request stopped before provider: %d %s %v", result.status, result.body, result.err)
			case <-time.After(15 * time.Second):
				t.Fatal("provider dispatch did not start")
			}
			status, body, err := request(second.base)
			require.NoError(t, err)
			require.Equal(t, http.StatusPaymentRequired, status, body)
			require.EqualValues(t, 1, calls.Load())
			keys, err := store.ScanWithPrefix(t.Context(), "budget:v1:attempt:", 100)
			require.NoError(t, err)
			require.Len(t, keys, 1)
			raw, err := store.Get(t.Context(), keys[0])
			require.NoError(t, err)
			var record reservation.Record
			require.NoError(t, json.Unmarshal(raw, &record))
			require.Equal(t, reservation.Dispatched, record.State)
			require.Len(t, record.Attempt.Rules, 5, "account/key token and spend meters plus team spend")
			for _, rule := range record.Attempt.Rules {
				state, err := ledger.Window(t.Context(), rule.Meter, record.AdmittedAt)
				require.NoError(t, err)
				require.Positive(t, state.Reserved)
				require.Zero(t, state.Consumed)
			}
			if mode == "backend-replacement" {
				first.stop()
				<-pending
				second.stop()
				restoredConfig := *cfg
				restoredConfig.Storage.Valkey.URL = os.Getenv("TEST_VALKEY_REPLACEMENT_URL")
				original, err := openStorage(cfg.RuntimeStorage())
				require.NoError(t, err)
				t.Cleanup(func() { require.NoError(t, original.Close()) })
				restored, err := openStorage(restoredConfig.RuntimeStorage())
				require.NoError(t, err)
				t.Cleanup(func() {
					keys, err := restored.ScanWithPrefix(context.Background(), "", 0)
					require.NoError(t, err)
					require.NoError(t, restored.BatchDelete(context.Background(), keys))
					require.NoError(t, restored.Close())
				})
				oldBackend := original.(storage.IncarnationProvider)
				newBackend := restored.(storage.IncarnationProvider)
				oldID, err := oldBackend.ObserveIncarnation(t.Context())
				require.NoError(t, err)
				newID, err := newBackend.ObserveIncarnation(t.Context())
				require.NoError(t, err)
				require.NotEqual(t, oldID, newID, "replacement must use an independent backend process")
				copyBudgetFleetSnapshot(t, original, restored)
				unapproved, err := New(&restoredConfig)
				require.Error(t, err, "copied authority records cannot approve a new backend")
				require.Nil(t, unapproved)
				require.EqualValues(t, 1, calls.Load())

				db, err := sqlstore.Open(cfg.Storage.RuntimeSQL())
				require.NoError(t, err)
				t.Cleanup(func() { require.NoError(t, db.Close()) })
				witness, err := recovery.New(db)
				require.NoError(t, err)
				_, err = witness.OpenAuthority(t.Context(), newBackend, input.Deployment)
				require.ErrorIs(t, err, storage.ErrIncarnationChanged)
				approved, err := witness.Current(t.Context(), input.Deployment)
				require.NoError(t, err)
				sourceFleet, err := runtimecatalog.NewFleetStore(t.Context(), oldBackend, witness, input.Deployment)
				require.NoError(t, err)
				stoppedHead, err := sourceFleet.CurrentHead(t.Context())
				require.NoError(t, err)
				// Closing the setup owner permits a successor publication. Recovery
				// must select the exact stopped head, including its current revision.
				require.Equal(t, fleetHead.GenerationID, stoppedHead.GenerationID)
				require.Equal(t, fleetHead.RecoveryChecksum, stoppedHead.RecoveryChecksum)
				fleetHead = stoppedHead
				closed, err := witness.Close(t.Context(), approved)
				require.NoError(t, err)
				_, err = witness.ApproveAuthority(t.Context(), newBackend, closed, newID, "fixture-complete-snapshot-with-retained-dispatch", "restore-fixture")
				require.NoError(t, err)
				copiedFleet, err := runtimecatalog.NewFleetStore(t.Context(), newBackend, witness, input.Deployment)
				require.NoError(t, err)
				_, err = copiedFleet.CurrentHead(t.Context())
				require.ErrorContains(t, err, "another recovery identity", "budget approval does not adopt catalog history")
				partialApproval, err := witness.Current(t.Context(), input.Deployment)
				require.NoError(t, err)
				closed, err = witness.Close(t.Context(), partialApproval)
				require.NoError(t, err)
				// The fixture stops both gateways before abandoning their readers.
				adoption := runtimecatalog.FleetAdoptionRequest{Closed: closed, SourceApproval: approved,
					Head: fleetHead, BackendID: newID, OperationID: "catalog-and-budget-restore",
					Evidence: "fixture-complete-snapshot-with-retained-dispatch", ResolveReaders: true}
				completed, err := runtimecatalog.AdoptFleet(t.Context(), newBackend, witness, adoption, catalogSettings(&restoredConfig))
				require.NoError(t, err)
				repeated, err := runtimecatalog.AdoptFleet(t.Context(), newBackend, witness, adoption, catalogSettings(&restoredConfig))
				require.NoError(t, err)
				require.Equal(t, completed, repeated)

				authority, err := witness.OpenAuthority(t.Context(), newBackend, input.Deployment)
				require.NoError(t, err)
				ledger, err := reservation.Open(authority)
				require.NoError(t, err)
				current, err := ledger.Inspect(t.Context(), record.Attempt.ID)
				require.NoError(t, err)
				require.Equal(t, &record, current, "restoration cannot change the dispatched reservation")
				input.Valkey = restoredConfig.Storage.Valkey.URL
				replacement := startBudgetFleetProcess(t, input)
				status, body, err = request(replacement.base)
				require.NoError(t, err)
				require.Equal(t, http.StatusPaymentRequired, status, body)
				require.EqualValues(t, 1, calls.Load(), "approval cannot refund the retained dispatch")
				for _, rule := range record.Attempt.Rules {
					state, err := ledger.Window(t.Context(), rule.Meter, record.AdmittedAt)
					require.NoError(t, err)
					require.Positive(t, state.Reserved)
					require.Zero(t, state.Consumed)
				}
				// The fixture supplies external no-charge evidence only after recovery.
				evidence := reservation.Evidence{ID: "fixture-provider-no-charge", NoCharge: true}
				require.NoError(t, ledger.Reconcile(t.Context(), record.Attempt.ID, evidence))
				require.NoError(t, ledger.Reconcile(t.Context(), record.Attempt.ID, evidence))
				close(release)
				status, body, err = request(replacement.base)
				require.NoError(t, err)
				require.Equal(t, http.StatusOK, status, body)
				require.EqualValues(t, 2, calls.Load())
			} else if mode == "dispatched-process-loss" {
				first.stop()
				<-pending
				replacement := startBudgetFleetProcess(t, input)
				status, body, err = request(replacement.base)
				require.NoError(t, err)
				require.Equal(t, http.StatusPaymentRequired, status, body)
				require.EqualValues(t, 1, calls.Load(), "restart cannot refund uncertain dispatch")
				current, err := ledger.Inspect(t.Context(), record.Attempt.ID)
				require.NoError(t, err)
				require.Equal(t, reservation.Dispatched, current.State)
				require.Nil(t, current.Evidence)
				for _, rule := range record.Attempt.Rules {
					state, err := ledger.Window(t.Context(), rule.Meter, record.AdmittedAt)
					require.NoError(t, err)
					require.Positive(t, state.Reserved)
					require.Zero(t, state.Consumed)
				}
			} else {
				close(release)
				result := <-pending
				require.NoError(t, result.err)
				require.Equal(t, http.StatusOK, result.status, result.body)
				current, err := ledger.Inspect(t.Context(), record.Attempt.ID)
				require.NoError(t, err)
				require.Equal(t, reservation.Settled, current.State)
				require.EqualValues(t, 11, current.Evidence.Tokens)
				for _, rule := range record.Attempt.Rules {
					state, err := ledger.Window(t.Context(), rule.Meter, record.AdmittedAt)
					require.NoError(t, err)
					require.Zero(t, state.Reserved)
					if rule.Meter.Dimension == limits.DimensionTokens {
						require.EqualValues(t, 11, state.Consumed)
					} else {
						require.Equal(t, *current.NanoUSD, state.Consumed)
					}
				}
				status, body, err = request(second.base)
				require.NoError(t, err)
				require.Equal(t, http.StatusOK, status, body)
				require.EqualValues(t, 2, calls.Load())
			}
		})
	}
}

// seedBudgetFleet closes the setup gateway before the independent processes start.
// Durable repositories inspect the result without retaining another full runtime.
func seedBudgetFleet(t *testing.T, cfg *config.Config, publish bool) starmapruntime.FleetHead {
	t.Helper()
	var deps server.Dependencies
	application, err := New(cfg, func(options *buildOptions) {
		original := options.factories.newServer
		options.factories.newServer = func(settings *server.Config, dependencies server.Dependencies) (httpRuntime, error) {
			deps = dependencies
			return original(settings, dependencies)
		}
	})
	require.NoError(t, err)
	defer func() { require.NoError(t, application.Close(context.Background())) }()
	var head starmapruntime.FleetHead
	if publish {
		// New opens the embedded baseline. Explicit refresh publishes the
		// fleet snapshot before the fixture takes a recovery copy.
		catalogRuntime, ok := application.catalogRuntime.(*runtimecatalog.Runtime)
		require.True(t, ok)
		_, err := catalogRuntime.AcceptedGeneration(t.Context())
		require.ErrorIs(t, err, starmaperrors.ErrNotFound, "construction alone has not accepted a fleet publication")
		candidate, err := application.syncCatalog(t.Context())
		require.NoError(t, err)
		require.NoError(t, application.activateRuntimeState(t.Context(), candidate))
		accepted, err := catalogRuntime.AcceptedGeneration(t.Context())
		require.NoError(t, err)
		require.Equal(t, candidate.State.GenerationID, accepted.Manifest.GenerationID)
		head = candidate.FleetHead
	}
	now := time.Now().UTC()
	budgets := func() *limits.Limits {
		return &limits.Limits{Tokens: &limits.Budget{Limit: 200_000, Interval: limits.IntervalDay}, Spend: &limits.Budget{Limit: 1_000_000_000, Interval: limits.IntervalDay}}
	}
	_, err = deps.Accounts.Create(t.Context(), account.Account{ID: "fleet-account", Name: "Fleet account", Limits: budgets(), Active: true, CreatedAt: now})
	require.NoError(t, err)
	team, err := deps.Identity.Teams.Create(t.Context(), identity.Team{ID: "fleet-team", Name: "Fleet team", Budget: &limits.TeamBudget{Limit: 1_000_000_000, Interval: limits.IntervalDay}})
	require.NoError(t, err)
	digest := sha256.Sum256([]byte(performanceGatewayKey))
	key := testAPIKey()
	key.ID = "fleet-key"
	key.Hash = hex.EncodeToString(digest[:])
	key.AccountID = "fleet-account"
	key.TeamID = team.Team.ID
	key.Limits = budgets()
	_, err = deps.APIKeys.Create(t.Context(), key)
	require.NoError(t, err)
	return head
}

func inspectBudgetFleet(t *testing.T, cfg *config.Config) (storage.KVStore, *reservation.Repository) {
	t.Helper()
	store, err := openStorage(cfg.RuntimeStorage())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	db, err := sqlstore.Open(cfg.Storage.RuntimeSQL())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	witness, err := recovery.New(db)
	require.NoError(t, err)
	backend, ok := store.(storage.IncarnationProvider)
	require.True(t, ok)
	authority, err := witness.OpenAuthority(t.Context(), backend, cfg.EffectivePaths().DeploymentID)
	require.NoError(t, err)
	ledger, err := reservation.Open(authority)
	require.NoError(t, err)
	return store, ledger
}

// copyBudgetFleetSnapshot copies only this test's namespace after its writers stop.
func copyBudgetFleetSnapshot(t *testing.T, source, target storage.KVStore) {
	t.Helper()
	reader, ok := source.(storage.LifetimeReader)
	require.True(t, ok)
	keys, err := source.ScanWithPrefix(t.Context(), "", 0)
	require.NoError(t, err)
	require.NotEmpty(t, keys)
	for _, key := range keys {
		started := time.Now()
		data, ttl, err := reader.ReadWithLifetime(t.Context(), key, 32<<20)
		if errors.Is(err, storage.ErrNotFound) {
			continue
		}
		require.NoError(t, err)
		if ttl == 0 {
			require.NoError(t, target.Set(t.Context(), key, data))
		} else if remaining := ttl - time.Since(started); remaining > 0 {
			require.NoError(t, target.SetWithTTL(t.Context(), key, data, remaining))
		}
	}
}
