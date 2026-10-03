package app

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agentstation/starmap/pkg/productfiles"
	"github.com/agentstation/starport/internal/account"
	"github.com/agentstation/starport/internal/apikey"
	"github.com/agentstation/starport/internal/authorization"
	"github.com/agentstation/starport/internal/blob"
	"github.com/agentstation/starport/internal/cli"
	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/configrevision"
	"github.com/agentstation/starport/internal/identity"
	"github.com/agentstation/starport/internal/recovery"
	"github.com/agentstation/starport/internal/sqlstore"
	"github.com/agentstation/starport/internal/storage"
	"github.com/aws/aws-sdk-go-v2/aws"
	awscredentials "github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/go-sql-driver/mysql"
	"github.com/stretchr/testify/require"
)

const (
	populatedAdopt    = "adopt"
	populatedPrepare  = "prepare"
	populatedActivate = "activate"
	populatedInspect  = "inspect"
	populatedRetained = "adoption-test:retained"
	populatedAccount  = "adoption-tenant"
)

// populatedFixture is one live deployment that an earlier empty-target import opened.
// capture closes it, captures C, and writes the zero-prefix history H that binds C.
type populatedFixture struct {
	cfg         *config.Config
	environment map[string]string
	// live selects the catalog state directory of the prior approval.
	live        map[string]string
	valkey      *adoptValkey
	prior       recovery.Record
	key         apikey.APIKey
	request     PopulatedRecoveryRequest
	requestFile string
	private     string
	adoption    string
}

// populatedAdoptionFixture opens a live deployment on a private Valkey process, shared PostgreSQL, and object storage.
// The shared storage recipe refuses MySQL with Valkey, so a populated deployment has PostgreSQL relational state.
func populatedAdoptionFixture(t *testing.T) *populatedFixture {
	t.Helper()
	postgres, endpoint := os.Getenv("TEST_POSTGRES_URL"), os.Getenv("TEST_BLOB_S3_ENDPOINT")
	if postgres == "" || endpoint == "" {
		t.Skip("UNVERIFIED: populated adoption needs native PostgreSQL and object storage")
	}
	valkey := startAdoptValkey(t)
	cfg, prepare := boundedActivationSourceFixture(t)
	configureSharedRestore(t, cfg, valkey.url, postgres, endpoint)
	cfg, environment := operatorPrimaryConfiguration(t, cfg)
	cfg, first := activationPreparedFixture(t, cfg, prepare)
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	activated, err := ActivateRecovery(ctx, cfg, first)
	require.NoError(t, err)
	require.True(t, activated.CurrentAdmissionValid)
	populatedSharedConfiguration(t, cfg)
	// The open deployment acknowledged these records before the fence. C captures them, and adoption keeps them in place.
	store, err := storage.OpenValkey(cfg.RuntimeStorage().Valkey)
	require.NoError(t, err)
	defer func() { require.NoError(t, store.Close()) }()
	require.NoError(t, store.Set(ctx, populatedRetained, []byte("retained before capture")))
	accounts, err := account.Open(store)
	require.NoError(t, err)
	_, err = accounts.Create(ctx, account.Account{ID: populatedAccount, Name: "Adoption tenant", Active: true, CredentialStrategy: account.StrategyOperatorFirst})
	require.NoError(t, err)
	keys, err := apikey.Open(store)
	require.NoError(t, err)
	issuer, err := apikey.NewIssuer(keys, apikey.WithAccountChecker(accounts))
	require.NoError(t, err)
	issued, err := issuer.IssueInitial(ctx, apikey.IssueRequest{Name: "adopted", AccountID: populatedAccount, Scopes: []string{"inference:write"}})
	require.NoError(t, err)
	return &populatedFixture{cfg: cfg, environment: environment, valkey: valkey, prior: populatedWitness(t, cfg).approved, key: issued.APIKey}
}

// populatedSharedConfiguration seeds the first shared configuration revision.
// The populated deployment selects distributed storage, so shared management
// owns its deployment-scope catalog settings. Startup refuses a deployment
// without a stored head, so the operator runs this step after activation and
// before any gateway starts. The store lives in the shared SQL database, so
// the step also precedes capture. It matches `starport config init --shared --yes`.
func populatedSharedConfiguration(t *testing.T, cfg *config.Config) {
	t.Helper()
	require.True(t, cfg.SharedManagement(), "the populated deployment selects shared management")
	result, err := InitializeSharedConfiguration(t.Context(), cfg, configrevision.Request{OperationID: "initialize-populated-" + rand.Text()})
	require.NoError(t, err)
	require.True(t, result.Written)
	require.Equal(t, int64(1), result.Revision.Sequence)
}

// capture closes the live boundary, captures C, and retains H and the private operator request.
func (f *populatedFixture) capture(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	_, err := CloseBackupBoundary(ctx, f.cfg)
	require.NoError(t, err)
	parent := filepath.Join(t.TempDir(), "private-capture")
	_, err = productfiles.CreateDirectory(parent)
	require.NoError(t, err)
	receipt, err := CaptureBackup(ctx, f.cfg, recovery.CaptureRequest{Destination: filepath.Join(parent, "populated"), Build: "test", OperationID: "capture-populated", FencingEvidence: "operator-fenced-writers", KeyReference: "test-master-key"})
	require.NoError(t, err)
	f.adoption = filepath.Join(t.TempDir(), "adoption")
	_, err = productfiles.CreateDirectory(f.adoption)
	require.NoError(t, err)
	// The operator selects a new empty catalog state directory for adoption and the fresh gateway.
	f.live = maps.Clone(f.environment)
	state := filepath.Join(f.adoption, "catalog-state")
	_, err = productfiles.CreateDirectory(state)
	require.NoError(t, err)
	f.environment[operatorCatalogStateEnvironment] = state
	f.cfg, err = config.NewLoader().WithEnvironment(f.environment).Load(ctx)
	require.NoError(t, err)
	prepare := recovery.PrepareRequest{VerifyRequest: recovery.VerifyRequest{Directory: receipt.Directory, ManifestSHA256: receipt.ManifestSHA256}, FilesDirectory: filepath.Join(f.adoption, "prepared"), Operation: recovery.RestoreOperation{ID: "adopt-populated", FencingEvidence: "operator-fenced-writers"}}
	preparation := filepath.Join(f.adoption, "populated-preparation")
	_, err = productfiles.CreateDirectory(preparation)
	require.NoError(t, err)
	f.request = PopulatedRecoveryRequest{Activation: activationHistoryFixture(t, f.cfg, prepare), PriorApproval: f.prior, PreparationDirectory: preparation}
	f.private = filepath.Join(t.TempDir(), "private-operator")
	_, err = productfiles.CreateDirectory(f.private)
	require.NoError(t, err)
	f.requestFile = operatorPrivateJSON(t, f.private, "adoption.json", f.request)
}

type populatedWitnessState struct {
	current, approved         recovery.Record
	currentErr, approvedError string
}

func populatedWitness(t *testing.T, cfg *config.Config) populatedWitnessState {
	t.Helper()
	db, err := openBackupSQL(cfg)
	require.NoError(t, err)
	defer func() { require.NoError(t, db.Close()) }()
	witness, err := recovery.New(db)
	require.NoError(t, err)
	var state populatedWitnessState
	state.current, err = witness.Current(t.Context(), cfg.EffectivePaths().DeploymentID)
	if err != nil {
		state.currentErr = err.Error()
	}
	state.approved, err = witness.Approved(t.Context(), cfg.EffectivePaths().DeploymentID)
	if err != nil {
		state.approvedError = err.Error()
	}
	return state
}

// populatedCensus holds every native record, the witness, every object, and every private file of one fixture.
type populatedCensus struct {
	KV      map[string]string
	Witness populatedWitnessState
	Blobs   map[string]string
	Files   map[string]string
}

func (f *populatedFixture) census(t *testing.T) populatedCensus {
	t.Helper()
	return populatedStateCensus(t, f.cfg, f.cfg.EffectivePaths().ConfigDir, f.adoption)
}

func populatedStateCensus(t *testing.T, cfg *config.Config, roots ...string) populatedCensus {
	t.Helper()
	store, err := storage.OpenValkey(cfg.RuntimeStorage().Valkey)
	require.NoError(t, err)
	defer func() { require.NoError(t, store.Close()) }()
	keys, err := store.ScanWithPrefix(t.Context(), "", 100000)
	require.NoError(t, err)
	census := populatedCensus{KV: make(map[string]string, len(keys)), Witness: populatedWitness(t, cfg), Blobs: make(map[string]string), Files: operatorEvidenceCensus(t, roots...)}
	for _, key := range keys {
		value, err := store.Get(t.Context(), key)
		require.NoError(t, err)
		census.KV[key] = canonicalRecordSHA256(value)
	}
	object := cfg.Files.ObjectStore
	client := s3.NewFromConfig(aws.Config{Region: object.Region, Credentials: awscredentials.NewStaticCredentialsProvider(object.AccessKeyID, object.SecretAccessKey, "")}, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(object.Endpoint)
		o.UsePathStyle = true
	})
	pages := s3.NewListObjectsV2Paginator(client, &s3.ListObjectsV2Input{Bucket: aws.String(object.Bucket)})
	for pages.HasMorePages() {
		page, err := pages.NextPage(t.Context())
		require.NoError(t, err)
		for _, item := range page.Contents {
			census.Blobs[aws.ToString(item.Key)] = aws.ToString(item.ETag)
		}
	}
	return census
}

func populatedArguments(step, request, prepared, decision string) []string {
	arguments := []string{operatorProgram, operatorBackup, populatedAdopt, step, operatorRequestFlag, request, "--timeout", "3m", operatorJSONFlag}
	if prepared != "" {
		arguments = append(arguments, "--prepared-sha256", prepared)
	}
	if decision != "" {
		arguments = append(arguments, "--decision-sha256", decision)
	}
	return arguments
}

func populatedRun(t *testing.T, environment map[string]string, arguments []string) ([]byte, error) {
	t.Helper()
	var output, diagnostic bytes.Buffer
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	err := cli.Run(ctx, arguments, operatorCommandDependencies(t, environment, &output, &diagnostic))
	return output.Bytes(), err
}

func populatedPrepared(t *testing.T, body []byte) string {
	t.Helper()
	var result PopulatedRecoveryPreparation
	require.NoError(t, json.Unmarshal(body, &result, json.RejectUnknownMembers(true)))
	require.True(t, populatedRecordDigest(result.PreparedSHA256))
	require.Equal(t, "activate with the retained prepared digest", result.NextAction)
	return result.PreparedSHA256
}

func populatedResult(t *testing.T, body []byte) recovery.ActivationResult {
	t.Helper()
	var result recovery.ActivationResult
	require.NoError(t, json.Unmarshal(body, &result))
	return result
}

// refuse runs one command that must fail and requires every native record, object, and private file to stay unchanged.
func (f *populatedFixture) refuse(t *testing.T, environment map[string]string, arguments []string) error {
	t.Helper()
	before := f.census(t)
	output, err := populatedRun(t, environment, arguments)
	require.Error(t, err)
	if len(output) != 0 {
		// A missing required flag prints command usage and no result.
		require.Contains(t, string(output), "USAGE:")
		require.NotContains(t, string(output), "_sha256\"")
	}
	require.Equal(t, before, f.census(t))
	return err
}

// populatedMySQLRecipeFailure is the fixed operator instruction of the shared storage recipe for a MySQL selection.
const populatedMySQLRecipeFailure = "shared storage requires PostgreSQL for relational state and recovery approval"

// populatedMySQLTarget creates a private migrated MySQL database and returns its DSN and a census of its tables.
func populatedMySQLTarget(t *testing.T) (string, func() map[string]string) {
	t.Helper()
	address := os.Getenv("TEST_MYSQL_DSN")
	if address == "" {
		t.Skip("UNVERIFIED: the MySQL refusal needs the native MySQL fixture")
	}
	parsed, err := mysql.ParseDSN(address)
	require.NoError(t, err)
	admin, err := sqlstore.Open(sqlstore.Config{Type: sqlstore.TypeMySQL, MySQL: sqlstore.MySQLConfig{DSN: address}})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, admin.Close()) })
	name := "populated_adoption_" + strings.ToLower(rand.Text())
	_, err = admin.ExecContext(t.Context(), "CREATE DATABASE `"+name+"`")
	require.NoError(t, err)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, err := admin.ExecContext(ctx, "DROP DATABASE `"+name+"`")
		require.NoError(t, err)
	})
	parsed.DBName = name
	dsn := parsed.FormatDSN()
	db, err := sqlstore.Open(sqlstore.Config{Type: sqlstore.TypeMySQL, MySQL: sqlstore.MySQLConfig{DSN: dsn}})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	require.NoError(t, db.Migrate(t.Context()))
	census := func() map[string]string {
		rows, err := db.QueryContext(t.Context(), "SELECT table_name FROM information_schema.tables WHERE table_schema = ? ORDER BY table_name", name)
		require.NoError(t, err)
		var tables []string
		for rows.Next() {
			var table string
			require.NoError(t, rows.Scan(&table))
			tables = append(tables, table)
		}
		require.NoError(t, rows.Err())
		require.NoError(t, rows.Close())
		require.NotEmpty(t, tables)
		result := make(map[string]string, len(tables))
		for _, table := range tables {
			var checked string
			var checksum sql.NullInt64
			require.NoError(t, db.QueryRowContext(t.Context(), "CHECKSUM TABLE `"+table+"` EXTENDED").Scan(&checked, &checksum))
			result[table] = fmt.Sprint(checksum)
		}
		return result
	}
	return dsn, census
}

// populatedCanonicalFile returns one existing canonical file that the in-place verification checks.
func populatedCanonicalFile(t *testing.T, f *populatedFixture) string {
	t.Helper()
	_, selected, _, err := configuredCanonicalFilesWithSource(t.Context(), f.cfg, f.request.Activation.Prepare, true, nil)
	require.NoError(t, err)
	for _, role := range selected {
		for _, file := range role.tree.Files {
			path := filepath.Join(role.tree.Destination, filepath.FromSlash(file.Relative))
			if info, err := os.Lstat(path); err == nil && info.Mode().IsRegular() {
				return path
			}
		}
	}
	require.FailNow(t, "the capture selects no existing canonical file")
	return ""
}

// populatedPrefixHistory copies H and puts two identity steps before the final steps.
func populatedPrefixHistory(t *testing.T, request PopulatedRecoveryRequest) PopulatedRecoveryRequest {
	t.Helper()
	original := request.Activation.History.HistoryDirectory
	directory := original + "-prefix"
	for _, path := range []string{directory, filepath.Join(directory, "payloads")} {
		_, err := productfiles.CreateDirectory(path)
		require.NoError(t, err)
	}
	grant := identity.AccountGrant{AccountID: "tenant", UserID: "person", CreatedAt: time.Now().UTC()}
	create, err := identity.NewRecoveryTransition([]identity.RecoveryIdentityEvent{{Grant: &identity.RecoveryGrantChange{After: &grant}}})
	require.NoError(t, err)
	withdraw, err := identity.NewRecoveryTransition([]identity.RecoveryIdentityEvent{{Grant: &identity.RecoveryGrantChange{Before: &grant}}})
	require.NoError(t, err)
	payloads := []any{create, withdraw}
	for i := range 2 {
		body, err := os.ReadFile(filepath.Join(original, fmt.Sprintf("payloads/%06d.json", i+1)))
		require.NoError(t, err)
		payloads = append(payloads, jsontext.Value(body))
	}
	kinds := []string{"sql_identity", "sql_identity", "kv_authorization_final", "sql_authorization_final"}
	steps := make([]map[string]any, 0, len(payloads))
	for i, value := range payloads {
		body, err := json.Marshal(value, json.Deterministic(true))
		require.NoError(t, err)
		path := fmt.Sprintf("payloads/%06d.json", i+1)
		require.NoError(t, os.WriteFile(filepath.Join(directory, path), body, 0600))
		steps = append(steps, map[string]any{"ordinal": i + 1, "kind": kinds[i], "path": path, "size": len(body), "sha256": canonicalRecordSHA256(body), "evidence_source_ids": []string{"controlled-source"}})
	}
	body, err := os.ReadFile(filepath.Join(original, "history.json"))
	require.NoError(t, err)
	var manifest map[string]any
	require.NoError(t, json.Unmarshal(body, &manifest))
	manifest["steps"] = steps
	body, err = json.Marshal(manifest, json.Deterministic(true))
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(directory, "history.json"), body, 0600))
	request.Activation.History.HistoryDirectory = directory
	request.Activation.History.HistorySHA256 = canonicalRecordSHA256(body)
	return request
}

// populatedCoherent requires one open approval that the SQL witness and the native KV authority hold together,
// and a fresh gateway that admits work.
func populatedCoherent(t *testing.T, cfg *config.Config, prior recovery.Record, key apikey.APIKey) recovery.Record {
	t.Helper()
	approved := populatedApproved(t, cfg, prior)
	populatedGateway(t, cfg, key)
	return approved
}

// populatedApproved requires one open approval that the SQL witness and the native KV authority hold together.
func populatedApproved(t *testing.T, cfg *config.Config, prior recovery.Record) recovery.Record {
	t.Helper()
	state := populatedWitness(t, cfg)
	require.Empty(t, state.currentErr)
	require.Empty(t, state.approvedError)
	require.Equal(t, state.current, state.approved)
	require.True(t, state.approved.Open)
	require.Greater(t, state.approved.Epoch, prior.Epoch)
	db, err := openBackupSQL(cfg)
	require.NoError(t, err)
	defer func() { require.NoError(t, db.Close()) }()
	witness, err := recovery.New(db)
	require.NoError(t, err)
	store, err := storage.OpenValkey(cfg.RuntimeStorage().Valkey)
	require.NoError(t, err)
	defer func() { require.NoError(t, store.Close()) }()
	authority, err := witness.OpenAuthority(t.Context(), store.(storage.IncarnationProvider), cfg.EffectivePaths().DeploymentID)
	require.NoError(t, err)
	require.NoError(t, authority.Check(t.Context()))
	return state.approved
}

// populatedGateway requires a fresh gateway that admits work and resolves the credential that the deployment held before the fence.
// The gateway starts its catalog, so sealed inspection after it reports restricted. Callers run it after the sealed checks.
func populatedGateway(t *testing.T, cfg *config.Config, key apikey.APIKey) {
	t.Helper()
	application, err := New(cfg)
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	defer func() { require.NoError(t, application.Close(ctx)) }()
	require.True(t, application.admissionReady())
	_, err = application.authorization.cache.Resolve(t.Context(), authorization.Identity{Subject: key.Hash})
	require.NoError(t, err)
}

func TestPopulatedAdoptionOperatorCommands(t *testing.T) {
	f := populatedAdoptionFixture(t)
	f.capture(t)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Minute)
	defer cancel()

	t.Run("prepare-refusals", func(t *testing.T) {
		cases := []struct {
			name   string
			change func(PopulatedRecoveryRequest) PopulatedRecoveryRequest
			want   error
		}{
			{"prior-epoch-differs", func(r PopulatedRecoveryRequest) PopulatedRecoveryRequest { r.PriorApproval.Epoch++; return r }, recovery.ErrConflict},
			{"prior-backend-differs", func(r PopulatedRecoveryRequest) PopulatedRecoveryRequest {
				r.PriorApproval.BackendID += "-other"
				return r
			}, recovery.ErrConflict},
			{"prefix-step", func(r PopulatedRecoveryRequest) PopulatedRecoveryRequest { return populatedPrefixHistory(t, r) }, recovery.ErrAdoptionPrefix},
			{"target-differs", func(r PopulatedRecoveryRequest) PopulatedRecoveryRequest {
				r.Activation.History.ExpectedTargetSHA256 = strings.Repeat("0", 64)
				return r
			}, recovery.ErrConflict},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				changed := tc.change(f.request)
				file := operatorPrivateJSON(t, f.private, tc.name+".json", changed)
				f.refuse(t, f.environment, populatedArguments(populatedPrepare, file, "", ""))
				_, err := PreparePopulatedRecovery(ctx, f.cfg, changed)
				require.ErrorIs(t, err, tc.want)
			})
		}
	})

	t.Run("live-catalog-state-directory", func(t *testing.T) {
		err := f.refuse(t, f.live, populatedArguments(populatedPrepare, f.requestFile, "", ""))
		require.ErrorIs(t, err, ErrCatalogDirectoryOccupied)
		require.ErrorIs(t, err, recovery.ErrConflict)
		require.ErrorContains(t, err, "set STARPORT_CATALOG_STATE_DIR to a new empty directory")
		// The owner inspection reads the actual gateway record of the live directory and the absent record of the new one.
		live, loadErr := config.NewLoader().WithEnvironment(f.live).Load(ctx)
		require.NoError(t, loadErr)
		target, _, targetErr := catalogSettings(live).RecoveryTopologyTarget()
		require.NoError(t, targetErr)
		require.NotEmpty(t, target.Directory)
		require.ErrorContains(t, err, target.Directory)
		before := f.census(t)
		require.ErrorIs(t, checkPopulatedCatalogDirectory(ctx, live), ErrCatalogDirectoryOccupied)
		require.NoError(t, checkPopulatedCatalogDirectory(ctx, f.cfg))
		require.Equal(t, before, f.census(t))
	})

	t.Run("backend-refusals", func(t *testing.T) {
		cases := []struct {
			name     string
			override map[string]string
			change   func(*config.Config)
			want     error
		}{
			{"badger", map[string]string{"STARPORT_STORAGE_MODE": storage.StorageTypeBadger}, func(c *config.Config) { c.Storage.Mode = storage.StorageTypeBadger }, storage.ErrPopulatedImportUnsupported},
			{"sqlite", map[string]string{"STARPORT_STORAGE_SQL_MODE": sqlstore.TypeSQLite}, func(c *config.Config) { c.Storage.SQL.Mode = sqlstore.TypeSQLite }, sqlstore.ErrPopulatedBackend},
			{"filesystem", map[string]string{"STARPORT_FILES_BACKEND": config.BlobBackendFilesystem}, func(c *config.Config) { c.Files.Backend = config.BlobBackendFilesystem }, blob.ErrPopulatedClaimUnsupported},
			// The shared storage recipe refuses MySQL with Valkey before any recovery owner opens.
			{"mysql", map[string]string{"STARPORT_STORAGE_SQL_MODE": sqlstore.TypeMySQL}, nil, nil},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				environment := map[string]string{}
				for key, value := range f.environment {
					environment[key] = value
				}
				for key, value := range tc.override {
					environment[key] = value
				}
				var mysqlCensus func() map[string]string
				if tc.name == "mysql" {
					environment["STARPORT_STORAGE_SQL_MYSQL_DSN"], mysqlCensus = populatedMySQLTarget(t)
				}
				var mysqlBefore map[string]string
				if mysqlCensus != nil {
					mysqlBefore = mysqlCensus()
				}
				// Shared configuration may refuse SQLite and filesystem selections before the owner check.
				prepareErr := f.refuse(t, environment, populatedArguments(populatedPrepare, f.requestFile, "", ""))
				activateErr := f.refuse(t, environment, populatedArguments(populatedActivate, f.requestFile, strings.Repeat("a", 64), ""))
				inspectErr := f.refuse(t, environment, populatedArguments(populatedInspect, f.requestFile, strings.Repeat("a", 64), strings.Repeat("b", 64)))
				if tc.change == nil {
					_, loadErr := config.NewLoader().WithEnvironment(environment).Load(ctx)
					require.EqualError(t, config.OperatorError(loadErr), populatedMySQLRecipeFailure)
					for _, err := range []error{prepareErr, activateErr, inspectErr} {
						require.EqualError(t, err, populatedMySQLRecipeFailure)
					}
					require.Equal(t, mysqlBefore, mysqlCensus(), "the refused selection changed the MySQL target")
					return
				}
				selected := *f.cfg
				tc.change(&selected)
				before := f.census(t)
				_, err := PreparePopulatedRecovery(ctx, &selected, f.request)
				require.ErrorIs(t, err, tc.want)
				retry := f.request
				retry.ExpectedPreparedSHA256 = strings.Repeat("a", 64)
				_, err = ActivatePopulatedRecovery(ctx, &selected, retry)
				require.ErrorIs(t, err, tc.want)
				retry.Activation.ExpectedDecisionSHA256 = strings.Repeat("b", 64)
				_, err = InspectPopulatedRecovery(ctx, &selected, retry)
				require.ErrorIs(t, err, tc.want)
				require.Equal(t, before, f.census(t))
			})
		}
	})

	canonical := populatedCanonicalFile(t, f)
	original, err := os.ReadFile(canonical)
	require.NoError(t, err)
	t.Run("prepare-changed-canonical-file", func(t *testing.T) {
		require.NoError(t, os.WriteFile(canonical, append(bytes.Clone(original), '\n'), 0600))
		defer func() { require.NoError(t, os.WriteFile(canonical, original, 0600)) }()
		f.refuse(t, f.environment, populatedArguments(populatedPrepare, f.requestFile, "", ""))
	})

	before := f.census(t)
	body, err := populatedRun(t, f.environment, populatedArguments(populatedPrepare, f.requestFile, "", ""))
	require.NoError(t, err)
	prepared := populatedPrepared(t, body)
	require.Equal(t, before.KV, f.census(t).KV, "preparation placed a native claim")
	require.Equal(t, before.Witness, f.census(t).Witness, "preparation changed the witness")
	require.Equal(t, before.Blobs, f.census(t).Blobs, "preparation changed object storage")
	retained := f.census(t)
	body, err = populatedRun(t, f.environment, populatedArguments(populatedPrepare, f.requestFile, "", ""))
	require.NoError(t, err)
	require.Equal(t, prepared, populatedPrepared(t, body), "exact preparation retry must return the retained record")
	require.Equal(t, retained, f.census(t))

	t.Run("activation-refusals", func(t *testing.T) {
		f.refuse(t, f.environment, populatedArguments(populatedActivate, f.requestFile, "", ""))
		f.refuse(t, f.environment, populatedArguments(populatedActivate, f.requestFile, strings.Repeat("0", 64), ""))
		f.refuse(t, f.environment, populatedArguments(populatedInspect, f.requestFile, prepared, strings.Repeat("0", 64)))
		// The first claim checks the catalog state directory again, so a changed selection refuses before any claim.
		require.ErrorIs(t, f.refuse(t, f.live, populatedArguments(populatedActivate, f.requestFile, prepared, "")), ErrCatalogDirectoryOccupied)
		require.NoError(t, os.WriteFile(canonical, append(bytes.Clone(original), '\n'), 0600))
		f.refuse(t, f.environment, populatedArguments(populatedActivate, f.requestFile, prepared, ""))
		require.NoError(t, os.WriteFile(canonical, original, 0600))
	})

	// A live KV record that differs from C refuses after the SQL claim. The deployment stays closed and restricted.
	// After the operator restores the fenced record, the exact retry continues the same claim.
	t.Run("changed-live-record-fails-closed", func(t *testing.T) {
		store, err := storage.OpenValkey(f.cfg.RuntimeStorage().Valkey)
		require.NoError(t, err)
		defer func() { require.NoError(t, store.Close()) }()
		retainedValue, err := store.Get(ctx, populatedRetained)
		require.NoError(t, err)
		require.NoError(t, store.Set(ctx, populatedRetained, []byte("changed after capture")))
		output, err := populatedRun(t, f.environment, populatedArguments(populatedActivate, f.requestFile, prepared, ""))
		require.Error(t, err)
		require.Empty(t, output)
		require.NoFileExists(t, filepath.Join(f.request.Activation.ActivationDirectory, "decision.json"))
		state := populatedWitness(t, f.cfg)
		require.False(t, state.current.Open)
		require.NotEmpty(t, state.approvedError)
		require.False(t, freshHistoryConstruction(t, f.cfg).Started)
		require.NoError(t, store.Set(ctx, populatedRetained, retainedValue))
	})

	body, err = populatedRun(t, f.environment, populatedArguments(populatedActivate, f.requestFile, prepared, ""))
	require.NoError(t, err)
	completed := populatedResult(t, body)
	require.True(t, populatedRecordDigest(completed.DecisionSHA256))
	require.Equal(t, 3, completed.CompletedPhases)
	require.True(t, completed.HistoricallyComplete)
	require.True(t, completed.CurrentAdmissionValid)
	require.False(t, completed.Restricted)
	require.Equal(t, "start a fresh gateway and verify readiness", completed.NextAction)
	approved := populatedApproved(t, f.cfg, f.prior)

	sealed := f.census(t)
	decision := completed.DecisionSHA256
	body, err = populatedRun(t, f.environment, populatedArguments(populatedInspect, f.requestFile, prepared, decision))
	require.NoError(t, err)
	require.Equal(t, completed, populatedResult(t, body))
	body, err = populatedRun(t, f.environment, populatedArguments(populatedActivate, f.requestFile, prepared, decision))
	require.NoError(t, err)
	require.Equal(t, completed, populatedResult(t, body), "exact sealed retry")
	require.Equal(t, completed, operatorRunFreshCommand(t, f.environment, populatedArguments(populatedActivate, f.requestFile, prepared, decision)), "exact sealed retry in a fresh process")
	require.Equal(t, sealed, f.census(t))
	require.Equal(t, approved, populatedApproved(t, f.cfg, f.prior))

	t.Run("sealed-refusals", func(t *testing.T) {
		f.refuse(t, f.environment, populatedArguments(populatedActivate, f.requestFile, prepared, ""))
		f.refuse(t, f.environment, populatedArguments(populatedActivate, f.requestFile, prepared, strings.Repeat("0", 64)))
		f.refuse(t, f.environment, populatedArguments(populatedInspect, f.requestFile, strings.Repeat("0", 64), decision))
		ordinary := operatorPrivateJSON(t, f.private, "ordinary-activation.json", f.request.Activation)
		f.refuse(t, f.environment, operatorCommandArguments(operatorActivate, ordinary, decision))
	})

	t.Run("shipping-binary", func(t *testing.T) {
		binary := populatedBinary(t)
		for _, step := range []string{populatedInspect, populatedActivate} {
			output, err := populatedBinaryRun(t, binary, f, "sealed-"+step, populatedArguments(step, f.requestFile, prepared, decision))
			require.NoError(t, err, "shipping operator command failed; private output retained")
			require.Equal(t, completed, populatedResult(t, output))
		}
		require.Equal(t, sealed, f.census(t))
	})

	populatedGateway(t, f.cfg, f.key)
	require.Equal(t, approved, populatedApproved(t, f.cfg, f.prior))

	// Closing the approval withdraws current permission. Inspection and exact retry report it and renew nothing.
	db, err := openBackupSQL(f.cfg)
	require.NoError(t, err)
	witness, err := recovery.New(db)
	require.NoError(t, err)
	withdrawn, err := witness.Close(ctx, approved)
	require.NoError(t, err)
	require.NoError(t, db.Close())
	for _, step := range []string{populatedInspect, populatedActivate} {
		body, err = populatedRun(t, f.environment, populatedArguments(step, f.requestFile, prepared, decision))
		require.NoError(t, err)
		status := populatedResult(t, body)
		require.True(t, status.HistoricallyComplete)
		require.False(t, status.CurrentAdmissionValid)
		require.True(t, status.Restricted)
	}
	require.Equal(t, withdrawn, populatedWitness(t, f.cfg).current)
}

func populatedBinary(t *testing.T) string {
	t.Helper()
	binary := os.Getenv("STARPORT_RECOVERY_OPERATOR_BINARY")
	if binary == "" {
		t.Skip("UNVERIFIED: supply the exact source-built Starport executable")
	}
	return binary
}

// populatedBinaryRun runs the shipping executable and retains its output privately instead of printing credentials.
func populatedBinaryRun(t *testing.T, binary string, f *populatedFixture, name string, arguments []string) ([]byte, error) {
	t.Helper()
	return populatedBinaryRunWith(t, binary, f.environment, f, name, arguments)
}

func populatedBinaryRunWith(t *testing.T, binary string, environment map[string]string, f *populatedFixture, name string, arguments []string) ([]byte, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, binary, arguments[1:]...) // #nosec G204 G702 -- The test harness selects the source-built executable.
	command.Env = operatorChildEnvironment(environment)
	command.Dir = f.cfg.EffectivePaths().ConfigDir
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	err := command.Run()
	operatorPrivateJSON(t, f.private, "binary-"+name+"-output.json", map[string][]byte{"stdout": stdout.Bytes(), "stderr": stderr.Bytes()})
	return stdout.Bytes(), err
}

// adoptWithBinary prepares and activates through the shipping executable.
func (f *populatedFixture) adoptWithBinary(t *testing.T, binary string, environment map[string]string) recovery.ActivationResult {
	t.Helper()
	output, err := populatedBinaryRunWith(t, binary, environment, f, "prepare", populatedArguments(populatedPrepare, f.requestFile, "", ""))
	require.NoError(t, err, "shipping preparation failed; private output retained")
	prepared := populatedPrepared(t, output)
	output, err = populatedBinaryRunWith(t, binary, environment, f, "activate", populatedArguments(populatedActivate, f.requestFile, prepared, ""))
	require.NoError(t, err, "shipping activation failed; private output retained")
	completed := populatedResult(t, output)
	require.True(t, completed.HistoricallyComplete)
	require.True(t, completed.CurrentAdmissionValid)
	output, err = populatedBinaryRunWith(t, binary, environment, f, "inspect", populatedArguments(populatedInspect, f.requestFile, prepared, completed.DecisionSHA256))
	require.NoError(t, err, "shipping inspection failed; private output retained")
	require.Equal(t, completed, populatedResult(t, output))
	return completed
}

func TestPopulatedAdoptionProcess(t *testing.T) {
	t.Run("valkey-restart-with-persistent-data", func(t *testing.T) {
		binary := populatedBinary(t)
		f := populatedAdoptionFixture(t)
		store, err := storage.OpenValkey(f.cfg.RuntimeStorage().Valkey)
		require.NoError(t, err)
		// The gateway acknowledged this domain write during the open epoch.
		require.NoError(t, store.Set(t.Context(), "adoption-test:acknowledged", []byte("acknowledged before restart")))
		require.NoError(t, store.Close())
		before := adoptIncarnation(t, f.cfg, f.valkey.url)
		f.valkey.restart(t)
		after := adoptIncarnation(t, f.cfg, f.valkey.url)
		require.NotEqual(t, before, after)
		refused := freshHistoryConstruction(t, f.cfg)
		require.False(t, refused.Started, "the stored authority must refuse a restarted Valkey process")
		f.capture(t)
		f.adoptWithBinary(t, binary, f.environment)
		// The adoption boundary kept the old backend identity. Approval binds the restarted process.
		require.Equal(t, before, f.prior.BackendID)
		require.Equal(t, after, populatedCoherent(t, f.cfg, f.prior, f.key).BackendID)
		store, err = storage.OpenValkey(f.cfg.RuntimeStorage().Valkey)
		require.NoError(t, err)
		defer func() { require.NoError(t, store.Close()) }()
		value, err := store.Get(t.Context(), "adoption-test:acknowledged")
		require.NoError(t, err)
		require.Equal(t, "acknowledged before restart", string(value))
	})

	// The replica becomes the primary. The old primary then acknowledges a write that the new primary never receives.
	// The zero-prefix delivery cannot detect that loss. The operator attestation covers it.
	// The old primary stays reachable, and the closed permission blocks dispatch through it.
	t.Run("promotion-acknowledged-loss-old-primary", func(t *testing.T) {
		binary := populatedBinary(t)
		f := populatedAdoptionFixture(t)
		replica, address := f.valkey.replica(t)
		f.valkey.promote(t, replica)
		old, err := storage.OpenValkey(f.cfg.RuntimeStorage().Valkey)
		require.NoError(t, err)
		defer func() { require.NoError(t, old.Close()) }()
		require.NoError(t, old.Set(t.Context(), "adoption-test:acknowledged-lost", []byte("acknowledged by the old primary")))
		oldConfig := f.cfg
		f.environment["STARPORT_STORAGE_VALKEY_URL"] = address
		promoted, err := config.NewLoader().WithEnvironment(f.environment).Load(t.Context())
		require.NoError(t, err)
		f.cfg = promoted
		require.NotEqual(t, adoptIncarnation(t, f.cfg, f.valkey.url), adoptIncarnation(t, f.cfg, address))
		require.False(t, freshHistoryConstruction(t, f.cfg).Started, "the stored authority must refuse a promoted Valkey process")
		f.capture(t)
		require.Equal(t, address, f.cfg.Storage.Valkey.URL)
		f.adoptWithBinary(t, binary, f.environment)
		require.Equal(t, adoptIncarnation(t, f.cfg, address), populatedCoherent(t, f.cfg, f.prior, f.key).BackendID)
		current, err := storage.OpenValkey(f.cfg.RuntimeStorage().Valkey)
		require.NoError(t, err)
		defer func() { require.NoError(t, current.Close()) }()
		_, err = current.Get(t.Context(), "adoption-test:acknowledged-lost")
		require.ErrorIs(t, err, storage.ErrNotFound, "adoption copies no record; the lost write stays lost")
		value, err := old.Get(t.Context(), "adoption-test:acknowledged-lost")
		require.NoError(t, err)
		require.Equal(t, "acknowledged by the old primary", string(value))
		reply := freshHistoryConstruction(t, oldConfig)
		require.False(t, reply.Started, "the reachable old primary must not admit work after adoption")
		require.False(t, reply.OtherError)
		db, err := openBackupSQL(oldConfig)
		require.NoError(t, err)
		defer func() { require.NoError(t, db.Close()) }()
		witness, err := recovery.New(db)
		require.NoError(t, err)
		_, err = witness.OpenAuthority(t.Context(), old.(storage.IncarnationProvider), oldConfig.EffectivePaths().DeploymentID)
		require.Error(t, err, "the old primary cannot bind the adopted approval")
	})

	// A restored SQL database holds a witness below the KV authority. Capture refuses it, so C cannot bind it.
	t.Run("restored-sql-witness-before-capture", func(t *testing.T) {
		f := populatedAdoptionFixture(t)
		populatedRestoreWitness(t, f)
		ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
		defer cancel()
		_, err := CloseBackupBoundary(ctx, f.cfg)
		require.NoError(t, err)
		parent := filepath.Join(t.TempDir(), "private-capture")
		_, err = productfiles.CreateDirectory(parent)
		require.NoError(t, err)
		destination := filepath.Join(parent, "restored")
		_, err = CaptureBackup(ctx, f.cfg, recovery.CaptureRequest{Destination: destination, Build: "test", OperationID: "capture-restored", FencingEvidence: "operator-fenced-writers", KeyReference: "test-master-key"})
		require.ErrorContains(t, err, "captured fleet identity differs from the closed recovery boundary")
		// The bundle bytes precede the refusal. Verification refuses them with the same reason, so no preparation can open them.
		manifest, err := os.ReadFile(filepath.Join(destination, "backup-manifest.json"))
		require.NoError(t, err)
		_, err = VerifyBackup(ctx, f.cfg, recovery.VerifyRequest{Directory: destination, ManifestSHA256: canonicalRecordSHA256(manifest)})
		require.ErrorContains(t, err, "captured fleet identity differs from the closed recovery boundary")
		require.False(t, freshHistoryConstruction(t, f.cfg).Started)
	})

	// The SQL database is restored after capture. Preparation refuses the live witness below C and changes nothing.
	t.Run("restored-sql-witness-after-capture", func(t *testing.T) {
		binary := populatedBinary(t)
		f := populatedAdoptionFixture(t)
		f.capture(t)
		populatedRestoreWitness(t, f)
		before := f.census(t)
		output, err := populatedBinaryRun(t, binary, f, "restored-prepare", populatedArguments(populatedPrepare, f.requestFile, "", ""))
		require.Error(t, err)
		require.Empty(t, output)
		ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
		defer cancel()
		// The live witness no longer equals the closed boundary in C.
		_, err = PreparePopulatedRecovery(ctx, f.cfg, f.request)
		require.ErrorIs(t, err, recovery.ErrConflict)
		require.Equal(t, before, f.census(t))
		require.NoFileExists(t, filepath.Join(f.request.PreparationDirectory, populatedRecoveryPreparation))
		require.False(t, freshHistoryConstruction(t, f.cfg).Started)
	})
}

// populatedRestoreWitness replaces the SQL witness with a closed record one epoch below the prior approval.
func populatedRestoreWitness(t *testing.T, f *populatedFixture) {
	t.Helper()
	db, err := openBackupSQL(f.cfg)
	require.NoError(t, err)
	defer func() { require.NoError(t, db.Close()) }()
	_, err = db.ExecContext(t.Context(), db.Bind("UPDATE catalog_recovery SET epoch=?, gate_open=0, evidence=? WHERE deployment_id=?"), f.prior.Epoch-1, "restored-sql-database", f.prior.DeploymentID)
	require.NoError(t, err)
}
