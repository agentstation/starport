package recovery

import (
	"context"
	"database/sql"
	"encoding/json/v2"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/authorization/revision"
	"github.com/agentstation/starport/internal/blob"
	"github.com/agentstation/starport/internal/credentials"
	"github.com/agentstation/starport/internal/identity"
	"github.com/agentstation/starport/internal/sqlstore"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

// preAdoptionAcceptance mirrors historyAcceptance before the optional adoption binding.
type preAdoptionAcceptance struct {
	Version        int                `json:"version"`
	HistorySHA256  string             `json:"history_sha256"`
	BackupSHA256   string             `json:"backup_sha256"`
	PreparedSHA256 string             `json:"prepared_sha256"`
	TargetSHA256   string             `json:"target_sha256"`
	Operation      RestoreOperation   `json:"operation"`
	Attestation    HistoryAttestation `json:"attestation"`
}

// preAdoptionRunRecord mirrors historyRunRecord before the journal mode.
type preAdoptionRunRecord struct {
	Version            int                        `json:"version"`
	AcceptanceSHA256   string                     `json:"acceptance_sha256"`
	HistorySHA256      string                     `json:"history_sha256"`
	TargetSHA256       string                     `json:"target_sha256"`
	Authority          revision.RecoveryAuthority `json:"authority"`
	ValidatedAt        time.Time                  `json:"validated_at"`
	CatalogPreparation *CatalogPreparationPlan    `json:"catalog_preparation,omitempty"`
}

func TestImportAcceptanceAndRunnerBytesUnchanged(t *testing.T) {
	a, b, c, d := strings.Repeat("a", 64), strings.Repeat("b", 64), strings.Repeat("c", 64), strings.Repeat("d", 64)
	attestation := HistoryAttestation{Operator: "operator", Reference: "acceptance-reference", WritersFenced: true, AdmittedWorkAccounted: true, CompleteInterval: true}
	acceptance := historyAcceptance{Version: 1, HistorySHA256: a, BackupSHA256: b, PreparedSHA256: c, TargetSHA256: d,
		Operation: RestoreOperation{ID: "restore-operation", FencingEvidence: "fence-reference"}, Attestation: attestation}
	body, err := json.Marshal(acceptance, json.Deterministic(true))
	require.NoError(t, err)
	require.Equal(t, `{"version":1,"history_sha256":"`+a+`","backup_sha256":"`+b+`","prepared_sha256":"`+c+`","target_sha256":"`+d+`",`+
		`"operation":{"ID":"restore-operation","FencingEvidence":"fence-reference"},`+
		`"attestation":{"operator":"operator","reference":"acceptance-reference","writers_fenced":true,"admitted_work_accounted":true,"complete_interval":true}}`, string(body))
	run, err := json.Marshal(historyRunRecord{Version: 1, AcceptanceSHA256: a, HistorySHA256: b, TargetSHA256: c}, json.Deterministic(true))
	require.NoError(t, err)
	require.Equal(t, `{"version":1,"acceptance_sha256":"`+a+`","history_sha256":"`+b+`","target_sha256":"`+c+`","authority":{"recovery_id":"","epoch":""},"validated_at":"0001-01-01T00:00:00Z"}`, string(run))

	// A real import acceptance keeps the exact pre-adoption encoding.
	_, source, verified, request := historyAcceptanceFixture(t, false)
	prepared, err := prepareHistoryAcceptance(source, verified, request)
	require.NoError(t, err)
	require.Nil(t, prepared.adoption)
	m := verified.state.manifest
	mirror, err := json.Marshal(preAdoptionAcceptance{Version: 1, HistorySHA256: verified.Digest(), BackupSHA256: m.BackupSHA256, PreparedSHA256: m.PreparedSHA256,
		TargetSHA256: m.TargetSHA256, Operation: m.Operation, Attestation: request.Attestation}, json.Deterministic(true))
	require.NoError(t, err)
	require.Equal(t, string(mirror), string(prepared.body))
	require.NotContains(t, string(prepared.body), "adoption")

	// A real import journal keeps the exact pre-adoption runner record.
	f := newHistoryRunnerFixtureWithFinalPolicy(t, false, true, "", true)
	f.run(t)
	written, err := os.ReadFile(filepath.Join(f.accepted.state.directory, "runner.json"))
	require.NoError(t, err)
	var record preAdoptionRunRecord
	require.NoError(t, json.Unmarshal(written, &record, json.RejectUnknownMembers(true)))
	again, err := json.Marshal(record, json.Deterministic(true))
	require.NoError(t, err)
	require.Equal(t, string(again), string(written))
	require.NotContains(t, string(written), `"mode"`)
}

func TestBindPriorApproval(t *testing.T) {
	prior := Record{DeploymentID: "deployment", Epoch: 4, Open: true, BackendID: "prior-backend", Evidence: "prior-proof"}
	closed := Record{DeploymentID: "deployment", Epoch: 5, BackendID: "prior-backend", Evidence: "prior-proof"}
	for _, test := range []struct {
		name            string
		captured, prior func() Record
		want            error
	}{
		{name: "closed-prior", want: nil},
		{name: "prior-not-open", prior: func() Record { p := prior; p.Open = false; return p }, want: ErrConflict},
		{name: "prior-without-backend", prior: func() Record { p := prior; p.BackendID = " "; return p }, want: ErrConflict},
		{name: "prior-without-evidence", prior: func() Record { p := prior; p.Evidence = ""; return p }, want: ErrConflict},
		{name: "prior-exhausted", prior: func() Record { p := prior; p.Epoch = math.MaxInt64 - 1; return p }, want: ErrConflict},
		{name: "captured-open", captured: func() Record { c := closed; c.Open = true; return c }, want: ErrConflict},
		{name: "other-deployment", captured: func() Record { c := closed; c.DeploymentID = "other"; return c }, want: ErrConflict},
		{name: "restored-equal-epoch", captured: func() Record { c := closed; c.Epoch = prior.Epoch; return c }, want: ErrRestoredWitness},
		{name: "restored-lower-epoch", captured: func() Record { c := closed; c.Epoch = prior.Epoch - 2; return c }, want: ErrRestoredWitness},
		{name: "skipped-epoch", captured: func() Record { c := closed; c.Epoch++; return c }, want: ErrEpochConflict},
		{name: "other-backend", captured: func() Record { c := closed; c.BackendID = "other-backend"; return c }, want: ErrEpochConflict},
		{name: "other-evidence", captured: func() Record { c := closed; c.Evidence = "other-proof"; return c }, want: ErrEpochConflict},
	} {
		t.Run(test.name, func(t *testing.T) {
			c, p := closed, prior
			if test.captured != nil {
				c = test.captured()
			}
			if test.prior != nil {
				p = test.prior()
			}
			err := bindPriorApproval(c, p)
			if test.want == nil {
				require.NoError(t, err)
				return
			}
			require.ErrorIs(t, err, test.want)
			require.ErrorIs(t, err, ErrConflict)
			if test.want == ErrRestoredWitness {
				require.NotErrorIs(t, err, ErrEpochConflict)
			}
		})
	}
}

func TestRecordAdoptionBoundary(t *testing.T) {
	digest := strings.Repeat("a", 64)
	for _, test := range []struct {
		record Record
		want   bool
	}{
		{Record{DeploymentID: "deployment", Epoch: 5, BackendID: "prior-backend", Evidence: "adoption-epoch:" + digest}, true},
		{Record{DeploymentID: "deployment", Epoch: 5, Open: true, BackendID: "prior-backend", Evidence: "adoption-epoch:" + digest}, false},
		{Record{DeploymentID: "deployment", Epoch: 5, BackendID: "prior-backend", Evidence: "adoption-epoch:" + strings.ToUpper(digest)}, false},
		{Record{DeploymentID: "deployment", Epoch: 5, BackendID: "prior-backend", Evidence: "adoption-epoch:" + digest[:62]}, false},
		{Record{DeploymentID: "deployment", Epoch: 5, BackendID: "prior-backend", Evidence: "adoption-epoch:operator-choice"}, false},
		{Record{DeploymentID: "deployment", Epoch: 5, BackendID: "prior-backend", Evidence: "restore-epoch:" + digest}, false},
		{Record{DeploymentID: "deployment", Epoch: 5, BackendID: "prior-backend", Evidence: digest}, false},
	} {
		require.Equal(t, test.want, test.record.AdoptionBoundary(), test.record.Evidence)
	}
}

func TestClosedAdoptionEpochTransitions(t *testing.T) {
	base := ClosedAdoptionEpochRequest{
		Closed:        Record{DeploymentID: "deployment", Epoch: 6, BackendID: "prior-backend", Evidence: "adoption-prepared:" + strings.Repeat("e", 64)},
		PriorApproval: Record{DeploymentID: "deployment", Epoch: 4, Open: true, BackendID: "prior-backend", Evidence: "prior-proof"},
		Evidence:      EpochEvidence{HighestEpoch: 5, SourceSHA256: strings.Repeat("a", 64), Reference: "accepted-history:reference", Operator: "operator"},
	}
	for _, test := range []struct {
		name   string
		change func(*ClosedAdoptionEpochRequest)
		epoch  int64
		want   error
	}{
		{name: "highest-just-below-closed", epoch: 6},
		{name: "highest-equal-closed", change: func(r *ClosedAdoptionEpochRequest) { r.Evidence.HighestEpoch = 6 }, epoch: 7},
		{name: "highest-above-closed", change: func(r *ClosedAdoptionEpochRequest) { r.Evidence.HighestEpoch = 11 }, epoch: 12},
		{name: "highest-below-closed", change: func(r *ClosedAdoptionEpochRequest) { r.Evidence.HighestEpoch = 4 }, want: ErrEpochConflict},
		{name: "prior-equal-closed", change: func(r *ClosedAdoptionEpochRequest) { r.PriorApproval.Epoch = 6 }, want: ErrEpochConflict},
		{name: "prior-above-closed", change: func(r *ClosedAdoptionEpochRequest) { r.PriorApproval.Epoch = 7 }, want: ErrEpochConflict},
		{name: "closed-open", change: func(r *ClosedAdoptionEpochRequest) { r.Closed.Open = true }, want: ErrConflict},
		{name: "closed-without-backend", change: func(r *ClosedAdoptionEpochRequest) { r.Closed.BackendID = ""; r.PriorApproval.BackendID = "" }, want: ErrConflict},
		{name: "backend-differs", change: func(r *ClosedAdoptionEpochRequest) { r.PriorApproval.BackendID = "other-backend" }, want: ErrConflict},
		{name: "deployment-differs", change: func(r *ClosedAdoptionEpochRequest) { r.PriorApproval.DeploymentID = "other" }, want: ErrConflict},
		{name: "prior-closed", change: func(r *ClosedAdoptionEpochRequest) { r.PriorApproval.Open = false }, want: ErrConflict},
		{name: "highest-exhausted", change: func(r *ClosedAdoptionEpochRequest) { r.Evidence.HighestEpoch = math.MaxInt64 - 1 }, want: ErrConflict},
		{name: "reference-control", change: func(r *ClosedAdoptionEpochRequest) { r.Evidence.Reference = "line\nbreak" }, want: ErrConflict},
		{name: "operator-empty", change: func(r *ClosedAdoptionEpochRequest) { r.Evidence.Operator = " " }, want: ErrConflict},
		{name: "source-uppercase", change: func(r *ClosedAdoptionEpochRequest) { r.Evidence.SourceSHA256 = strings.Repeat("A", 64) }, want: ErrConflict},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := base
			if test.change != nil {
				test.change(&request)
			}
			next, digest, err := request.next()
			if test.want != nil {
				require.ErrorIs(t, err, test.want)
				if test.want == ErrConflict {
					require.NotErrorIs(t, err, ErrEpochConflict)
				}
				return
			}
			require.NoError(t, err)
			require.Equal(t, Record{DeploymentID: "deployment", Epoch: test.epoch, BackendID: "prior-backend", Evidence: "adoption-epoch:" + digest}, next)
			again, repeated, err := request.next()
			require.NoError(t, err)
			require.Equal(t, next, again)
			require.Equal(t, digest, repeated)
			request.Evidence.Reference += "-changed"
			_, changed, err := request.next()
			require.NoError(t, err)
			require.NotEqual(t, digest, changed)
		})
	}
}

// adoptionFixture is one fenced populated deployment with its capture C and zero-prefix history H.
type adoptionFixture struct {
	witness     *Witness
	source      *RestoreSource
	history     *VerifiedHistory
	historyReq  HistoryPackageRequest
	prefix      *VerifiedHistory
	prior       Record
	captured    Record
	targets     PopulatedTargets
	replay      HistoryReplayTargets
	kv          storage.KVStore
	incarnation string
	blobTarget  blob.RestoreTarget
	scratch     string
	accept      HistoryAcceptanceRequest
}

type adoptionOptions struct {
	highest   int64
	authority func(prior Record) (Record, bool)
}

func newAdoptionFixture(t *testing.T, sqlType string, options adoptionOptions) *adoptionFixture {
	t.Helper()
	if os.Getenv("TEST_BLOB_S3_ENDPOINT") == "" {
		t.Skip("UNVERIFIED: populated adoption requires the object storage fixture")
	}
	kv, transfer, _ := kvTransferStores(t, storage.StorageTypeValkey)
	require.NoError(t, kv.Set(t.Context(), "account:one", []byte("account record")))
	db, err := sqlstore.Open(historySQLConfig(t, sqlType))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	require.NoError(t, db.Migrate(t.Context()))
	witness, err := New(db)
	require.NoError(t, err)
	initial, err := witness.Initialize(t.Context(), "deployment")
	require.NoError(t, err)
	first, err := witness.Approve(t.Context(), initial, "first-backend", "first-proof")
	require.NoError(t, err)
	reopened, err := witness.Close(t.Context(), first)
	require.NoError(t, err)
	// The prior approval follows one earlier epoch, so a skipped epoch stays representable.
	prior, err := witness.Approve(t.Context(), reopened, "prior-backend", "prior-proof")
	require.NoError(t, err)
	people, err := identity.Open(db)
	require.NoError(t, err)
	_, err = people.Users.Create(t.Context(), identity.User{ID: "person", Subject: "private-subject"})
	require.NoError(t, err)
	approval, present := prior, true
	if options.authority != nil {
		approval, present = options.authority(prior)
	}
	if present {
		body, err := authorityBytes(approval, "prior-operation")
		require.NoError(t, err)
		require.NoError(t, kv.Set(t.Context(), authorityKey, body))
	}
	captured, err := witness.Close(t.Context(), prior)
	require.NoError(t, err)
	store := bundleObjectFixture(t)
	_, err = store.Publish(t.Context(), "file-one", strings.NewReader("linked bytes"))
	require.NoError(t, err)
	require.NoError(t, store.Retire(t.Context(), "old-file"))
	encryption, err := credentials.NewEncryptionService([]byte(strings.Repeat("k", 32)))
	require.NoError(t, err)
	configFile := filepath.Join(privateKVDirectory(t), "config.env")
	require.NoError(t, os.WriteFile(configFile, []byte("STARPORT_CATALOG_SOURCE=embedded\n"), 0o600))
	directory := filepath.Join(privateKVDirectory(t), "capture")
	manifest, err := BackupBundle(t.Context(), directory, BundleSources{KV: transfer, SQL: db, Blobs: store, Encryption: encryption, Files: []BundleFile{{ID: "configuration/config.env", Path: configFile}}},
		BundleRequest{OperationID: "capture-1", Build: "qualification", Boundary: captured, FencingEvidence: "test/all-fixture-writers-owned", KeyReference: "test/master", ExternalRequirements: []string{"provider credentials supplied by the operator"}})
	require.NoError(t, err)
	digest, err := manifest.Digest()
	require.NoError(t, err)
	source, err := InspectRestoreSource(t.Context(), VerifyRequest{Directory: directory, ManifestSHA256: digest, ScratchDirectory: privateKVDirectory(t)}, encryption)
	require.NoError(t, err)
	view := historyPayloadView(t, transfer)
	_, kvExpected, err := revision.CaptureKVRecovery(t.Context(), view)
	require.NoError(t, err)
	conn, err := db.Conn(t.Context())
	require.NoError(t, err)
	sqlExpected, err := revision.CaptureSQLRecovery(t.Context(), db, conn)
	require.NoError(t, err)
	require.NoError(t, conn.Close())
	final := []historyFixturePayload{
		{"kv_authorization_final", historyKVAuthorityPayload{Version: 1, ExpectedSHA256: kvExpected}},
		{"sql_authorization_final", historySQLAuthorityPayload{Version: 1, Expected: sqlExpected}},
	}
	highest := captured.Epoch + options.highest
	history, historyReq := adoptionHistory(t, source, manifest, highest, final)
	grant := identity.AccountGrant{AccountID: "tenant", UserID: "person", CreatedAt: time.Now().UTC()}
	create, err := identity.NewRecoveryTransition([]identity.RecoveryIdentityEvent{{Grant: &identity.RecoveryGrantChange{After: &grant}}})
	require.NoError(t, err)
	withdraw, err := identity.NewRecoveryTransition([]identity.RecoveryIdentityEvent{{Grant: &identity.RecoveryGrantChange{Before: &grant}}})
	require.NoError(t, err)
	prefix, _ := adoptionHistory(t, source, manifest, highest, append([]historyFixturePayload{{"sql_identity", create}, {"sql_identity", withdraw}}, final...))
	objects, ok := store.(*blob.ObjectStore)
	require.True(t, ok)
	blobTarget, err := blob.ObjectRestoreTarget(objects)
	require.NoError(t, err)
	incarnation, err := kv.(storage.IncarnationProvider).ObserveIncarnation(t.Context())
	require.NoError(t, err)
	return &adoptionFixture{
		witness: witness, source: source, history: history, historyReq: historyReq, prefix: prefix, prior: prior, captured: captured,
		targets:     PopulatedTargets{KV: transfer.(storage.PopulatedImportClaimer), SQL: db, Blobs: blobTarget.(blob.PopulatedImportClaimer)},
		replay:      HistoryReplayTargets{KV: transfer.(HistoryKVTarget), Blobs: blobTarget.(HistoryBlobTarget), Encryption: encryption},
		kv:          kv,
		incarnation: incarnation,
		blobTarget:  blobTarget,
		scratch:     privateKVDirectory(t),
		accept: HistoryAcceptanceRequest{Directory: privateKVDirectory(t), Attestation: HistoryAttestation{Operator: "private-operator", Reference: "private-acceptance-reference",
			WritersFenced: true, AdmittedWorkAccounted: true, CompleteInterval: true}},
	}
}

// adoptionHistory writes an H that binds C.ImportIdentity with the given steps.
func adoptionHistory(t *testing.T, source *RestoreSource, manifest BundleManifest, highest int64, inputs []historyFixturePayload) (*VerifiedHistory, HistoryPackageRequest) {
	t.Helper()
	_, request, history := historyPackageFixture(t)
	prepared, err := source.ImportIdentity(request.Operation)
	require.NoError(t, err)
	encoded, err := json.Marshal(prepared, json.Deterministic(true))
	require.NoError(t, err)
	digest, err := manifest.Digest()
	require.NoError(t, err)
	history.BackupSHA256 = digest
	history.DeploymentID = source.DeploymentID()
	history.PreparedSHA256 = historySHA256(encoded)
	history.HighestEpoch.HighestEpoch = highest
	history.Interval.Through = manifest.FinishedAt.Add(time.Second)
	history.Disposition = "replay_complete"
	for i, input := range inputs {
		data := historyPayloadJSON(t, input.value)
		path := fmt.Sprintf("payloads/%06d.json", i+1)
		require.NoError(t, os.WriteFile(filepath.Join(request.Directory, path), data, 0o600))
		history.Steps = append(history.Steps, historyStep{Ordinal: i + 1, Kind: input.kind, Path: path, Size: len(data), SHA256: historySHA256(data), Evidence: []string{"source"}})
	}
	request = writeHistoryManifest(t, request, history)
	verified, err := source.VerifyHistoryPackage(t.Context(), request)
	require.NoError(t, err)
	return verified, request
}

func (f *adoptionFixture) observe(t *testing.T) PopulatedControls {
	t.Helper()
	controls, err := ObservePopulatedControls(t.Context(), f.targets)
	require.NoError(t, err)
	return controls
}

func (f *adoptionFixture) claim(t *testing.T, controls PopulatedControls) PreparedImportIdentity {
	t.Helper()
	identity, err := f.source.ClaimPopulated(t.Context(), f.history, f.targets, PopulatedClaimRequest{PriorApproval: f.prior, Controls: controls, ScratchDirectory: f.scratch})
	require.NoError(t, err)
	return identity
}

// requireUnclaimed proves that a refusal left the SQL witness, every barrier, and the blob controls unchanged.
func (f *adoptionFixture) requireUnclaimed(t *testing.T, controls PopulatedControls) {
	t.Helper()
	current, err := f.witness.Current(t.Context(), f.captured.DeploymentID)
	require.NoError(t, err)
	require.Equal(t, f.captured, current)
	require.NoError(t, f.witness.db.CheckImportBarrier(t.Context()))
	require.NoError(t, storage.CheckImportBarrier(t.Context(), f.kv))
	require.Equal(t, controls, f.observe(t))
}

func adoptionSQLTypes() []string { return []string{sqlstore.TypePostgres, sqlstore.TypeMySQL} }

func TestPopulatedAdoptionNativeEpochSequenceAndApproval(t *testing.T) {
	for _, kind := range adoptionSQLTypes() {
		t.Run(kind, func(t *testing.T) {
			f := newAdoptionFixture(t, kind, adoptionOptions{highest: 3})
			controls := f.observe(t)
			imported, err := f.source.ImportIdentity(f.history.state.manifest.Operation)
			require.NoError(t, err)
			identity := f.claim(t, controls)
			require.NotEqual(t, imported.ComponentOperation, identity.ComponentOperation, "populated claims differ from empty-target claims")
			require.NotEqual(t, imported.KVClaim, identity.KVClaim)
			require.NotEqual(t, imported.SQL, identity.SQL)
			require.Equal(t, imported.SQLOriginal, identity.SQLOriginal)
			require.Equal(t, imported.KVOriginal, identity.KVOriginal)
			require.Equal(t, imported.BlobOriginal, identity.BlobOriginal)
			require.Equal(t, f.captured.Epoch+1, identity.Boundary.Epoch)
			require.False(t, identity.Boundary.Open)
			require.Equal(t, f.prior.BackendID, identity.Boundary.BackendID)
			require.True(t, strings.HasPrefix(identity.Boundary.Evidence, "adoption-prepared:"))
			current, err := f.witness.Current(t.Context(), f.captured.DeploymentID)
			require.NoError(t, err)
			require.Equal(t, identity.Boundary, current)
			require.ErrorIs(t, f.witness.db.CheckImportBarrier(t.Context()), sqlstore.ErrImportRestricted)
			require.ErrorIs(t, storage.CheckImportBarrier(t.Context(), f.kv), storage.ErrImportRestricted)
			require.Equal(t, identity, f.claim(t, controls), "an exact claim retry continues the same claim")
			// The populated marker equals an ordinary import claim, so only a distinct identity separates the two paths.
			image := filepath.Join(f.source.request.Directory, bundleSQLFile)
			restricted := false
			restrict := func(context.Context, *sql.Conn) error {
				restricted = true
				return nil
			}
			require.ErrorIs(t, f.witness.db.ImportRelationalOnce(t.Context(), image, imported.SQLOriginal, privateKVDirectory(t), imported.SQL, restrict), sqlstore.ErrImportRestricted,
				"an empty-target import of the same capture refuses the populated claim")
			require.NoError(t, f.witness.db.ImportRelationalOnce(t.Context(), image, identity.SQLOriginal, privateKVDirectory(t), identity.SQL, restrict),
				"the sqlstore owner treats the equal marker as an exact import retry")
			require.False(t, restricted, "neither import runs the witness transition")
			current, err = f.witness.Current(t.Context(), f.captured.DeploymentID)
			require.NoError(t, err)
			require.Equal(t, identity.Boundary, current, "only the claim callback moves the captured record")

			accepted, err := f.witness.AcceptClosedAdoptionHistory(t.Context(), f.source, f.history, f.prior, f.accept)
			require.NoError(t, err)
			highest := f.history.state.manifest.HighestEpoch.HighestEpoch
			require.Equal(t, max(identity.Boundary.Epoch, highest+1), accepted.state.boundary.Epoch)
			require.False(t, accepted.state.boundary.Open)
			require.Equal(t, f.prior.BackendID, accepted.state.boundary.BackendID)
			require.True(t, strings.HasPrefix(accepted.state.boundary.Evidence, "adoption-epoch:"))
			require.True(t, accepted.state.boundary.AdoptionBoundary())
			current, err = f.witness.Current(t.Context(), f.captured.DeploymentID)
			require.NoError(t, err)
			require.Equal(t, accepted.state.boundary, current)
			body, err := os.ReadFile(filepath.Join(f.accept.Directory, "acceptance.json"))
			require.NoError(t, err)
			var retained historyAcceptance
			require.NoError(t, json.Unmarshal(body, &retained, json.RejectUnknownMembers(true)))
			require.NotNil(t, retained.Adoption)
			encoded, err := json.Marshal(identity, json.Deterministic(true))
			require.NoError(t, err)
			require.Equal(t, historyAdoption{Version: 1, CaptureSHA256: f.source.request.ManifestSHA256, IdentitySHA256: historySHA256(encoded), PriorApproval: f.prior, Closed: identity.Boundary}, *retained.Adoption)
			again, err := f.witness.AcceptClosedAdoptionHistory(t.Context(), f.source, f.history, f.prior, f.accept)
			require.NoError(t, err)
			require.Equal(t, accepted.Digest(), again.Digest())
			_, err = f.witness.AcceptImportedHistory(t.Context(), f.source, f.history, f.accept)
			require.ErrorIs(t, err, ErrConflict, "import acceptance cannot reuse an adoption journal")
			_, err = f.source.ClaimPopulated(t.Context(), f.history, f.targets, PopulatedClaimRequest{PriorApproval: f.prior, Controls: controls, ScratchDirectory: f.scratch})
			require.ErrorIs(t, err, ErrConflict, "a claim retry after acceptance refuses")

			request := HistoryReplayRequest{TargetSHA256: f.historyReq.TargetSHA256, ScratchDirectory: privateKVDirectory(t)}
			completed, err := f.witness.ReplayImportedHistory(t.Context(), f.source, accepted, f.replay, request)
			require.NoError(t, err)
			report := completed.Report()
			require.Equal(t, 2, report.CompletedSteps)
			require.True(t, report.KVRotated)
			require.True(t, report.SQLRotated)
			require.Equal(t, identity, completed.runner.identity)
			run, err := os.ReadFile(filepath.Join(f.accept.Directory, "runner.json"))
			require.NoError(t, err)
			var record historyRunRecord
			require.NoError(t, json.Unmarshal(run, &record, json.RejectUnknownMembers(true)))
			require.Equal(t, closedAdoptionMode, record.Mode)
			restarted, err := f.witness.ReplayImportedHistory(t.Context(), f.source, accepted, f.replay, request)
			require.NoError(t, err)
			require.Equal(t, report, restarted.Report())

			finalRequest := ClosedFinalRequest{TargetSHA256: f.historyReq.TargetSHA256, Attestation: f.accept.Attestation}
			final, err := f.witness.FinalizeImportedHistory(t.Context(), completed, finalRequest)
			require.NoError(t, err)
			decision := final.Report().DecisionSHA256
			retainedRequest := RetainedActivationHistoryRequest{History: f.historyReq, Directory: f.accept.Directory, DecisionSHA256: decision,
				ScratchDirectory: privateKVDirectory(t), Attestation: f.accept.Attestation, PriorApproval: f.prior}
			retainedHistory, err := OpenRetainedActivationHistory(t.Context(), f.source, retainedRequest, f.replay.Encryption)
			require.NoError(t, err, "the prior approval selects the adoption journal")
			require.Equal(t, final.Report(), retainedHistory.Report())
			require.NoError(t, retainedHistory.Check(t.Context(), f.source, retainedRequest, f.replay.Encryption))
			importMode := retainedRequest
			importMode.PriorApproval = Record{}
			_, err = OpenRetainedActivationHistory(t.Context(), f.source, importMode, f.replay.Encryption)
			require.ErrorIs(t, err, ErrConflict, "a zero prior approval cannot open an adoption journal")
			require.NoError(t, f.blobTarget.(blob.ImportReplayActivator).ActivateImportAt(t.Context(), identity.ComponentOperation, identity.BlobOriginal, report.Positions.Blobs, decision))
			require.NoError(t, f.replay.KV.(storage.ImportReplayActivator).ActivateImportAt(t.Context(), identity.KVClaim, report.Positions.KV, decision))
			ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
			defer cancel()
			checked := false
			approval := ImportedAuthorityRequest{Closed: accepted.state.boundary, BackendID: f.incarnation, Evidence: "populated-adoption-proof",
				OperationID: identity.SQL.OperationID, Snapshot: identity.SQLOriginal, Import: identity.SQL, DecisionSHA256: decision}
			approved, err := f.witness.ApproveImportedAuthorityCheckedAt(ctx, f.kv.(storage.IncarnationProvider), approval, report.Positions.SQL, func(context.Context) error {
				checked = true
				return nil
			})
			require.NoError(t, err)
			require.True(t, checked)
			require.Equal(t, Record{DeploymentID: f.captured.DeploymentID, Epoch: accepted.state.boundary.Epoch, Open: true, BackendID: f.incarnation, Evidence: "populated-adoption-proof"}, approved)
			current, err = f.witness.Approved(t.Context(), f.captured.DeploymentID)
			require.NoError(t, err)
			require.Equal(t, approved, current)
			stored, err := f.kv.Get(t.Context(), authorityKey)
			require.NoError(t, err)
			var authority authorityRecord
			require.NoError(t, json.Unmarshal(stored, &authority))
			require.Equal(t, approved, authority.Approval)
			require.Equal(t, identity.SQL.OperationID, authority.OperationID)
			value, err := f.kv.Get(t.Context(), "account:one")
			require.NoError(t, err)
			require.Equal(t, []byte("account record"), value, "adoption copies no domain data")
		})
	}
}

func TestPopulatedAdoptionNativeEpochRule(t *testing.T) {
	for _, test := range []struct {
		name    string
		highest int64
	}{
		{name: "highest-equals-captured", highest: 0},
		{name: "highest-equals-prepared", highest: 1},
		{name: "highest-above-prepared", highest: 5},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newAdoptionFixture(t, sqlstore.TypePostgres, adoptionOptions{highest: test.highest})
			identity := f.claim(t, f.observe(t))
			accepted, err := f.witness.AcceptClosedAdoptionHistory(t.Context(), f.source, f.history, f.prior, f.accept)
			require.NoError(t, err)
			require.Equal(t, max(identity.Boundary.Epoch, f.captured.Epoch+test.highest+1), accepted.state.boundary.Epoch)
			require.Greater(t, accepted.state.boundary.Epoch, f.prior.Epoch)
		})
	}
}

func TestPopulatedAdoptionNativeRefusesConflictingEpochs(t *testing.T) {
	for _, test := range []struct {
		name      string
		authority func(Record) (Record, bool)
		prior     func(prior, captured Record) Record
		want      error
	}{
		{name: "kv-authority-missing", authority: func(Record) (Record, bool) { return Record{}, false }, want: ErrEpochConflict},
		{name: "kv-authority-other-epoch", authority: func(p Record) (Record, bool) { p.Epoch--; return p, true }, want: ErrEpochConflict},
		{name: "kv-authority-other-backend", authority: func(p Record) (Record, bool) { p.BackendID = "other-backend"; return p, true }, want: ErrEpochConflict},
		{name: "prior-skips-epoch", prior: func(p, _ Record) Record { p.Epoch--; return p }, want: ErrEpochConflict},
		{name: "prior-other-evidence", prior: func(p, _ Record) Record { p.Evidence = "other-proof"; return p }, want: ErrEpochConflict},
		// The SQL witness is at or below the prior approval, so SQL was restored from an older image.
		{name: "restored-sql-witness", authority: func(p Record) (Record, bool) { p.Epoch += 2; return p, true },
			prior: func(p, _ Record) Record { p.Epoch += 2; return p }, want: ErrRestoredWitness},
		{name: "restored-sql-witness-equal", prior: func(p, c Record) Record { p.Epoch = c.Epoch; return p }, want: ErrRestoredWitness},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newAdoptionFixture(t, sqlstore.TypePostgres, adoptionOptions{highest: 3, authority: test.authority})
			prior := f.prior
			if test.prior != nil {
				prior = test.prior(f.prior, f.captured)
			}
			controls := f.observe(t)
			if test.prior != nil {
				_, err := f.source.AdoptionIdentity(f.history, prior)
				require.ErrorIs(t, err, test.want)
			}
			_, err := f.source.ClaimPopulated(t.Context(), f.history, f.targets, PopulatedClaimRequest{PriorApproval: prior, Controls: controls, ScratchDirectory: f.scratch})
			require.ErrorIs(t, err, test.want)
			f.requireUnclaimed(t, controls)
			_, err = f.witness.AcceptClosedAdoptionHistory(t.Context(), f.source, f.history, prior, f.accept)
			require.ErrorIs(t, err, test.want)
			_, err = os.Stat(filepath.Join(f.accept.Directory, "acceptance.json"))
			require.ErrorIs(t, err, os.ErrNotExist)
			f.requireUnclaimed(t, controls)
		})
	}
}

func TestPopulatedAdoptionNativeRefusesPrefixStep(t *testing.T) {
	f := newAdoptionFixture(t, sqlstore.TypePostgres, adoptionOptions{highest: 3})
	controls := f.observe(t)
	require.Positive(t, f.prefix.StepCount()-f.history.StepCount())
	_, err := f.source.AdoptionIdentity(f.prefix, f.prior)
	require.ErrorIs(t, err, ErrAdoptionPrefix)
	_, err = f.source.ClaimPopulated(t.Context(), f.prefix, f.targets, PopulatedClaimRequest{PriorApproval: f.prior, Controls: controls, ScratchDirectory: f.scratch})
	require.ErrorIs(t, err, ErrAdoptionPrefix)
	f.requireUnclaimed(t, controls)

	f.claim(t, controls)
	_, err = f.witness.AcceptClosedAdoptionHistory(t.Context(), f.source, f.prefix, f.prior, f.accept)
	require.ErrorIs(t, err, ErrAdoptionPrefix)
	_, err = os.Stat(filepath.Join(f.accept.Directory, "acceptance.json"))
	require.ErrorIs(t, err, os.ErrNotExist)

	// A forged private state with a prefix history cannot start a zero-prefix journal.
	accepted, err := f.witness.AcceptClosedAdoptionHistory(t.Context(), f.source, f.history, f.prior, f.accept)
	require.NoError(t, err)
	state := *accepted.state
	state.history = f.prefix.state
	request := HistoryReplayRequest{TargetSHA256: f.historyReq.TargetSHA256, ScratchDirectory: privateKVDirectory(t)}
	_, err = f.witness.ReplayImportedHistory(t.Context(), f.source, &AcceptedHistory{state: &state}, f.replay, request)
	require.ErrorIs(t, err, ErrAdoptionPrefix)
	_, err = os.Stat(filepath.Join(f.accept.Directory, "runner.json"))
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestHistoryRunnerRefusesModeMismatch(t *testing.T) {
	rewrite := func(t *testing.T, directory string, mode string) {
		t.Helper()
		path := filepath.Join(directory, "runner.json")
		body, err := os.ReadFile(path)
		require.NoError(t, err)
		var record historyRunRecord
		require.NoError(t, json.Unmarshal(body, &record, json.RejectUnknownMembers(true)))
		record.Mode = mode
		body, err = json.Marshal(record, json.Deterministic(true))
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(path, body, 0o600))
	}
	t.Run("import-journal-with-adoption-mode", func(t *testing.T) {
		f := newHistoryRunnerFixtureWithFinalPolicy(t, false, true, "", true)
		f.run(t)
		rewrite(t, f.accepted.state.directory, closedAdoptionMode)
		_, err := f.witness.ReplayImportedHistory(t.Context(), f.source, f.accepted, f.targets, f.request)
		require.ErrorIs(t, err, ErrConflict)
	})
	for _, kind := range adoptionSQLTypes() {
		for name, mode := range map[string]string{"absent": "", "other": "closed_adoption_other"} {
			t.Run(kind+"-adoption-journal-mode-"+name, func(t *testing.T) {
				f := newAdoptionFixture(t, kind, adoptionOptions{highest: 3})
				f.claim(t, f.observe(t))
				accepted, err := f.witness.AcceptClosedAdoptionHistory(t.Context(), f.source, f.history, f.prior, f.accept)
				require.NoError(t, err)
				request := HistoryReplayRequest{TargetSHA256: f.historyReq.TargetSHA256, ScratchDirectory: privateKVDirectory(t)}
				_, err = f.witness.ReplayImportedHistory(t.Context(), f.source, accepted, f.replay, request)
				require.NoError(t, err)
				rewrite(t, f.accept.Directory, mode)
				_, err = f.witness.ReplayImportedHistory(t.Context(), f.source, accepted, f.replay, request)
				require.ErrorIs(t, err, ErrConflict)
			})
		}
	}
}

// adoptionLostKV loses the process before the KV claim, or loses the reply after it.
type adoptionLostKV struct {
	storage.PopulatedImportClaimer
	before bool
}

func (o adoptionLostKV) ClaimPopulated(ctx context.Context, claim []byte, controls storage.ImportControls, source storage.CapturedRecordSource) error {
	if o.before {
		return errHistoryLostReply
	}
	if err := o.PopulatedImportClaimer.ClaimPopulated(ctx, claim, controls, source); err != nil {
		return err
	}
	return errHistoryLostReply
}

// adoptionLostBlob loses the reply after the blob claim.
type adoptionLostBlob struct{ blob.PopulatedImportClaimer }

func (o adoptionLostBlob) ClaimPopulated(ctx context.Context, source, scratch, operation string, captured blob.Snapshot, controls blob.PopulatedControls) error {
	if err := o.PopulatedImportClaimer.ClaimPopulated(ctx, source, scratch, operation, captured, controls); err != nil {
		return err
	}
	return errHistoryLostReply
}

func TestPopulatedAdoptionNativeLostRepliesRetryExactly(t *testing.T) {
	for _, kind := range adoptionSQLTypes() {
		for _, step := range []string{"sql-claim", "kv-claim", "blob-claim", "acceptance-publication", "adoption-epoch"} {
			t.Run(kind+"-"+step, func(t *testing.T) {
				f := newAdoptionFixture(t, kind, adoptionOptions{highest: 3})
				controls := f.observe(t)
				wanted, err := f.source.AdoptionIdentity(f.history, f.prior)
				require.NoError(t, err)
				lost := f.targets
				switch step {
				case "sql-claim":
					lost.KV = adoptionLostKV{PopulatedImportClaimer: f.targets.KV, before: true}
				case "kv-claim":
					lost.KV = adoptionLostKV{PopulatedImportClaimer: f.targets.KV}
				case "blob-claim":
					lost.Blobs = adoptionLostBlob{f.targets.Blobs}
				}
				if step == "sql-claim" || step == "kv-claim" || step == "blob-claim" {
					_, err = f.source.ClaimPopulated(t.Context(), f.history, lost, PopulatedClaimRequest{PriorApproval: f.prior, Controls: controls, ScratchDirectory: f.scratch})
					require.ErrorIs(t, err, errHistoryLostReply)
					current, err := f.witness.Current(t.Context(), f.captured.DeploymentID)
					require.NoError(t, err)
					require.Equal(t, wanted.Boundary, current, "the SQL claim moved the witness before the lost reply")
					require.ErrorIs(t, f.witness.db.CheckImportBarrier(t.Context()), sqlstore.ErrImportRestricted)
					if step == "sql-claim" {
						require.NoError(t, storage.CheckImportBarrier(t.Context(), f.kv), "the process was lost before the KV claim")
					} else {
						require.ErrorIs(t, storage.CheckImportBarrier(t.Context(), f.kv), storage.ErrImportRestricted)
					}
				}
				require.Equal(t, wanted, f.claim(t, controls), "the exact retry reuses the retained controls")
				require.Equal(t, wanted, f.claim(t, controls))
				if step == "acceptance-publication" || step == "adoption-epoch" {
					state, err := prepareAdoptionAcceptance(f.source, f.history, f.prior, f.accept)
					require.NoError(t, err)
					require.NoError(t, retainHistoryAcceptance(t.Context(), state, true))
					want := wanted.Boundary
					if step == "adoption-epoch" {
						want, err = state.advance(t.Context(), f.witness)
						require.NoError(t, err)
						require.Equal(t, state.boundary, want)
					}
					current, err := f.witness.Current(t.Context(), f.captured.DeploymentID)
					require.NoError(t, err)
					require.Equal(t, want, current)
				}
				accepted, err := f.witness.AcceptClosedAdoptionHistory(t.Context(), f.source, f.history, f.prior, f.accept)
				require.NoError(t, err)
				again, err := f.witness.AcceptClosedAdoptionHistory(t.Context(), f.source, f.history, f.prior, f.accept)
				require.NoError(t, err)
				require.Equal(t, accepted.Digest(), again.Digest())
				require.Equal(t, accepted.state.boundary, again.state.boundary)
				current, err := f.witness.Current(t.Context(), f.captured.DeploymentID)
				require.NoError(t, err)
				require.Equal(t, accepted.state.boundary, current)
				completed, err := f.witness.ReplayImportedHistory(t.Context(), f.source, accepted, f.replay, HistoryReplayRequest{TargetSHA256: f.historyReq.TargetSHA256, ScratchDirectory: privateKVDirectory(t)})
				require.NoError(t, err)
				require.Equal(t, 2, completed.Report().CompletedSteps)
				require.Equal(t, wanted, completed.runner.identity)
			})
		}
	}
}
