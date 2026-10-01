package app

import (
	"context"
	legacyjson "encoding/json"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/agentstation/starmap/pkg/productfiles"
	"github.com/agentstation/starport/internal/account"
	"github.com/agentstation/starport/internal/apikey"
	"github.com/agentstation/starport/internal/authorization"
	"github.com/agentstation/starport/internal/catalog"
	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/limits"
	"github.com/agentstation/starport/internal/limits/admission"
	"github.com/agentstation/starport/internal/limits/reservation"
	"github.com/agentstation/starport/internal/recovery"
	"github.com/agentstation/starport/internal/server/requestctx"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

const freshGatewayChild = "STARPORT_RECOVERY_FRESH_GATEWAY"
const freshGatewayAccount = "fresh-recovered-account"

type freshGatewayFixture struct {
	Paths   config.Paths
	Config  jsontext.Value
	Catalog map[string]string
	KeyHash string
	Report  string
}

type freshGatewayReport struct {
	Approval   recovery.Record
	Ready      bool
	Generation string
	Reserved   int64
}

func captureFreshGatewayPolicy(t *testing.T, cfg *config.Config) apikey.APIKey {
	t.Helper()
	store, err := storage.Open(cfg.RuntimeStorage())
	require.NoError(t, err)
	defer func() { require.NoError(t, store.Close()) }()
	accounts, err := account.Open(store)
	require.NoError(t, err)
	created, err := accounts.Create(t.Context(), account.Account{ID: freshGatewayAccount, Name: "Fresh gateway", Active: true, CredentialStrategy: account.StrategyOperatorFirst, Limits: &limits.Limits{Tokens: &limits.Budget{Limit: 1000, Interval: limits.IntervalDay}}})
	require.NoError(t, err)
	keys, err := apikey.Open(store)
	require.NoError(t, err)
	issuer, err := apikey.NewIssuer(keys, apikey.WithAccountChecker(accounts))
	require.NoError(t, err)
	issued, err := issuer.IssueInitial(t.Context(), apikey.IssueRequest{Name: "recovered-client", AccountID: freshGatewayAccount, Scopes: []string{"inference:write"}})
	require.NoError(t, err)
	generations, err := catalog.NewGenerationStore(store)
	require.NoError(t, err)
	generation, err := generations.Current(t.Context())
	require.NoError(t, err)
	ledger, err := reservation.Open(store.(storage.TimeBoundStore))
	require.NoError(t, err)
	meter := reservation.Meter{Scope: limits.ScopeAccount, Holder: freshGatewayAccount, Dimension: limits.DimensionTokens, Interval: limits.IntervalDay}
	_, err = ledger.Reserve(t.Context(), reservation.Attempt{ID: "original-zero-token-window", RequestID: "original-token-window", AccountID: freshGatewayAccount, KeyID: issued.APIKey.ID, OfferingID: "acme/opaque/chat@001", CatalogGeneration: generation.Manifest.GenerationID, Operation: "chat-completions", Rules: []reservation.Rule{{Meter: meter, Limit: 1000, PolicyRevision: "source-account-1", HistoryID: created.Account.Limits.Tokens.HistoryID}}, TokenOnly: true, TokenBound: 1})
	require.NoError(t, err)
	require.NoError(t, ledger.CancelBeforeDispatch(t.Context(), "original-zero-token-window"))
	return issued.APIKey
}

func freshGatewayConfiguration(t *testing.T, fixture freshGatewayFixture) *config.Config {
	t.Helper()
	var saved config.Config
	require.NoError(t, json.Unmarshal(fixture.Config, &saved, legacyjson.FormatDurationAsNano(true)))
	values := map[string]string{"STARPORT_SECURITY_MASTER_KEY": saved.Security.MasterKey, "STARPORT_DEPLOYMENT_ID": fixture.Paths.DeploymentID, "STARPORT_INSTANCE_ID": fixture.Paths.InstanceID}
	for key, value := range fixture.Catalog {
		name := "STARPORT_" + strings.TrimPrefix(key, "STARMAP_")
		if key == "STARMAP_STATE_DIR" {
			name = "STARPORT_CATALOG_STATE_DIR"
		}
		values[name] = value
	}
	cfg, err := config.NewLoader().WithPaths(fixture.Paths).WithEnvFiles().WithEnvironment(values).Load(t.Context())
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(fixture.Config, cfg, legacyjson.FormatDurationAsNano(true)))
	return cfg
}

func runFreshGatewayChild(t *testing.T, path string) {
	t.Helper()
	body, err := os.ReadFile(path)
	require.NoError(t, err)
	var fixture freshGatewayFixture
	require.NoError(t, json.Unmarshal(body, &fixture))
	cfg := freshGatewayConfiguration(t, fixture)
	application, err := New(cfg)
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	defer func() { require.NoError(t, application.Close(ctx)) }()
	require.True(t, application.admissionReady())
	require.NotNil(t, application.budget.shared)
	require.NoError(t, application.budget.shared.Check(ctx))
	bundle, err := application.authorization.cache.Resolve(ctx, authorization.Identity{Subject: fixture.KeyHash})
	require.NoError(t, err)
	require.Equal(t, freshGatewayAccount, bundle.Account().Account.ID)
	require.Equal(t, 1, bundle.BudgetPolicy().RuleCount())
	permission := requestctx.WithAuthorization(ctx, bundle, application.authorization.clock, nil)
	snapshot := application.catalog.Current()
	require.NotNil(t, snapshot)
	route := requireSyntheticRoute(t, snapshot.Routes(), "opaque/chat@001", catalogs.ProviderOperationChatCompletions)
	quote := func(requirements admission.Requirements) (admission.Quote, error) {
		require.True(t, requirements.Tokens)
		require.False(t, requirements.Spend)
		return admission.Quote{Tokens: new(int64(1))}, nil
	}
	ticket, err := application.budget.admission.Start(permission, admission.Target{RequestID: "fresh-gateway-no-provider-call", AccountID: freshGatewayAccount, OfferingID: route.ID(), CatalogGeneration: snapshot.GenerationID(), Operation: "chat-completions"}, quote)
	require.NoError(t, err)
	require.NotEmpty(t, ticket.ID())
	record, err := application.budget.ledger.Inspect(ctx, ticket.ID())
	require.NoError(t, err)
	require.Equal(t, reservation.Dispatched, record.State)
	require.Nil(t, record.NanoUSD)
	meter := reservation.Meter{Scope: limits.ScopeAccount, Holder: freshGatewayAccount, Dimension: limits.DimensionTokens, Interval: limits.IntervalDay}
	window, err := application.budget.ledger.Window(ctx, meter, record.AdmittedAt)
	require.NoError(t, err)
	require.EqualValues(t, 1, window.Reserved)
	// This child makes no provider call, so its explicit no-charge evidence is complete.
	require.NoError(t, ticket.Finish(ctx, &reservation.Evidence{ID: "fixture-no-provider-call", NoCharge: true}))
	db, err := openBackupSQL(cfg)
	require.NoError(t, err)
	defer func() { require.NoError(t, db.Close()) }()
	witness, err := recovery.New(db)
	require.NoError(t, err)
	approval, err := witness.Approved(ctx, cfg.EffectivePaths().DeploymentID)
	require.NoError(t, err)
	body, err = json.Marshal(freshGatewayReport{Approval: approval, Ready: application.admissionReady(), Generation: snapshot.GenerationID(), Reserved: window.Reserved})
	require.NoError(t, err)
	directory, err := productfiles.ExistingDirectory(filepath.Dir(fixture.Report))
	require.NoError(t, err)
	require.NoError(t, directory.CompareAndPublish(ctx, filepath.Base(fixture.Report), nil, body))
}

func TestRecoveredSharedGatewayFreshStartupAndBudgetAdmission(t *testing.T) {
	if path := os.Getenv(freshGatewayChild); path != "" {
		runFreshGatewayChild(t, path)
		return
	}
	valkey, postgres, endpoint := os.Getenv("TEST_VALKEY_URL"), os.Getenv("TEST_POSTGRES_URL"), os.Getenv("TEST_BLOB_S3_ENDPOINT")
	if valkey == "" || postgres == "" || endpoint == "" {
		t.Skip("UNVERIFIED: complete fresh gateway needs native shared recovery owners")
	}
	var key apikey.APIKey
	cfg, prepare := boundedActivationSourceFixtureWithActivity(t, false, func(source *config.Config) { key = captureFreshGatewayPolicy(t, source) }, nil)
	configureSharedRestore(t, cfg, valkey, postgres, endpoint)
	cfg, request := activationPreparedFixture(t, cfg, prepare)
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	result, err := ActivateRecovery(ctx, cfg, request)
	require.NoError(t, err)
	require.True(t, result.CurrentAdmissionValid)
	require.True(t, result.HistoricallyComplete)
	request.ExpectedDecisionSHA256 = result.DecisionSHA256
	native, err := openRecoveryActivationNative(ctx, cfg, request)
	require.NoError(t, err)
	approved, err := native.witness.Approved(ctx, cfg.EffectivePaths().DeploymentID)
	require.NoError(t, err)
	require.Equal(t, request.History.ValkeyIncarnation, approved.BackendID)
	fleet, err := catalog.NewFleetStore(ctx, native.store.(storage.IncarnationProvider), native.witness, cfg.EffectivePaths().DeploymentID)
	require.NoError(t, err)
	accepted, err := fleet.AcceptedPublication(ctx)
	require.NoError(t, err)
	require.NoError(t, native.close())
	root := filepath.Join(t.TempDir(), "private")
	directory, err := productfiles.CreateDirectory(root)
	require.NoError(t, err)
	body, err := json.Marshal(cfg, legacyjson.FormatDurationAsNano(true), json.Deterministic(true))
	require.NoError(t, err)
	fixture, err := json.Marshal(freshGatewayFixture{Paths: cfg.EffectivePaths(), Config: body, Catalog: cfg.Catalog.CatalogValues(), KeyHash: key.Hash, Report: filepath.Join(root, "report.json")})
	require.NoError(t, err)
	require.NoError(t, directory.CompareAndPublish(ctx, "fixture.json", nil, fixture))
	command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestRecoveredSharedGatewayFreshStartupAndBudgetAdmission$", "-test.timeout=2m", "-test.v")
	command.Env = append(os.Environ(), freshGatewayChild+"="+filepath.Join(root, "fixture.json"))
	output, err := command.CombinedOutput()
	require.NoError(t, err, "%s", output)
	body, err = os.ReadFile(filepath.Join(root, "report.json"))
	require.NoError(t, err)
	var report freshGatewayReport
	require.NoError(t, json.Unmarshal(body, &report))
	require.Equal(t, approved, report.Approval)
	require.True(t, report.Ready)
	require.Equal(t, accepted.Publication.Generation.Manifest.GenerationID, report.Generation)
	require.EqualValues(t, 1, report.Reserved)
}
