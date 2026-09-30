package app

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/agentstation/starmap/pkg/productfiles"
	"github.com/agentstation/starport/internal/account"
	"github.com/agentstation/starport/internal/apikey"
	"github.com/agentstation/starport/internal/authorization"
	"github.com/agentstation/starport/internal/catalog"
	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/identity"
	"github.com/agentstation/starport/internal/jobs"
	"github.com/agentstation/starport/internal/limits"
	"github.com/agentstation/starport/internal/limits/reservation"
	"github.com/agentstation/starport/internal/recovery"
	"github.com/agentstation/starport/internal/routing"
	"github.com/agentstation/starport/internal/sqlstore"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

const postBackupAccount = "post-backup-tenant"

// postBackupActivity retains actual source-owner results outside the captured backup.
type postBackupActivity struct {
	key       apikey.APIKey
	survivor  apikey.APIKey
	grant     identity.AccountGrant
	meter     reservation.Meter
	prototype reservation.Attempt
	attempts  []reservation.Record
	job       jobs.RecoveryJob
	indexes   apikey.RecoveryIndexes
	boundary  recovery.Record
	through   time.Time
	runner    *postBackupLostReplyRunner
}

func postBackupSnapshot(t *testing.T, store storage.KVStore) *recovery.KVSnapshotView {
	t.Helper()
	source, err := storage.OpenRecordTransfer(t.Context(), store, "")
	require.NoError(t, err)
	root := filepath.Join(t.TempDir(), "private")
	_, err = productfiles.CreateDirectory(root)
	require.NoError(t, err)
	directory := filepath.Join(root, "snapshot")
	receipt, err := recovery.SnapshotKV(t.Context(), source, directory)
	require.NoError(t, err)
	view, err := recovery.OpenKVSnapshot(t.Context(), recovery.KVSnapshotPath(directory), root, receipt)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, view.Close()) })
	return view
}

func (a *postBackupActivity) beforeCapture(t *testing.T, cfg *config.Config) {
	t.Helper()
	store, err := storage.Open(cfg.RuntimeStorage())
	require.NoError(t, err)
	defer func() { require.NoError(t, store.Close()) }()
	generations, err := catalog.NewGenerationStore(store)
	require.NoError(t, err)
	generation, err := generations.Current(t.Context())
	require.NoError(t, err)
	accounts, err := account.Open(store)
	require.NoError(t, err)
	created, err := accounts.Create(t.Context(), account.Account{ID: postBackupAccount, Name: "Recovery tenant", Active: true, CredentialStrategy: account.StrategyOperatorFirst, Limits: &limits.Limits{Spend: &limits.Budget{Limit: 1000, Interval: limits.IntervalDay}}})
	require.NoError(t, err)
	keys, err := apikey.Open(store)
	require.NoError(t, err)
	issuer, err := apikey.NewIssuer(keys, apikey.WithAccountChecker(accounts))
	require.NoError(t, err)
	withdrawn, err := issuer.IssueInitial(t.Context(), apikey.IssueRequest{Name: "withdrawn", AccountID: postBackupAccount, Scopes: []string{"inference:write"}})
	require.NoError(t, err)
	a.key = withdrawn.APIKey
	survivor, err := issuer.Issue(t.Context(), apikey.IssueRequest{Name: "survivor", AccountID: postBackupAccount, Scopes: []string{"inference:write"}})
	require.NoError(t, err)
	a.survivor = survivor.APIKey
	ledger, err := reservation.Open(store.(storage.TimeBoundStore))
	require.NoError(t, err)
	a.meter = reservation.Meter{Scope: limits.ScopeAccount, Holder: postBackupAccount, Dimension: limits.DimensionSpend, Interval: limits.IntervalDay}
	a.prototype = reservation.Attempt{ID: "pre-backup-unused", RequestID: "pre-backup-request", AccountID: postBackupAccount, KeyID: a.key.ID, OfferingID: "provider/model", CatalogGeneration: generation.Manifest.GenerationID, Operation: string(routing.OperationVideosGenerations), Rules: []reservation.Rule{{Meter: a.meter, Limit: 1000, PolicyRevision: fmt.Sprintf("account-%d", created.Revision), HistoryID: created.Account.Limits.Spend.HistoryID}}, Valuation: reservation.Valuation{Version: reservation.ArithmeticVersion, Components: []reservation.Component{{Unit: "seconds", Price: reservation.Price{USD: "0.000000001", PerUnits: 1}}}}, Bound: reservation.Quantities{"seconds": 1}}
	_, err = ledger.Reserve(t.Context(), a.prototype)
	require.NoError(t, err)
	require.NoError(t, ledger.CancelBeforeDispatch(t.Context(), a.prototype.ID))
	db, err := sqlstore.Open(cfg.Storage.RuntimeSQL())
	require.NoError(t, err)
	defer func() { require.NoError(t, db.Close()) }()
	people, err := identity.Open(db)
	require.NoError(t, err)
	_, err = people.Users.Create(t.Context(), identity.User{ID: "post-backup-person", Subject: "post-backup-subject"})
	require.NoError(t, err)
	a.grant, err = people.AccountGrants.Add(t.Context(), identity.AccountGrant{AccountID: postBackupAccount, UserID: "post-backup-person"})
	require.NoError(t, err)
}

func (a *postBackupActivity) afterCapture(t *testing.T, cfg *config.Config, captured recovery.CaptureResult) {
	t.Helper()
	store, err := storage.Open(cfg.RuntimeStorage())
	require.NoError(t, err)
	defer func() { require.NoError(t, store.Close()) }()
	db, err := sqlstore.Open(cfg.Storage.RuntimeSQL())
	require.NoError(t, err)
	defer func() { require.NoError(t, db.Close()) }()
	witness, err := recovery.New(db)
	require.NoError(t, err)
	original, err := witness.Current(t.Context(), cfg.EffectivePaths().DeploymentID)
	require.NoError(t, err)
	require.False(t, original.Open)
	_, err = witness.Approve(t.Context(), original, "controlled-live-source", "post-backup-source-resumed")
	require.NoError(t, err)
	ledger, err := reservation.Open(store.(storage.TimeBoundStore))
	require.NoError(t, err)
	spent := a.prototype
	spent.ID, spent.RequestID, spent.Bound = "post-backup-spent", "post-backup-spend-request", reservation.Quantities{"seconds": 700}
	_, err = ledger.Reserve(t.Context(), spent)
	require.NoError(t, err)
	require.NoError(t, ledger.Begin(t.Context(), spent.ID))
	require.NoError(t, ledger.Reconcile(t.Context(), spent.ID, reservation.Evidence{ID: "independent-provider-usage", Quantities: reservation.Quantities{"seconds": 700}}))
	uncertain := a.prototype
	uncertain.ID, uncertain.RequestID, uncertain.Bound = "post-backup-uncertain", "post-backup-uncertain-request", reservation.Quantities{"seconds": 200}
	_, err = ledger.Reserve(t.Context(), uncertain)
	require.NoError(t, err)
	require.NoError(t, ledger.Begin(t.Context(), uncertain.ID))
	records, err := jobs.OpenRepository(store)
	require.NoError(t, err)
	service, err := jobs.NewService(records, jobs.WithRequiredSettlement(&budgetOwner{ledger: ledger}), jobs.WithIdentifiers(func() string { return "post-backup-job" }))
	require.NoError(t, err)
	a.runner = &postBackupLostReplyRunner{attempt: uncertain}
	_, err = service.Submit(t.Context(), func(context.Context) (jobs.Runner, error) { return a.runner, nil }, jobs.Submission{Account: postBackupAccount, KeyID: a.key.ID, Operation: routing.OperationVideosGenerations})
	require.ErrorIs(t, err, jobs.ErrSubmissionUnconfirmed)
	require.Equal(t, 1, a.runner.submits)
	require.NoError(t, ledger.MarkUncertain(t.Context(), uncertain.ID, "provider-response-lost"))
	keys, err := apikey.Open(store)
	require.NoError(t, err)
	key, err := keys.GetByID(t.Context(), a.key.ID)
	require.NoError(t, err)
	require.NoError(t, keys.Delete(t.Context(), a.key.ID, key.Revision))
	people, err := identity.Open(db)
	require.NoError(t, err)
	require.NoError(t, people.AccountGrants.Remove(t.Context(), a.grant))
	current, err := witness.Current(t.Context(), cfg.EffectivePaths().DeploymentID)
	require.NoError(t, err)
	a.boundary, err = witness.Close(t.Context(), current)
	require.NoError(t, err)
	require.False(t, a.boundary.Open)
	view := postBackupSnapshot(t, store)
	for _, id := range []string{spent.ID, uncertain.ID} {
		retained, err := reservation.ReadBackupAttempt(t.Context(), view, id)
		require.NoError(t, err)
		a.attempts = append(a.attempts, *retained)
	}
	a.job, err = jobs.CaptureRecoveryJob(t.Context(), view, postBackupAccount, "post-backup-job")
	require.NoError(t, err)
	a.indexes, _, err = apikey.CaptureRecoveryIndexes(t.Context(), view)
	require.NoError(t, err)
	a.through = time.Now().UTC()
	require.Greater(t, a.boundary.Epoch, captured.RecoveryEpoch)
}

type postBackupLostReplyRunner struct {
	attempt                          reservation.Attempt
	submits, polls, cancels, fetches int
}

func (r *postBackupLostReplyRunner) Submit(ctx context.Context, recorder jobs.SubmissionRecorder) (jobs.Acceptance, error) {
	if err := recorder.BeforeDispatch(ctx, jobs.Dispatch{Provider: "provider", Model: r.attempt.OfferingID, CatalogGeneration: r.attempt.CatalogGeneration, ReservationID: r.attempt.ID, Valuation: &r.attempt.Valuation}); err != nil {
		return jobs.Acceptance{}, err
	}
	r.submits++
	return jobs.Acceptance{}, errors.New("provider response lost after dispatch")
}
func (r *postBackupLostReplyRunner) Poll(context.Context, jobs.Handle) (jobs.Report, error) {
	r.polls++
	return jobs.Report{}, errors.New("unexpected provider poll")
}
func (r *postBackupLostReplyRunner) Cancel(context.Context, jobs.Handle) (jobs.Report, error) {
	r.cancels++
	return jobs.Report{}, errors.New("unexpected provider cancellation")
}
func (r *postBackupLostReplyRunner) Fetch(context.Context, jobs.Handle, int64) (jobs.Asset, error) {
	r.fetches++
	return jobs.Asset{}, errors.New("unexpected provider fetch")
}

func postBackupHistory(t *testing.T, cfg *config.Config, request RecoveryActivationRequest, a *postBackupActivity) RecoveryActivationRequest {
	t.Helper()
	encryption, err := backupEncryption(cfg)
	require.NoError(t, err)
	source, err := recovery.InspectRestoreSource(t.Context(), request.Prepare.VerifyRequest, encryption, catalog.InspectCapturedCatalog)
	require.NoError(t, err)
	before, err := source.OpenCapturedKV(t.Context())
	require.NoError(t, err)
	defer func() { require.NoError(t, before.Close()) }()
	original, err := apikey.CaptureRecoveryRecord(t.Context(), before, a.key.ID)
	require.NoError(t, err)
	_, expectedIndexes, err := apikey.CaptureRecoveryIndexes(t.Context(), before)
	require.NoError(t, err)
	withdrawal, err := identity.NewRecoveryTransition([]identity.RecoveryIdentityEvent{{Grant: &identity.RecoveryGrantChange{Before: &a.grant}}})
	require.NoError(t, err)
	policy := map[string]any{"version": 1, "api_keys": apikey.RecoveryReplay{ExpectedIndexesSHA256: expectedIndexes, Indexes: a.indexes, Changes: []apikey.RecoveryChange{{ID: a.key.ID, ExpectedSHA256: original.SHA256(), Next: nil}}}, "attempts": a.attempts, "videos": []map[string]any{{"final": a.job, "publish": true}}}
	retained, err := json.Marshal(struct {
		Boundary   recovery.Record
		Through    time.Time
		Policy     any
		Withdrawal identity.RecoveryTransition
	}{a.boundary, a.through, policy, withdrawal}, json.Deterministic(true))
	require.NoError(t, err)
	ledgerPath := filepath.Join(filepath.Dir(request.ActivationDirectory), "independent-post-backup.json")
	directory, err := productfiles.ExistingDirectory(filepath.Dir(ledgerPath))
	require.NoError(t, err)
	require.NoError(t, directory.CompareAndPublish(t.Context(), filepath.Base(ledgerPath), nil, retained))
	finalPayloads := make([]jsontext.Value, 2)
	for i := range finalPayloads {
		body, err := os.ReadFile(filepath.Join(request.History.HistoryDirectory, fmt.Sprintf("payloads/%06d.json", i+1)))
		require.NoError(t, err)
		finalPayloads[i] = body
	}
	payloads := []any{policy, withdrawal, finalPayloads[0], finalPayloads[1]}
	kinds := []string{"kv_domain", "sql_identity", "kv_authorization_final", "sql_authorization_final"}
	steps := make([]map[string]any, 0, len(payloads))
	for i, value := range payloads {
		body, err := json.Marshal(value, json.Deterministic(true))
		require.NoError(t, err)
		path := fmt.Sprintf("payloads/%06d.json", i+1)
		require.NoError(t, os.WriteFile(filepath.Join(request.History.HistoryDirectory, path), body, 0600))
		steps = append(steps, map[string]any{"ordinal": i + 1, "kind": kinds[i], "path": path, "size": len(body), "sha256": canonicalRecordSHA256(body), "evidence_source_ids": []string{"post-backup-source"}})
	}
	body, err := os.ReadFile(filepath.Join(request.History.HistoryDirectory, "history.json"))
	require.NoError(t, err)
	var manifest map[string]any
	require.NoError(t, json.Unmarshal(body, &manifest))
	evidenceDigest := canonicalRecordSHA256(retained)
	manifest["mode"] = "disaster_recovery"
	manifest["interval"] = map[string]any{"through_utc": a.through, "end_reference": "post-backup-source-fenced"}
	manifest["highest_epoch"] = recovery.EpochEvidence{HighestEpoch: a.boundary.Epoch, SourceSHA256: evidenceDigest, Reference: ledgerPath, Operator: "source-operator"}
	manifest["evidence_sources"] = []map[string]any{{"id": "post-backup-source", "sha256": evidenceDigest, "size": len(retained), "reference": ledgerPath}}
	manifest["steps"] = steps
	body, err = json.Marshal(manifest, json.Deterministic(true))
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(request.History.HistoryDirectory, "history.json"), body, 0600))
	request.History.HistorySHA256 = canonicalRecordSHA256(body)
	request.History.Attestation.Reference = ledgerPath
	return request
}

func TestRecoveryActivationReplaysActualPostBackupRevocationSpendAndUncertainDispatch(t *testing.T) {
	var activity postBackupActivity
	cfg, prepare := boundedActivationSourceFixtureWithActivity(t, false, func(cfg *config.Config) { activity.beforeCapture(t, cfg) }, func(cfg *config.Config, receipt recovery.CaptureResult) { activity.afterCapture(t, cfg, receipt) })
	cfg, request := activationPreparedFixture(t, cfg, prepare)
	request = postBackupHistory(t, cfg, request, &activity)
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	incomplete := request
	incomplete.History.Attestation.CompleteInterval = false
	_, err := ActivateRecovery(ctx, cfg, incomplete)
	require.Error(t, err)
	native, err := openRecoveryActivationNative(ctx, cfg, request)
	require.NoError(t, err)
	require.ErrorIs(t, storage.CheckImportBarrier(ctx, native.store), storage.ErrImportRestricted)
	require.ErrorIs(t, native.db.CheckImportBarrier(ctx), sqlstore.ErrImportRestricted)
	require.NoError(t, native.close())
	result, err := ActivateRecovery(ctx, cfg, request)
	require.NoError(t, err)
	require.True(t, result.HistoricallyComplete)
	require.True(t, result.CurrentAdmissionValid)
	require.Equal(t, 3, result.CompletedPhases)
	request.ExpectedDecisionSHA256 = result.DecisionSHA256
	postBackupRecoveredAssertions(t, cfg, &activity)
	again, err := ActivateRecovery(ctx, cfg, request)
	require.NoError(t, err)
	require.Equal(t, result.DecisionSHA256, again.DecisionSHA256)
	postBackupRecoveredAssertions(t, cfg, &activity)
	application, err := New(cfg)
	require.NoError(t, err)
	defer func() { require.NoError(t, application.Close(ctx)) }()
	_, err = application.authorization.cache.Resolve(ctx, authorization.Identity{Subject: activity.survivor.Hash})
	require.NoError(t, err)
	_, err = application.authorization.cache.Resolve(ctx, authorization.Identity{Subject: activity.key.Hash})
	require.Error(t, err)
	_, err = application.authorization.cache.Resolve(ctx, authorization.Identity{Tenant: postBackupAccount, Subject: authorization.SessionSubjectPrefix + "post-backup-subject"})
	require.ErrorIs(t, err, authorization.ErrDenied)
}

func postBackupRecoveredAssertions(t *testing.T, cfg *config.Config, a *postBackupActivity) {
	t.Helper()
	store, err := storage.Open(cfg.RuntimeStorage())
	require.NoError(t, err)
	defer func() { require.NoError(t, store.Close()) }()
	keys, err := apikey.Open(store)
	require.NoError(t, err)
	_, err = keys.GetByID(t.Context(), a.key.ID)
	require.ErrorIs(t, err, apikey.ErrNotFound)
	_, err = keys.GetByHash(t.Context(), a.key.Hash)
	require.ErrorIs(t, err, apikey.ErrNotFound)
	db, err := sqlstore.Open(cfg.Storage.RuntimeSQL())
	require.NoError(t, err)
	defer func() { require.NoError(t, db.Close()) }()
	people, err := identity.Open(db)
	require.NoError(t, err)
	reachable, err := people.AccountGrants.ReachableAccounts(t.Context(), a.grant.UserID)
	require.NoError(t, err)
	require.Empty(t, reachable)
	ledger, err := reservation.Open(store.(storage.TimeBoundStore))
	require.NoError(t, err)
	window, err := ledger.Window(t.Context(), a.meter, a.through)
	require.NoError(t, err)
	require.EqualValues(t, 700, window.Consumed)
	require.EqualValues(t, 200, window.Reserved)
	for _, expected := range a.attempts {
		actual, err := ledger.Inspect(t.Context(), expected.Attempt.ID)
		require.NoError(t, err)
		require.Equal(t, expected, *actual)
	}
	require.ErrorIs(t, ledger.Begin(t.Context(), "post-backup-uncertain"), reservation.ErrAlreadyDispatched)
	next := a.prototype
	next.ID, next.RequestID, next.Bound = "after-recovery-exhausted", "after-recovery-request", reservation.Quantities{"seconds": 101}
	_, err = ledger.Reserve(t.Context(), next)
	require.ErrorIs(t, err, reservation.ErrExhausted)
	worker, err := reservation.NewRecovery(ledger, store)
	require.NoError(t, err)
	pass, err := worker.Pass(t.Context(), 100)
	require.NoError(t, err)
	require.True(t, pass.Complete)
	require.Equal(t, 1, pass.Held)
	require.Zero(t, pass.Recovered)
	records, err := jobs.OpenRepository(store)
	require.NoError(t, err)
	service, err := jobs.NewService(records, jobs.WithRequiredSettlement(&budgetOwner{ledger: ledger}))
	require.NoError(t, err)
	_, err = service.Sweep(t.Context())
	require.NoError(t, err)
	job, err := records.Get(t.Context(), postBackupAccount, "post-backup-job")
	require.NoError(t, err)
	require.True(t, job.SubmissionPending)
	_, err = service.Refresh(t.Context(), a.runner, postBackupAccount, job.ID)
	require.ErrorIs(t, err, jobs.ErrSubmissionUnconfirmed)
	_, err = service.Cancel(t.Context(), a.runner, postBackupAccount, job.ID)
	require.ErrorIs(t, err, jobs.ErrSubmissionUnconfirmed)
	require.Equal(t, 1, a.runner.submits)
	require.Zero(t, a.runner.polls)
	require.Zero(t, a.runner.cancels)
	require.Zero(t, a.runner.fetches)
}
