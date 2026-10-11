package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/v2"
	"io"
	"io/fs"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/agentstation/starmap/pkg/productfiles"
	"github.com/agentstation/starport/internal/apikey"
	"github.com/agentstation/starport/internal/authorization"
	"github.com/agentstation/starport/internal/blob"
	"github.com/agentstation/starport/internal/cli"
	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/configrevision"
	"github.com/agentstation/starport/internal/diagnosis"
	"github.com/agentstation/starport/internal/files"
	"github.com/agentstation/starport/internal/identity"
	"github.com/agentstation/starport/internal/jobs"
	"github.com/agentstation/starport/internal/limits/reservation"
	"github.com/agentstation/starport/internal/limits/storedbytes"
	"github.com/agentstation/starport/internal/recovery"
	"github.com/agentstation/starport/internal/sqlstore"
	"github.com/agentstation/starport/internal/storage"
	"github.com/joho/godotenv"
	"github.com/stretchr/testify/require"
)

const (
	operatorCommandChild             = "STARPORT_RECOVERY_OPERATOR_COMMAND_CHILD"
	operatorDeploymentEnvironment    = "STARPORT_DEPLOYMENT_ID"
	operatorInstanceEnvironment      = "STARPORT_INSTANCE_ID"
	operatorMasterKeyEnvironment     = "STARPORT_SECURITY_MASTER_KEY"
	operatorProducerStateEnvironment = "STARMAP_STATE_DIR"
	operatorCatalogStateEnvironment  = "STARPORT_CATALOG_STATE_DIR"
	operatorJSONFlag                 = "--json"
	operatorRequestFlag              = "--request-file"
	operatorBackup                   = "backup"
	operatorProgram                  = "starport"
	operatorActivate                 = "activate"
	operatorStatus                   = "activation-status"
	operatorSeconds                  = "seconds"
)

type operatorCommandFixture struct {
	Environment map[string]string `json:"environment"`
	Arguments   []string          `json:"arguments"`
	Report      string            `json:"report"`
}

// operatorPrimaryConfiguration uses the primary-file loader without changing its result.
func operatorPrimaryConfiguration(t *testing.T, cfg *config.Config) (*config.Config, map[string]string) {
	t.Helper()
	paths := cfg.EffectivePaths()
	values := map[string]string{
		operatorDeploymentEnvironment: paths.DeploymentID, operatorInstanceEnvironment: paths.InstanceID,
		"STARPORT_DATA_DIR": paths.DataDir, "STARPORT_STATE_ROOT": paths.StateDir, "STARPORT_CACHE_DIR": paths.CacheDir,
		operatorMasterKeyEnvironment:             cfg.Security.MasterKey,
		"STARPORT_STORAGE_MODE":                  cfg.Storage.Mode,
		"STARPORT_STORAGE_BADGER_PATH":           cfg.Storage.Badger.Path,
		"STARPORT_STORAGE_BADGER_SYNC_WRITES":    strconv.FormatBool(cfg.Storage.Badger.SyncWrites),
		"STARPORT_STORAGE_SQL_MODE":              cfg.Storage.SQL.Mode,
		"STARPORT_STORAGE_SQL_SQLITE_PATH":       cfg.Storage.SQL.SQLite.Path,
		"STARPORT_STORAGE_SQL_POSTGRES_URL":      cfg.Storage.SQL.Postgres.URL,
		"STARPORT_STORAGE_SQL_MYSQL_DSN":         cfg.Storage.SQL.MySQL.DSN,
		"STARPORT_STORAGE_VALKEY_URL":            cfg.Storage.Valkey.URL,
		"STARPORT_STORAGE_VALKEY_ALLOW_INSECURE": strconv.FormatBool(cfg.Storage.Valkey.AllowInsecure),
		"STARPORT_FILES_BACKEND":                 cfg.Files.Backend, "STARPORT_FILES_PATH": cfg.Files.Path,
		"STARPORT_FILES_OBJECT_STORE_BUCKET":            cfg.Files.ObjectStore.Bucket,
		"STARPORT_FILES_OBJECT_STORE_REGION":            cfg.Files.ObjectStore.Region,
		"STARPORT_FILES_OBJECT_STORE_ENDPOINT":          cfg.Files.ObjectStore.Endpoint,
		"STARPORT_FILES_OBJECT_STORE_ACCESS_KEY_ID":     cfg.Files.ObjectStore.AccessKeyID,
		"STARPORT_FILES_OBJECT_STORE_SECRET_ACCESS_KEY": cfg.Files.ObjectStore.SecretAccessKey,
	}
	for key, value := range cfg.Catalog.CatalogValues() {
		name := "STARPORT_" + strings.TrimPrefix(key, "STARMAP_")
		if key == operatorProducerStateEnvironment {
			name = operatorCatalogStateEnvironment
		}
		values[name] = value
	}
	body, err := godotenv.Marshal(values)
	require.NoError(t, err)
	directory, err := productfiles.ExistingDirectory(paths.ConfigDir)
	require.NoError(t, err)
	require.NoError(t, directory.CompareAndPublish(t.Context(), filepath.Base(paths.ConfigFile), nil, []byte(body)))
	bootstrap := map[string]string{"STARPORT_CONFIG_DIR": paths.ConfigDir, "STARPORT_CONFIG_FILE": paths.ConfigFile}
	loaded, err := config.NewLoader().WithEnvironment(bootstrap).Load(t.Context())
	require.NoError(t, err)
	require.True(t, reflect.DeepEqual(cfg.RuntimeStorage(), loaded.RuntimeStorage()), "primary file changed native storage selections")
	require.True(t, cfg.Files == loaded.Files, "primary file changed file selections")
	require.True(t, maps.Equal(cfg.Catalog.CatalogValues(), loaded.Catalog.CatalogValues()), "primary file changed catalog selections")
	require.True(t, cfg.Security.MasterKey == loaded.Security.MasterKey, "primary file changed encryption selection")
	return loaded, bootstrap
}

func operatorCommandDependencies(t *testing.T, environment map[string]string, output, diagnostic io.Writer) cli.Dependencies {
	t.Helper()
	return cli.Dependencies{
		Stdin: bytes.NewReader(nil), Stdout: output, Stderr: diagnostic,
		LoadConfig: func(ctx context.Context) (*config.Config, error) {
			return config.NewLoader().WithEnvironment(environment).Load(ctx)
		},
		ResolvePaths: func() (config.Paths, error) {
			cfg, err := config.NewLoader().WithEnvironment(environment).Load(t.Context())
			if err != nil {
				return config.Paths{}, err
			}
			return cfg.EffectivePaths(), nil
		},
		WriteImportedHistory: WriteImportedHistory, ActivateRecovery: ActivateRecovery, InspectRecoveryActivation: InspectRecoveryActivation,
		PreparePopulatedRecovery: func(ctx context.Context, cfg *config.Config, request cli.PopulatedRecoveryRequest) (cli.PopulatedRecoveryPreparation, error) {
			result, err := PreparePopulatedRecovery(ctx, cfg, PopulatedRecoveryRequest(request))
			return cli.PopulatedRecoveryPreparation(result), err
		},
		ActivatePopulatedRecovery: func(ctx context.Context, cfg *config.Config, request cli.PopulatedRecoveryRequest) (recovery.ActivationResult, error) {
			return ActivatePopulatedRecovery(ctx, cfg, PopulatedRecoveryRequest(request))
		},
		InspectPopulatedRecovery: func(ctx context.Context, cfg *config.Config, request cli.PopulatedRecoveryRequest) (recovery.ActivationResult, error) {
			return InspectPopulatedRecovery(ctx, cfg, PopulatedRecoveryRequest(request))
		},
		RunServer: func(context.Context, cli.GatewayOptions, cli.ServerOutput) error {
			t.Fatal("recovery started a gateway")
			return nil
		},
		StartDevelopment: func(context.Context, cli.GatewayOptions) (cli.DevelopmentSession, error) {
			t.Fatal("recovery started development")
			return cli.DevelopmentSession{}, nil
		},
		Initialize: func(context.Context, cli.InitOptions) (cli.InitResult, error) {
			t.Fatal("recovery initialized a gateway")
			return cli.InitResult{}, nil
		},
		Diagnose: func(context.Context, diagnosis.Options) diagnosis.Report {
			t.Fatal("recovery ran diagnostics")
			return diagnosis.Report{}
		},
	}
}

func operatorPrivateJSON(t *testing.T, directory, name string, value any) string {
	t.Helper()
	private, err := productfiles.ExistingDirectory(directory)
	require.NoError(t, err)
	body, err := json.Marshal(value, json.Deterministic(true))
	require.NoError(t, err)
	require.NoError(t, private.CompareAndPublish(t.Context(), name, nil, body))
	return filepath.Join(directory, name)
}

func operatorCommandArguments(command, request, digest string) []string {
	arguments := []string{operatorProgram, operatorBackup, command, operatorRequestFlag, request, "--timeout", "3m", operatorJSONFlag}
	if digest != "" {
		arguments = append(arguments, "--decision-sha256", digest)
	}
	return arguments
}

func operatorRunCommand(t *testing.T, environment map[string]string, arguments []string) recovery.ActivationResult {
	t.Helper()
	var output, diagnostic bytes.Buffer
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	require.NoError(t, cli.Run(ctx, arguments, operatorCommandDependencies(t, environment, &output, &diagnostic)))
	var result recovery.ActivationResult
	require.NoError(t, json.Unmarshal(output.Bytes(), &result))
	return result
}

func operatorChildEnvironment(environment map[string]string) []string {
	retained := make([]string, 0, len(os.Environ())+len(environment))
	for _, item := range os.Environ() {
		if !strings.HasPrefix(item, "STARPORT_") && !strings.HasPrefix(item, "STARMAP_") {
			retained = append(retained, item)
		}
	}
	for key, value := range environment {
		retained = append(retained, key+"="+value)
	}
	return retained
}

func operatorRunFreshCommand(t *testing.T, environment map[string]string, arguments []string) recovery.ActivationResult {
	t.Helper()
	directory := filepath.Join(t.TempDir(), "private-child")
	_, err := productfiles.CreateDirectory(directory)
	require.NoError(t, err)
	report := filepath.Join(directory, "result.json")
	fixture := operatorPrivateJSON(t, directory, "input.json", operatorCommandFixture{Environment: environment, Arguments: arguments, Report: report})
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestRecoveryPopulatedOperatorCommandsAcrossNativePhaseCut$", "-test.v") // #nosec G204 G702 -- This starts the current test executable.
	command.Env = append(operatorChildEnvironment(environment), operatorCommandChild+"="+fixture)
	output, err := command.CombinedOutput()
	private, fileErr := productfiles.ExistingDirectory(directory)
	require.NoError(t, fileErr)
	require.NoError(t, private.CompareAndPublish(t.Context(), "process.log", nil, output))
	// Retain process output privately instead of printing credentials from a failed child.
	require.NoError(t, err, "fresh operator process failed; private output retained")
	body, err := private.ReadFile(filepath.Base(report), 64<<10)
	require.NoError(t, err)
	var result recovery.ActivationResult
	require.NoError(t, json.Unmarshal(body, &result))
	return result
}

func operatorEvidenceCensus(t *testing.T, roots ...string) map[string]string {
	t.Helper()
	result := make(map[string]string)
	for _, root := range roots {
		info, err := os.Stat(root)
		require.NoError(t, err)
		directory := root
		if !info.IsDir() {
			directory = filepath.Dir(root)
		}
		scope, err := os.OpenRoot(directory)
		require.NoError(t, err)
		require.NoError(t, filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
			if err != nil || entry.IsDir() {
				return err
			}
			if !entry.Type().IsRegular() {
				return fs.ErrInvalid
			}
			relative, err := filepath.Rel(directory, path)
			if err != nil {
				return err
			}
			file, err := scope.Open(relative)
			if err != nil {
				return err
			}
			hash := sha256.New()
			_, copyErr := io.Copy(hash, file)
			closeErr := file.Close()
			if copyErr != nil {
				return copyErr
			}
			result[path] = hex.EncodeToString(hash.Sum(nil))
			return closeErr
		}))
		require.NoError(t, scope.Close())
	}
	return result
}

func operatorCapturedFile(t *testing.T, cfg *config.Config, payload string) files.File {
	t.Helper()
	store, err := storage.Open(cfg.RuntimeStorage())
	require.NoError(t, err)
	defer func() { require.NoError(t, store.Close()) }()
	repository, err := files.OpenRepository(store)
	require.NoError(t, err)
	blobs, err := blob.NewFilesystem(cfg.Files.Path)
	require.NoError(t, err)
	meter, err := storedbytes.NewStorageMeter(store)
	require.NoError(t, err)
	service, err := files.NewService(repository, blobs, files.WithMeter(meter))
	require.NoError(t, err)
	file, err := service.Upload(t.Context(), files.UploadRequest{Account: postBackupAccount, Filename: "recovered-user-data.txt", Purpose: files.PurposeUserData, Size: int64(len(payload))}, strings.NewReader(payload))
	require.NoError(t, err)
	return file
}

func operatorRecoveredActivity(t *testing.T, application *App, activity *postBackupActivity) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	keys, err := apikey.Open(application.store)
	require.NoError(t, err)
	_, err = keys.GetByID(ctx, activity.key.ID)
	require.ErrorIs(t, err, apikey.ErrNotFound)
	_, err = keys.GetByHash(ctx, activity.key.Hash)
	require.ErrorIs(t, err, apikey.ErrNotFound)
	db, err := openBackupSQL(application.config)
	require.NoError(t, err)
	defer func() { require.NoError(t, db.Close()) }()
	people, err := identity.Open(db)
	require.NoError(t, err)
	reachable, err := people.AccountGrants.ReachableAccounts(ctx, activity.grant.UserID)
	require.NoError(t, err)
	require.Empty(t, reachable)
	// The actual shared budget owner uses SQL authority time instead of a Valkey clock.
	require.NotNil(t, application.budget.shared)
	require.NoError(t, application.budget.shared.Check(ctx))
	ledger := application.budget.ledger
	window, err := ledger.Window(ctx, activity.meter, activity.through)
	require.NoError(t, err)
	require.EqualValues(t, 700, window.Consumed)
	require.EqualValues(t, 200, window.Reserved)
	for _, expected := range activity.attempts {
		actual, err := ledger.Inspect(ctx, expected.Attempt.ID)
		require.NoError(t, err)
		require.Equal(t, expected, *actual)
	}
	require.ErrorIs(t, ledger.Begin(ctx, "post-backup-uncertain"), reservation.ErrAlreadyDispatched)
	next := activity.prototype
	next.ID, next.RequestID, next.Bound = "after-recovery-exhausted", "after-recovery-request", reservation.Quantities{operatorSeconds: 101}
	_, err = ledger.Reserve(ctx, next)
	require.ErrorIs(t, err, reservation.ErrExhausted)
	var census reservation.RecoveryResult
	for !census.Complete {
		pass, err := application.budget.recovery.Pass(ctx, 1000)
		require.NoError(t, err)
		census.Scanned += pass.Scanned
		census.Held += pass.Held
		census.Recovered += pass.Recovered
		census.Failed += pass.Failed
		census.Complete = pass.Complete
	}
	require.True(t, census.Complete)
	require.Equal(t, 1, census.Held)
	require.Zero(t, census.Recovered)
	require.Zero(t, census.Failed)
	_, err = application.jobs.Sweep(ctx)
	require.NoError(t, err)
	records, err := jobs.OpenRepository(application.store)
	require.NoError(t, err)
	job, err := records.Get(ctx, postBackupAccount, "post-backup-job")
	require.NoError(t, err)
	require.True(t, job.SubmissionPending)
	_, err = application.jobs.Refresh(ctx, activity.runner, postBackupAccount, job.ID)
	require.ErrorIs(t, err, jobs.ErrSubmissionUnconfirmed)
	_, err = application.jobs.Cancel(ctx, activity.runner, postBackupAccount, job.ID)
	require.ErrorIs(t, err, jobs.ErrSubmissionUnconfirmed)
	require.Equal(t, 1, activity.runner.submits)
	require.Zero(t, activity.runner.polls)
	require.Zero(t, activity.runner.cancels)
	require.Zero(t, activity.runner.fetches)
}

func TestRecoveryPopulatedOperatorCommandsAcrossNativePhaseCut(t *testing.T) {
	if path := os.Getenv(operatorCommandChild); path != "" {
		private, err := productfiles.ExistingDirectory(filepath.Dir(path))
		require.NoError(t, err)
		body, err := private.ReadFile(filepath.Base(path), 64<<10)
		require.NoError(t, err)
		var fixture operatorCommandFixture
		require.NoError(t, json.Unmarshal(body, &fixture))
		result := operatorRunCommand(t, fixture.Environment, fixture.Arguments)
		operatorPrivateJSON(t, filepath.Dir(fixture.Report), filepath.Base(fixture.Report), result)
		return
	}
	valkey, postgres, endpoint := os.Getenv("TEST_VALKEY_URL"), os.Getenv("TEST_POSTGRES_URL"), os.Getenv("TEST_BLOB_S3_ENDPOINT")
	if valkey == "" || postgres == "" || endpoint == "" {
		t.Skip("UNVERIFIED: populated operator commands need native shared recovery owners")
	}
	var activity postBackupActivity
	var captured files.File
	const payload = "original user data retained across recovery"
	cfg, prepare := boundedActivationSourceFixtureWithActivity(t, false, func(source *config.Config) {
		activity.beforeCapture(t, source)
		captured = operatorCapturedFile(t, source, payload)
	}, func(source *config.Config, receipt recovery.CaptureResult) { activity.afterCapture(t, source, receipt) })
	configureSharedRestore(t, cfg, valkey, postgres, endpoint)
	cfg, environment := operatorPrimaryConfiguration(t, cfg)
	// The operator writes the final-only H through the shipped verb. The post-backup ledger then extends it.
	var written, writtenDiagnostic bytes.Buffer
	cfg, request := activationPreparedFixtureWith(t, cfg, prepare, activationHistoryCommand(t, operatorCommandDependencies(t, environment, &written, &writtenDiagnostic), &written))
	request = postBackupHistory(t, cfg, request, &activity)
	private := filepath.Join(t.TempDir(), "private-operator")
	_, err := productfiles.CreateDirectory(private)
	require.NoError(t, err)
	requestFile := operatorPrivateJSON(t, private, "activation.json", request)
	originalInputs := operatorEvidenceCensus(t, private, cfg.EffectivePaths().ConfigFile, cfg.EffectivePaths().LocalTokenFile)
	invalid := request
	invalid.History.Attestation.CompleteInterval = false
	invalidFile := operatorPrivateJSON(t, private, "incomplete-interval.json", invalid)
	var invalidOutput, invalidDiagnostic bytes.Buffer
	invalidDeps := operatorCommandDependencies(t, environment, &invalidOutput, &invalidDiagnostic)
	load := invalidDeps.LoadConfig
	loads := 0
	invalidDeps.LoadConfig = func(ctx context.Context) (*config.Config, error) {
		loads++
		return load(ctx)
	}
	require.Error(t, cli.Run(t.Context(), operatorCommandArguments(operatorActivate, invalidFile, ""), invalidDeps))
	require.Zero(t, loads)
	require.Empty(t, invalidOutput.Bytes())

	// Missing original evidence must not release any target or publish a decision.
	missing := filepath.Join(request.History.HistoryDirectory, "payloads", "000001.json")
	require.NoError(t, os.Rename(missing, missing+".retained"))
	var output, diagnostic bytes.Buffer
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	err = cli.Run(ctx, operatorCommandArguments(operatorActivate, requestFile, ""), operatorCommandDependencies(t, environment, &output, &diagnostic))
	require.Error(t, err)
	require.Equal(t, cli.ExitCodeRuntime, cli.ExitCode(err))
	require.Empty(t, output.Bytes())
	require.NoFileExists(t, filepath.Join(request.ActivationDirectory, "decision.json"))
	native, err := openRecoveryActivationNative(ctx, cfg, request)
	require.NoError(t, err)
	require.ErrorIs(t, storage.CheckImportBarrier(ctx, native.store), storage.ErrImportRestricted)
	require.ErrorIs(t, native.db.CheckImportBarrier(ctx), sqlstore.ErrImportRestricted)
	require.NoError(t, native.close())
	require.NoError(t, os.Rename(missing+".retained", missing))

	sealed, native := sealActivationFixture(t, cfg, request)
	digest := sealed.journal.Digest()
	// The blob owner commits. Its reply is lost before the application phase record.
	commitActivationNativePhases(t, cfg, sealed, native, 1)
	require.NoError(t, native.close())
	before := operatorEvidenceCensus(t, request.ActivationDirectory, request.History.JournalDirectory, request.History.HistoryDirectory)
	partial := operatorRunCommand(t, environment, operatorCommandArguments(operatorStatus, requestFile, digest))
	require.Equal(t, 1, partial.CompletedPhases)
	require.False(t, partial.HistoricallyComplete)
	require.False(t, partial.CurrentAdmissionValid)
	require.True(t, partial.Restricted)
	require.Equal(t, before, operatorEvidenceCensus(t, request.ActivationDirectory, request.History.JournalDirectory, request.History.HistoryDirectory))
	native, err = openRecoveryActivationNative(ctx, cfg, request)
	require.NoError(t, err)
	boundary, err := native.witness.Current(ctx, cfg.EffectivePaths().DeploymentID)
	require.NoError(t, err)
	require.Equal(t, sealed.facts.Boundary, boundary)
	require.ErrorIs(t, storage.CheckImportBarrier(ctx, native.store), storage.ErrImportRestricted)
	require.ErrorIs(t, native.db.CheckImportBarrier(ctx), sqlstore.ErrImportRestricted)
	require.NoError(t, native.close())
	completed := operatorRunFreshCommand(t, environment, operatorCommandArguments(operatorActivate, requestFile, digest))
	require.Equal(t, digest, completed.DecisionSHA256)
	require.Equal(t, 3, completed.CompletedPhases)
	require.True(t, completed.HistoricallyComplete)
	require.True(t, completed.CurrentAdmissionValid)
	require.False(t, completed.Restricted)
	require.Equal(t, "start a fresh gateway and verify readiness", completed.NextAction)
	before = operatorEvidenceCensus(t, request.ActivationDirectory, request.History.JournalDirectory, request.History.HistoryDirectory)
	require.Equal(t, completed, operatorRunCommand(t, environment, operatorCommandArguments(operatorStatus, requestFile, digest)))
	require.Equal(t, completed, operatorRunCommand(t, environment, operatorCommandArguments(operatorActivate, requestFile, digest)))
	require.Equal(t, before, operatorEvidenceCensus(t, request.ActivationDirectory, request.History.JournalDirectory, request.History.HistoryDirectory))
	native, err = openRecoveryActivationNative(ctx, cfg, request)
	require.NoError(t, err)
	approved, err := native.witness.Approved(ctx, cfg.EffectivePaths().DeploymentID)
	require.NoError(t, err)
	require.NoError(t, native.close())

	t.Run("shipping-binary", func(t *testing.T) {
		binary := os.Getenv("STARPORT_RECOVERY_OPERATOR_BINARY")
		if binary == "" {
			t.Skip("UNVERIFIED: supply the exact source-built Starport executable")
		}
		for _, name := range []string{operatorStatus, operatorActivate} {
			command := exec.CommandContext(ctx, binary, operatorCommandArguments(name, requestFile, digest)[1:]...) // #nosec G204 G702 -- The test harness selects the source-built executable.
			command.Env = operatorChildEnvironment(environment)
			command.Dir = cfg.EffectivePaths().ConfigDir
			var stdout, stderr bytes.Buffer
			command.Stdout, command.Stderr = &stdout, &stderr
			err := command.Run()
			operatorPrivateJSON(t, private, "binary-"+name+"-output.json", map[string][]byte{"stdout": stdout.Bytes(), "stderr": stderr.Bytes()})
			require.NoError(t, err, "shipping operator command failed; private output retained")
			var actual recovery.ActivationResult
			require.NoError(t, json.Unmarshal(stdout.Bytes(), &actual))
			require.Equal(t, completed, actual)
		}
		require.Equal(t, before, operatorEvidenceCensus(t, request.ActivationDirectory, request.History.JournalDirectory, request.History.HistoryDirectory))
		native, err := openRecoveryActivationNative(ctx, cfg, request)
		require.NoError(t, err)
		current, err := native.witness.Approved(ctx, cfg.EffectivePaths().DeploymentID)
		require.NoError(t, err)
		require.Equal(t, approved, current)
		require.NoError(t, native.close())
	})

	// Distributed storage selects shared management. The documented upgrade
	// step writes revision 1 before the first gateway start.
	require.True(t, cfg.SharedManagement())
	initialized, err := InitializeSharedConfiguration(ctx, cfg, configrevision.Request{OperationID: "recovery-operator-shared-configuration"})
	require.NoError(t, err)
	require.Equal(t, int64(1), initialized.Revision.Sequence)

	// Command completion is separate from actual fresh gateway admission readiness.
	application, err := New(cfg)
	require.NoError(t, err)
	defer func() {
		if application != nil {
			closeCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			require.NoError(t, application.Close(closeCtx))
		}
	}()
	require.True(t, application.admissionReady())
	operatorRecoveredActivity(t, application, &activity)
	_, err = application.authorization.cache.Resolve(ctx, authorization.Identity{Subject: activity.survivor.Hash})
	require.NoError(t, err)
	_, err = application.authorization.cache.Resolve(ctx, authorization.Identity{Subject: activity.key.Hash})
	require.Error(t, err)
	_, err = application.authorization.cache.Resolve(ctx, authorization.Identity{Tenant: postBackupAccount, Subject: authorization.SessionSubjectPrefix + "post-backup-subject"})
	require.ErrorIs(t, err, authorization.ErrDenied)
	actual, reader, err := application.files.Open(ctx, postBackupAccount, captured.ID)
	require.NoError(t, err)
	require.Equal(t, captured, actual)
	contents, err := io.ReadAll(reader)
	require.NoError(t, err)
	require.NoError(t, reader.Close())
	require.Equal(t, payload, string(contents))
	require.NoError(t, application.Close(ctx))
	application = nil

	request.ExpectedDecisionSHA256 = digest
	native, err = openRecoveryActivationNative(ctx, cfg, request)
	require.NoError(t, err)
	current, err := native.witness.Current(ctx, cfg.EffectivePaths().DeploymentID)
	require.NoError(t, err)
	withdrawn, err := native.witness.Close(ctx, current)
	require.NoError(t, err)
	require.NoError(t, native.close())
	status := operatorRunCommand(t, environment, operatorCommandArguments(operatorStatus, requestFile, digest))
	require.True(t, status.HistoricallyComplete)
	require.False(t, status.CurrentAdmissionValid)
	require.True(t, status.Restricted)
	require.Equal(t, status, operatorRunCommand(t, environment, operatorCommandArguments(operatorActivate, requestFile, digest)))
	native, err = openRecoveryActivationNative(ctx, cfg, request)
	require.NoError(t, err)
	current, err = native.witness.Current(ctx, cfg.EffectivePaths().DeploymentID)
	require.NoError(t, err)
	require.Equal(t, withdrawn, current)
	require.NoError(t, native.close())
	for path, digest := range originalInputs {
		require.Equal(t, digest, operatorEvidenceCensus(t, path)[path])
	}
}
