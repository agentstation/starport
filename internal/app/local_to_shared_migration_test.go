package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	legacyjson "encoding/json"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/agentstation/starmap/pkg/productfiles"
	"github.com/agentstation/starport/internal/account"
	"github.com/agentstation/starport/internal/apikey"
	"github.com/agentstation/starport/internal/audit"
	"github.com/agentstation/starport/internal/authorization"
	"github.com/agentstation/starport/internal/blob"
	"github.com/agentstation/starport/internal/catalog"
	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/credentials"
	"github.com/agentstation/starport/internal/files"
	"github.com/agentstation/starport/internal/identity"
	"github.com/agentstation/starport/internal/limits"
	"github.com/agentstation/starport/internal/limits/reservation"
	"github.com/agentstation/starport/internal/limits/storedbytes"
	"github.com/agentstation/starport/internal/localauth"
	"github.com/agentstation/starport/internal/providers/keyring"
	"github.com/agentstation/starport/internal/recovery"
	"github.com/agentstation/starport/internal/sqlstore"
	"github.com/agentstation/starport/internal/storage"
	"github.com/agentstation/starport/internal/usage"
	"github.com/stretchr/testify/require"
)

const localMigrationReplicaChild = "STARPORT_LOCAL_TO_SHARED_REPLICA"

// localMigrationAccounts own budgets, keys, credentials, files, and usage in the populated source.
var localMigrationAccounts = []string{"migration-primary", "migration-secondary"}

// localMigrationSource is the populated local deployment and its closed capture.
type localMigrationSource struct {
	config     *config.Config
	capture    recovery.CaptureResult
	census     localMigrationCensus
	keyHash    string
	generation string
}

// localMigrationCensus holds values that each domain owner returns. It reads no raw storage key.
type localMigrationCensus struct {
	Usage       []string
	Audit       []string
	Credentials map[string]string
	Files       map[string]string
}

type localMigrationReplicaReport struct {
	InstanceID string
	StateDir   string
	Ready      bool
	Account    string
	Generation string
}

// seedLocalMigrationWorkload populates Badger, SQLite, and filesystem blobs through the domain owners.
func seedLocalMigrationWorkload(t *testing.T, source *config.Config) (keyHash, generationID string) {
	t.Helper()
	ctx := t.Context()
	// Replace the backup fixture's placeholder with the record a running local deployment holds.
	token, err := localauth.NewStore(source.EffectivePaths().LocalTokenFile)
	require.NoError(t, err)
	_, err = token.Rotate(ctx, time.Now())
	require.NoError(t, err)
	store, err := storage.Open(source.RuntimeStorage())
	require.NoError(t, err)
	defer func() { require.NoError(t, store.Close()) }()
	accounts, err := account.Open(store)
	require.NoError(t, err)
	keys, err := apikey.Open(store)
	require.NoError(t, err)
	issuer, err := apikey.NewIssuer(keys, apikey.WithAccountChecker(accounts))
	require.NoError(t, err)
	generations, err := catalog.NewGenerationStore(store)
	require.NoError(t, err)
	generation, err := generations.Current(ctx)
	require.NoError(t, err)
	ledger, err := reservation.Open(store.(storage.TimeBoundStore))
	require.NoError(t, err)
	records, err := usage.Open(store, usage.Options{})
	require.NoError(t, err)
	repository, err := credentials.Open(store)
	require.NoError(t, err)
	inference := syntheticInferenceCatalog(t, "https://provider.invalid")
	validator, err := keyring.NewCatalogCredentialValidator(func(id catalogs.ProviderID) (catalogs.Provider, bool) {
		provider, err := inference.Provider(id)
		return provider, err == nil
	})
	require.NoError(t, err)
	provider, err := inference.Provider("acme")
	require.NoError(t, err)
	field := string(provider.Credentials.Fields[0].ID)
	masterKey := []byte(source.Security.MasterKey)
	if len(masterKey) < 32 {
		masterKey = credentials.DeriveKeyFromPassword(source.Security.MasterKey)
	}
	providerKeys, err := keyring.NewProviderKeys(repository, masterKey, validator)
	require.NoError(t, err)
	meter, err := storedbytes.NewStorageMeter(store)
	require.NoError(t, err)
	fileRecords, err := files.OpenRepository(store)
	require.NoError(t, err)
	blobs, err := openBlob(ctx, source.Files)
	require.NoError(t, err)
	service, err := files.NewService(fileRecords, blobs, files.WithRetention(source.Files.RetentionWindow()), files.WithMeter(meter))
	require.NoError(t, err)
	now := time.Now().UTC()
	for index, id := range localMigrationAccounts {
		created, err := accounts.Create(ctx, account.Account{ID: id, Name: id, Active: true, CredentialStrategy: account.StrategyOperatorFirst, Limits: &limits.Limits{Tokens: &limits.Budget{Limit: 1000, Interval: limits.IntervalDay}}})
		require.NoError(t, err)
		request := apikey.IssueRequest{Name: id + "-client", AccountID: id, Scopes: []string{"inference:write"}}
		var issued apikey.IssueResult
		if index == 0 {
			issued, err = issuer.IssueInitial(ctx, request)
			require.NoError(t, err)
			subject := sha256.Sum256([]byte(issued.Secret))
			require.Equal(t, hex.EncodeToString(subject[:]), issued.APIKey.Hash, "the client secret selects the stored key")
			keyHash = issued.APIKey.Hash
		} else {
			issued, err = issuer.Issue(ctx, request)
			require.NoError(t, err)
		}
		// Import inspection refuses a budget without a captured accounting history.
		attempt := id + "-history"
		meter := reservation.Meter{Scope: limits.ScopeAccount, Holder: id, Dimension: limits.DimensionTokens, Interval: limits.IntervalDay}
		_, err = ledger.Reserve(ctx, reservation.Attempt{ID: attempt, RequestID: attempt, AccountID: id, KeyID: issued.APIKey.ID, OfferingID: "acme/opaque/chat@001", CatalogGeneration: generation.Manifest.GenerationID, Operation: "chat-completions", Rules: []reservation.Rule{{Meter: meter, Limit: 1000, PolicyRevision: id + "-policy", HistoryID: created.Account.Limits.Tokens.HistoryID}}, TokenOnly: true, TokenBound: 1})
		require.NoError(t, err)
		require.NoError(t, ledger.CancelBeforeDispatch(ctx, attempt))
		_, err = providerKeys.AddKey(ctx, keyring.AccountScope(id), "acme", map[string]string{field: "sk-migration-fixture-" + id}, nil, false, 0)
		require.NoError(t, err)
		for request := range 2 {
			require.NoError(t, records.Put(ctx, usage.Record{RequestID: fmt.Sprintf("%s-request-%d", id, request), KeyID: issued.APIKey.ID, AccountID: id, Timestamp: now, Operation: usage.OperationChat, Status: usage.StatusOK, Tokens: usage.Tokens{Input: 2, Output: 3, Total: 5}, Cost: &usage.Cost{NanoUSD: 7, Currency: "USD"}}))
		}
	}
	_, err = issuer.Issue(ctx, apikey.IssueRequest{Name: "migration-primary-second", AccountID: localMigrationAccounts[0], Scopes: []string{"inference:write"}})
	require.NoError(t, err)
	require.NoError(t, meter.InitializeEmpty(ctx, localMigrationAccounts[0]))
	for _, name := range []string{"first", "second"} {
		payload := "local migration bytes for the " + name + " file\n"
		_, err = service.Upload(ctx, files.UploadRequest{Account: localMigrationAccounts[0], Filename: name + ".txt", Purpose: files.PurposeUserData, Size: int64(len(payload))}, strings.NewReader(payload))
		require.NoError(t, err)
	}

	db, err := sqlstore.Open(source.Storage.RuntimeSQL())
	require.NoError(t, err)
	defer func() { require.NoError(t, db.Close()) }()
	people, err := identity.Open(db)
	require.NoError(t, err)
	// A team budget needs separate origin history, so this fixture leaves the team unmetered.
	_, err = people.Teams.Create(ctx, identity.Team{ID: "migration-team", Name: "Migration team"})
	require.NoError(t, err)
	for _, user := range []string{"migration-alice", "migration-bob"} {
		_, err = people.Users.Create(ctx, identity.User{ID: user, Subject: user + "-subject"})
		require.NoError(t, err)
		_, err = people.Memberships.Add(ctx, identity.Membership{UserID: user, TeamID: "migration-team"})
		require.NoError(t, err)
	}
	_, err = people.AccountGrants.Add(ctx, identity.AccountGrant{AccountID: localMigrationAccounts[0], UserID: "migration-alice"})
	require.NoError(t, err)
	_, err = people.AccountGrants.Add(ctx, identity.AccountGrant{AccountID: localMigrationAccounts[1], TeamID: "migration-team"})
	require.NoError(t, err)
	trail, err := audit.Open(db, source.Audit.RetentionWindow())
	require.NoError(t, err)
	for index, subject := range append(slices.Clone(localMigrationAccounts), "migration-team") {
		require.NoError(t, trail.Record(ctx, audit.Record{Time: now, Actor: "operator:migration", Action: "fixture.create", Subject: subject, Outcome: audit.OutcomeOK, RequestID: fmt.Sprintf("audit-%d", index)}))
	}
	return keyHash, generation.Manifest.GenerationID
}

// readLocalMigrationCensus reads usage, audit, credentials, and file bytes through their owners.
func readLocalMigrationCensus(t *testing.T, cfg *config.Config) localMigrationCensus {
	t.Helper()
	ctx := t.Context()
	census := localMigrationCensus{Credentials: map[string]string{}, Files: map[string]string{}}
	store, err := storage.Open(cfg.RuntimeStorage())
	require.NoError(t, err)
	defer func() { require.NoError(t, store.Close()) }()
	records, err := usage.Open(store, usage.Options{})
	require.NoError(t, err)
	page, err := records.List(ctx, usage.Query{Limit: usage.MaxListLimit})
	require.NoError(t, err)
	require.Empty(t, page.NextCursor)
	for _, record := range page.Records {
		census.Usage = append(census.Usage, record.RequestID)
	}
	slices.Sort(census.Usage)
	encryption, err := backupEncryption(cfg)
	require.NoError(t, err)
	repository, err := credentials.Open(store)
	require.NoError(t, err)
	stored, err := repository.ListAll(ctx, 1000)
	require.NoError(t, err)
	for _, record := range stored {
		plaintext, err := encryption.DecryptCredential(record.Key.EncryptedCredential)
		require.NoError(t, err)
		census.Credentials[record.Key.Scope+"/"+record.Key.Provider] = plaintext
	}
	fileRecords, err := files.OpenRepository(store)
	require.NoError(t, err)
	blobs, err := openBlob(ctx, cfg.Files)
	require.NoError(t, err)
	service, err := files.NewService(fileRecords, blobs, files.WithRetention(cfg.Files.RetentionWindow()))
	require.NoError(t, err)
	for _, owner := range localMigrationAccounts {
		listed, err := service.List(ctx, owner, 100)
		require.NoError(t, err)
		for _, file := range listed {
			_, reader, err := service.Open(ctx, owner, file.ID)
			require.NoError(t, err)
			body, err := io.ReadAll(reader)
			require.NoError(t, err)
			require.NoError(t, reader.Close())
			census.Files[owner+"/"+file.ID] = string(body)
		}
	}
	db, err := openBackupSQL(cfg)
	require.NoError(t, err)
	defer func() { require.NoError(t, db.Close()) }()
	trail, err := audit.Open(db, cfg.Audit.RetentionWindow())
	require.NoError(t, err)
	trailPage, err := trail.List(ctx, audit.Query{Limit: audit.MaxListLimit})
	require.NoError(t, err)
	require.Empty(t, trailPage.NextCursor)
	for _, record := range trailPage.Records {
		census.Audit = append(census.Audit, record.Action+" "+record.Subject)
	}
	slices.Sort(census.Audit)
	return census
}

func localMigrationFixtures(t *testing.T) (valkey, postgres, endpoint string) {
	t.Helper()
	valkey, postgres, endpoint = os.Getenv("TEST_VALKEY_URL"), os.Getenv("TEST_POSTGRES_URL"), os.Getenv("TEST_BLOB_S3_ENDPOINT")
	if valkey == "" || postgres == "" || endpoint == "" {
		t.Skip("UNVERIFIED: local-to-shared migration needs native Valkey, PostgreSQL, and object storage")
	}
	return valkey, postgres, endpoint
}

// populatedLocalMigrationSource seeds, closes, and captures one local deployment.
// The returned target uses a new configuration directory with the same deployment identity.
func populatedLocalMigrationSource(t *testing.T) (localMigrationSource, *config.Config, recovery.PrepareRequest) {
	t.Helper()
	var source localMigrationSource
	target, prepare := boundedActivationSourceFixtureWithActivity(t, true, func(cfg *config.Config) {
		source.keyHash, source.generation = seedLocalMigrationWorkload(t, cfg)
	}, func(cfg *config.Config, capture recovery.CaptureResult) {
		source.config, source.capture = cfg, capture
		source.census = readLocalMigrationCensus(t, cfg)
	})
	require.Equal(t, []string{"migration-primary-request-0", "migration-primary-request-1", "migration-secondary-request-0", "migration-secondary-request-1"}, source.census.Usage)
	require.Len(t, source.census.Audit, 3)
	require.Len(t, source.census.Credentials, 2)
	require.Len(t, source.census.Files, 2)
	references := source.capture.References
	require.EqualValues(t, 3, references.GatewayKeys.Keys)
	require.EqualValues(t, 2, references.AccountRecords)
	require.EqualValues(t, 2, references.CredentialRecords)
	require.EqualValues(t, 2, references.FileRecords)
	require.EqualValues(t, 2, references.Identity.Users)
	require.EqualValues(t, 1, references.Identity.Teams)
	require.EqualValues(t, 2, references.Identity.Memberships)
	require.EqualValues(t, 2, references.Identity.Grants)
	require.NotZero(t, references.BudgetWindows)
	require.NotZero(t, references.BudgetRecords)
	return source, target, prepare
}

// localMigrationTarget derives another empty target for the same captured source.
func localMigrationTarget(t *testing.T, source *config.Config, prepare recovery.PrepareRequest, deploymentID string) (*config.Config, recovery.PrepareRequest) {
	t.Helper()
	parent := filepath.Join(t.TempDir(), "target")
	_, err := productfiles.CreateDirectory(parent)
	require.NoError(t, err)
	values := map[string]string{"STARPORT_SECURITY_MASTER_KEY": source.Security.MasterKey, "STARPORT_DEPLOYMENT_ID": deploymentID, "STARPORT_INSTANCE_ID": source.EffectivePaths().InstanceID}
	for key, value := range source.Catalog.CatalogValues() {
		if key != "STARMAP_STATE_DIR" {
			values["STARPORT_"+strings.TrimPrefix(key, "STARMAP_")] = value
		}
	}
	target, err := config.NewLoader().WithPaths(config.PathsForConfigDir(parent)).WithEnvFiles().WithEnvironment(values).Load(t.Context())
	require.NoError(t, err)
	prepare.FilesDirectory = filepath.Join(parent, "prepared")
	return target, prepare
}

// clearLocalMigrationValkey removes this deployment's keys so that the next empty-target import can start.
func clearLocalMigrationValkey(t *testing.T, cfg *config.Config) {
	t.Helper()
	t.Cleanup(func() {
		store, err := storage.OpenValkey(cfg.RuntimeStorage().Valkey)
		require.NoError(t, err)
		keys, err := store.ScanWithPrefix(context.Background(), "", 10000)
		require.NoError(t, err)
		if len(keys) > 0 {
			require.NoError(t, store.BatchDelete(context.Background(), keys))
		}
		require.NoError(t, store.Close())
	})
}

// activateLocalMigration runs prepare, import inspection, and activation on a shared target.
func activateLocalMigration(t *testing.T, source localMigrationSource, cfg *config.Config, prepare recovery.PrepareRequest) (RecoveryActivationRequest, recovery.ImportInspectionResult) {
	t.Helper()
	cfg, request := activationPreparedFixture(t, cfg, prepare)
	before := populatedWitness(t, cfg).current
	require.False(t, before.Open)
	inspected, err := InspectImportedBackup(t.Context(), cfg, recovery.InspectImportRequest{
		VerifyRequest: request.Prepare.VerifyRequest, Operation: request.Prepare.Operation,
		ExpectedBoundary: before, ValkeyIncarnation: request.History.ValkeyIncarnation,
		Destination: filepath.Join(filepath.Dir(request.Prepare.FilesDirectory), "migration-inspection"),
	})
	require.NoError(t, err)
	require.Equal(t, source.capture.References, inspected.Inspection.References, "the imported graph must equal the captured graph")
	require.Equal(t, request.History.ExpectedTargetSHA256, inspected.TargetSHA256)
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	result, err := ActivateRecovery(ctx, cfg, request)
	require.NoError(t, err)
	require.Equal(t, 3, result.CompletedPhases)
	require.True(t, result.HistoricallyComplete)
	require.True(t, result.CurrentAdmissionValid)
	require.False(t, result.Restricted)
	request.ExpectedDecisionSHA256 = result.DecisionSHA256
	return request, inspected
}

// requireLocalMigrationFleetHead checks the compiled fleet head before any gateway refreshes it.
func requireLocalMigrationFleetHead(t *testing.T, cfg *config.Config, request RecoveryActivationRequest, generation string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	native, err := openRecoveryActivationNative(ctx, cfg, request)
	require.NoError(t, err)
	defer func() { require.NoError(t, native.close()) }()
	approved, err := native.witness.Approved(ctx, cfg.EffectivePaths().DeploymentID)
	require.NoError(t, err)
	require.True(t, approved.Open)
	require.Equal(t, request.History.ValkeyIncarnation, approved.BackendID)
	fleet, err := catalog.NewFleetStore(ctx, native.store.(storage.IncarnationProvider), native.witness, cfg.EffectivePaths().DeploymentID)
	require.NoError(t, err)
	accepted, err := fleet.AcceptedPublication(ctx)
	require.NoError(t, err)
	require.Equal(t, generation, accepted.Head.GenerationID)
	require.Equal(t, generation, accepted.Publication.Generation.Manifest.GenerationID)
	require.NotNil(t, accepted.Publication.RecoveryOrigin)
	require.Equal(t, request.Prepare.Operation.ID, accepted.Publication.RecoveryOrigin.OperationID)
	prefix := fmt.Sprintf("catalog:fleet:{%x}:v1:", sha256.Sum256([]byte(cfg.EffectivePaths().DeploymentID)))
	_, err = native.store.Get(ctx, prefix+"lease")
	require.ErrorIs(t, err, storage.ErrNotFound, "recovery must not invent a live refresh lease")
}

// TestLocalToSharedMigrationPopulatedWorkload moves a populated local recipe into Valkey, PostgreSQL, and object storage.
func TestLocalToSharedMigrationPopulatedWorkload(t *testing.T) {
	valkey, postgres, endpoint := localMigrationFixtures(t)
	source, cfg, prepare := populatedLocalMigrationSource(t)
	configureSharedRestore(t, cfg, valkey, postgres, endpoint)
	clearLocalMigrationValkey(t, cfg)
	request, inspected := activateLocalMigration(t, source, cfg, prepare)
	t.Logf("parity: keys=%d accounts=%d credentials=%d/%d files=%d users=%d teams=%d memberships=%d grants=%d budget_windows=%d budget_records=%d usage=%d audit=%d",
		inspected.Inspection.References.GatewayKeys.Keys, inspected.Inspection.References.AccountRecords, inspected.Inspection.References.CredentialRecords, inspected.Inspection.References.CredentialValues,
		inspected.Inspection.References.FileRecords, inspected.Inspection.References.Identity.Users, inspected.Inspection.References.Identity.Teams, inspected.Inspection.References.Identity.Memberships,
		inspected.Inspection.References.Identity.Grants, inspected.Inspection.References.BudgetWindows, inspected.Inspection.References.BudgetRecords, len(source.census.Usage), len(source.census.Audit))
	requireLocalMigrationFleetHead(t, cfg, request, source.generation)
	// Credentials decrypt with the same master key to the same values, and file bytes are unchanged.
	require.Equal(t, source.census, readLocalMigrationCensus(t, cfg))

	application, err := New(cfg)
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	defer func() { require.NoError(t, application.Close(ctx)) }()
	require.True(t, application.admissionReady())
	bundle, err := application.authorization.cache.Resolve(ctx, authorization.Identity{Subject: source.keyHash})
	require.NoError(t, err)
	require.Equal(t, localMigrationAccounts[0], bundle.Account().Account.ID)
	require.Equal(t, 1, bundle.BudgetPolicy().RuleCount())
	require.Equal(t, source.generation, application.catalog.Current().GenerationID())
}

// TestLocalToSharedMigrationRefusals keeps each refused target closed and leaves existing data in place.
func TestLocalToSharedMigrationRefusals(t *testing.T) {
	valkey, postgres, endpoint := localMigrationFixtures(t)
	source, _, prepare := populatedLocalMigrationSource(t)
	deployment := source.config.EffectivePaths().DeploymentID
	target := func(t *testing.T, deploymentID string) (*config.Config, recovery.PrepareRequest) {
		cfg, request := localMigrationTarget(t, source.config, prepare, deploymentID)
		configureSharedRestore(t, cfg, valkey, postgres, endpoint)
		clearLocalMigrationValkey(t, cfg)
		return cfg, request
	}

	t.Run("populated-kv", func(t *testing.T) {
		cfg, request := target(t, deployment)
		store, err := storage.OpenValkey(cfg.RuntimeStorage().Valkey)
		require.NoError(t, err)
		require.NoError(t, store.Set(t.Context(), "operator-record", []byte("preserve")))
		require.NoError(t, store.Close())
		_, err = PrepareBackup(t.Context(), cfg, request)
		require.ErrorIs(t, err, storage.ErrDatabaseNotEmpty)
		store, err = storage.OpenValkey(cfg.RuntimeStorage().Valkey)
		require.NoError(t, err)
		retained, err := store.Get(t.Context(), "operator-record")
		require.NoError(t, err)
		require.Equal(t, "preserve", string(retained))
		require.NoError(t, store.Close())
	})
	t.Run("populated-sql", func(t *testing.T) {
		cfg, request := target(t, deployment)
		db, err := sqlstore.Open(cfg.Storage.RuntimeSQL())
		require.NoError(t, err)
		require.NoError(t, db.Migrate(t.Context()))
		trail, err := audit.Open(db, 0)
		require.NoError(t, err)
		require.NoError(t, trail.Record(t.Context(), audit.Record{Time: time.Now().UTC(), Actor: "operator:target", Action: "fixture.create", Subject: "preserve", Outcome: audit.OutcomeOK}))
		require.NoError(t, db.Close())
		_, err = PrepareBackup(t.Context(), cfg, request)
		require.ErrorIs(t, err, sqlstore.ErrNotFresh)
		db, err = sqlstore.Open(cfg.Storage.RuntimeSQL())
		require.NoError(t, err)
		trail, err = audit.Open(db, 0)
		require.NoError(t, err)
		retained, err := trail.List(t.Context(), audit.Query{})
		require.NoError(t, err)
		require.Len(t, retained.Records, 1)
		require.Equal(t, "preserve", retained.Records[0].Subject)
		require.NoError(t, db.Close())
	})
	t.Run("populated-blob", func(t *testing.T) {
		cfg, request := target(t, deployment)
		blobs, err := openBlob(t.Context(), cfg.Files)
		require.NoError(t, err)
		_, err = blobs.Put(t.Context(), "operator-object", strings.NewReader("preserve"))
		require.NoError(t, err)
		_, err = PrepareBackup(t.Context(), cfg, request)
		require.ErrorIs(t, err, blob.ErrImportTargetPopulated)
		retained, err := blobs.Get(t.Context(), "operator-object")
		require.NoError(t, err)
		body, err := io.ReadAll(retained)
		require.NoError(t, errors.Join(err, retained.Close()))
		require.Equal(t, "preserve", string(body))
	})
	t.Run("different-deployment", func(t *testing.T) {
		cfg, request := target(t, "migration-other-"+strings.ToLower(deployment[len(deployment)-8:]))
		_, err := PrepareBackup(t.Context(), cfg, request)
		require.ErrorIs(t, err, recovery.ErrConflict)
	})
	t.Run("wrong-master-key", func(t *testing.T) {
		cfg, request := target(t, deployment)
		cfg.Security.MasterKey = strings.Repeat("x", 32)
		_, err := PrepareBackup(t.Context(), cfg, request)
		require.ErrorIs(t, err, recovery.ErrBackupKey)
	})
	t.Run("restored-sql-witness", func(t *testing.T) {
		cfg, request := target(t, deployment)
		cfg, activation := activationPreparedFixture(t, cfg, request)
		closed := populatedWitness(t, cfg).current
		db, err := openBackupSQL(cfg)
		require.NoError(t, err)
		_, err = db.ExecContext(t.Context(), db.Bind("UPDATE catalog_recovery SET epoch=?, gate_open=0, evidence=? WHERE deployment_id=?"), closed.Epoch-1, "restored-sql-database", closed.DeploymentID)
		require.NoError(t, err)
		require.NoError(t, db.Close())
		_, err = InspectImportedBackup(t.Context(), cfg, recovery.InspectImportRequest{
			VerifyRequest: activation.Prepare.VerifyRequest, Operation: activation.Prepare.Operation,
			ExpectedBoundary: closed, ValkeyIncarnation: activation.History.ValkeyIncarnation,
			Destination: filepath.Join(filepath.Dir(activation.Prepare.FilesDirectory), "migration-inspection"),
		})
		require.ErrorIs(t, err, recovery.ErrConflict)
		ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
		defer cancel()
		_, err = ActivateRecovery(ctx, cfg, activation)
		require.ErrorIs(t, err, recovery.ErrConflict)
		_, err = storage.Open(cfg.RuntimeStorage())
		require.ErrorIs(t, err, storage.ErrImportRestricted)
	})
	// This case runs last because it reopens the source boundary.
	t.Run("open-boundary", func(t *testing.T) {
		db, err := openBackupSQL(source.config)
		require.NoError(t, err)
		witness, err := recovery.New(db)
		require.NoError(t, err)
		closed, err := witness.Current(t.Context(), deployment)
		require.NoError(t, err)
		_, err = witness.Approve(t.Context(), closed, "local-source", "operator-reopened-source")
		require.NoError(t, err)
		require.NoError(t, db.Close())
		destination := filepath.Join(filepath.Dir(source.capture.Directory), "open-boundary-backup")
		_, err = CaptureBackup(t.Context(), source.config, recovery.CaptureRequest{Destination: destination, Build: "test", OperationID: "capture-open-boundary", FencingEvidence: "controlled-source-stopped", KeyReference: "test-master-key"})
		require.ErrorIs(t, err, recovery.ErrClosed)
		require.NoDirExists(t, destination)
	})
}

// TestLocalToSharedMigrationSecondReplicaJoins starts a second process with its own instance and catalog state.
func TestLocalToSharedMigrationSecondReplicaJoins(t *testing.T) {
	if path := os.Getenv(localMigrationReplicaChild); path != "" {
		runLocalMigrationReplica(t, path)
		return
	}
	valkey, postgres, endpoint := localMigrationFixtures(t)
	source, cfg, prepare := populatedLocalMigrationSource(t)
	configureSharedRestore(t, cfg, valkey, postgres, endpoint)
	clearLocalMigrationValkey(t, cfg)
	request, _ := activateLocalMigration(t, source, cfg, prepare)
	requireLocalMigrationFleetHead(t, cfg, request, source.generation)

	first, err := New(cfg)
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	defer func() { require.NoError(t, first.Close(ctx)) }()
	require.True(t, first.admissionReady())
	require.Equal(t, source.generation, first.catalog.Current().GenerationID())

	parent := filepath.Join(t.TempDir(), "replica-two")
	_, err = productfiles.CreateDirectory(parent)
	require.NoError(t, err)
	values := map[string]string{"STARPORT_SECURITY_MASTER_KEY": cfg.Security.MasterKey, "STARPORT_DEPLOYMENT_ID": cfg.EffectivePaths().DeploymentID, "STARPORT_INSTANCE_ID": "migration-replica-two"}
	for key, value := range cfg.Catalog.CatalogValues() {
		if key != "STARMAP_STATE_DIR" {
			values["STARPORT_"+strings.TrimPrefix(key, "STARMAP_")] = value
		}
	}
	replica, err := config.NewLoader().WithPaths(config.PathsForConfigDir(parent)).WithEnvFiles().WithEnvironment(values).Load(ctx)
	require.NoError(t, err)
	replica.Storage, replica.Files = cfg.Storage, cfg.Files
	require.NotEqual(t, cfg.EffectivePaths().InstanceID, replica.EffectivePaths().InstanceID)
	require.NotEqual(t, cfg.EffectivePaths().RuntimeDir, replica.EffectivePaths().RuntimeDir)

	root := filepath.Join(t.TempDir(), "private")
	directory, err := productfiles.CreateDirectory(root)
	require.NoError(t, err)
	body, err := json.Marshal(replica, legacyjson.FormatDurationAsNano(true), json.Deterministic(true))
	require.NoError(t, err)
	fixture, err := json.Marshal(freshGatewayFixture{Paths: replica.EffectivePaths(), Config: body, Catalog: replica.Catalog.CatalogValues(), KeyHash: source.keyHash, Report: filepath.Join(root, "report.json")})
	require.NoError(t, err)
	require.NoError(t, directory.CompareAndPublish(ctx, "fixture.json", nil, fixture))
	command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestLocalToSharedMigrationSecondReplicaJoins$", "-test.timeout=2m", "-test.v")
	command.Env = append(os.Environ(), localMigrationReplicaChild+"="+filepath.Join(root, "fixture.json"))
	output, err := command.CombinedOutput()
	require.NoError(t, err, "%s", output)
	body, err = os.ReadFile(filepath.Join(root, "report.json"))
	require.NoError(t, err)
	var report localMigrationReplicaReport
	require.NoError(t, json.Unmarshal(body, &report))
	require.Equal(t, localMigrationReplicaReport{InstanceID: "migration-replica-two", StateDir: replica.EffectivePaths().RuntimeDir, Ready: true, Account: localMigrationAccounts[0], Generation: source.generation}, report)
	require.Equal(t, report.Generation, first.catalog.Current().GenerationID())
}

func runLocalMigrationReplica(t *testing.T, path string) {
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
	bundle, err := application.authorization.cache.Resolve(ctx, authorization.Identity{Subject: fixture.KeyHash})
	require.NoError(t, err)
	snapshot := application.catalog.Current()
	require.NotNil(t, snapshot)
	body, err = json.Marshal(localMigrationReplicaReport{InstanceID: cfg.EffectivePaths().InstanceID, StateDir: cfg.EffectivePaths().RuntimeDir, Ready: application.admissionReady(), Account: bundle.Account().Account.ID, Generation: snapshot.GenerationID()})
	require.NoError(t, err)
	directory, err := productfiles.ExistingDirectory(filepath.Dir(fixture.Report))
	require.NoError(t, err)
	require.NoError(t, directory.CompareAndPublish(ctx, filepath.Base(fixture.Report), nil, body))
}

// TestLocalToSharedMigrationRollback restarts the unchanged local source after the shared target activates.
// Local startup reads no recovery approval, so only the external writer fence protects this choice.
// Writes that the shared target accepted before rollback do not return to the local source.
func TestLocalToSharedMigrationRollback(t *testing.T) {
	valkey, postgres, endpoint := localMigrationFixtures(t)
	source, cfg, prepare := populatedLocalMigrationSource(t)
	configureSharedRestore(t, cfg, valkey, postgres, endpoint)
	clearLocalMigrationValkey(t, cfg)
	activateLocalMigration(t, source, cfg, prepare)
	store, err := storage.Open(cfg.RuntimeStorage())
	require.NoError(t, err)
	records, err := usage.Open(store, usage.Options{})
	require.NoError(t, err)
	require.NoError(t, records.Put(t.Context(), usage.Record{RequestID: "shared-only-request", KeyID: "shared-only-key", AccountID: localMigrationAccounts[0], Timestamp: time.Now().UTC(), Operation: usage.OperationChat, Status: usage.StatusOK, Tokens: usage.Tokens{Input: 1, Output: 1, Total: 2}, Cost: &usage.Cost{NanoUSD: 1, Currency: "USD"}}))
	require.NoError(t, store.Close())
	require.Contains(t, readLocalMigrationCensus(t, cfg).Usage, "shared-only-request")

	// Rollback reopens the local source. Its stores have not changed since capture.
	require.Equal(t, source.census, readLocalMigrationCensus(t, source.config))
	application, err := New(source.config)
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	require.True(t, application.admissionReady())
	_, err = application.authorization.cache.Resolve(ctx, authorization.Identity{Subject: source.keyHash})
	require.NoError(t, err)
	require.Equal(t, source.generation, application.catalog.Current().GenerationID())
	require.NoError(t, application.Close(ctx))
	require.Equal(t, source.census, readLocalMigrationCensus(t, source.config))
}
