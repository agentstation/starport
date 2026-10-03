package catalog

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	"encoding/json"
	"encoding/json/jsontext"
	jsonv2 "encoding/json/v2"
	"errors"
	"io"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/agentstation/starmap/pkg/catalogs"
	starmaperrors "github.com/agentstation/starmap/pkg/errors"
	"github.com/agentstation/starmap/pkg/sources"
	"github.com/agentstation/starmap/runtime"
	"github.com/stretchr/testify/require"

	"github.com/agentstation/starport/internal/recovery"
	"github.com/agentstation/starport/internal/storage"
)

// memoryIncarnationStore keeps the native compare-and-swap contract of the Valkey script in process memory.
// A nil expected value requires an absent key. A live key requires a positive lifetime.
// A zero mutation lifetime keeps the current lifetime.
type memoryIncarnationStore struct {
	mu      sync.Mutex
	values  map[string][]byte
	expires map[string]time.Time
}

func newMemoryIncarnationStore() *memoryIncarnationStore {
	return &memoryIncarnationStore{values: map[string][]byte{}, expires: map[string]time.Time{}}
}

func (s *memoryIncarnationStore) currentLocked(key string, now time.Time) ([]byte, bool) {
	value, ok := s.values[key]
	if expiry, bounded := s.expires[key]; ok && bounded && !now.Before(expiry) {
		delete(s.values, key)
		delete(s.expires, key)
		return nil, false
	}
	return value, ok
}

func (s *memoryIncarnationStore) ReadWithLifetime(ctx context.Context, key string, maxBytes int) ([]byte, time.Duration, error) {
	if err := ctx.Err(); err != nil {
		return nil, 0, err
	}
	if maxBytes <= 0 {
		return nil, 0, storage.ErrInvalidReadLimit
	}
	if key == "" {
		return nil, 0, storage.ErrInvalidKey
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	value, ok := s.currentLocked(key, now)
	if !ok {
		return nil, 0, storage.ErrNotFound
	}
	if len(value) > maxBytes {
		return nil, 0, storage.ErrValueTooLarge
	}
	var lifetime time.Duration
	if expiry, bounded := s.expires[key]; bounded {
		lifetime = expiry.Sub(now)
	}
	return bytes.Clone(value), lifetime, nil
}

func (s *memoryIncarnationStore) CompareAndSwap(ctx context.Context, mutations []storage.CompareAndSwapMutation, liveKeys ...string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	seen := make(map[string]bool, len(mutations))
	for _, m := range mutations {
		if m.Key == "" || m.TTL < 0 || seen[m.Key] {
			return storage.ErrInvalidMutation
		}
		seen[m.Key] = true
	}
	live := make(map[string]bool, len(liveKeys))
	for _, key := range liveKeys {
		live[key] = true
		if !seen[key] {
			return storage.ErrInvalidMutation
		}
	}
	for _, m := range mutations {
		if live[m.Key] && m.ExpectedValue == nil {
			return storage.ErrInvalidMutation
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	for _, m := range mutations {
		current, ok := s.currentLocked(m.Key, now)
		if m.ExpectedValue == nil {
			if ok {
				return storage.ErrConflict
			}
		} else if !ok || !bytes.Equal(current, m.ExpectedValue) {
			return storage.ErrConflict
		}
		if _, bounded := s.expires[m.Key]; live[m.Key] && !bounded {
			return storage.ErrConflict
		}
	}
	for _, m := range mutations {
		switch {
		case m.NewValue == nil:
			delete(s.values, m.Key)
			delete(s.expires, m.Key)
		case m.TTL > 0:
			s.values[m.Key] = bytes.Clone(m.NewValue)
			s.expires[m.Key] = now.Add(m.TTL)
		default:
			s.values[m.Key] = bytes.Clone(m.NewValue)
		}
	}
	return nil
}

// expire ends the lifetime of one key now, as a lapsed native lease does.
func (s *memoryIncarnationStore) expire(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.values[key]; ok {
		s.expires[key] = time.Now()
	}
}

// memoryFleetWitness approves one backend and grants first use once.
type memoryFleetWitness struct {
	mu       sync.Mutex
	record   recovery.Record
	consumed bool
}

func (w *memoryFleetWitness) Approved(_ context.Context, deployment string) (recovery.Record, error) {
	if deployment != w.record.DeploymentID {
		return recovery.Record{}, recovery.ErrConflict
	}
	return w.record, nil
}

func (w *memoryFleetWitness) CheckBootstrap(_ context.Context, expected recovery.Record) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	switch {
	case expected != w.record:
		return recovery.ErrConflict
	case w.consumed:
		return recovery.ErrBootstrapConsumed
	}
	return nil
}

func (w *memoryFleetWitness) ConsumeBootstrap(_ context.Context, expected recovery.Record) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if expected != w.record || w.consumed {
		return recovery.ErrConflict
	}
	w.consumed = true
	return nil
}

// memoryFleet is one approved shared deployment. Each session is a separate gateway process.
type memoryFleet struct {
	store   *memoryIncarnationStore
	witness *memoryFleetWitness
	kv      storage.KVStore
}

func newMemoryFleet() *memoryFleet {
	deployment := "promotion-" + rand.Text()
	return &memoryFleet{
		store: newMemoryIncarnationStore(),
		witness: &memoryFleetWitness{record: recovery.Record{
			DeploymentID: deployment, Epoch: 1, Open: true, BackendID: "memory-backend", Evidence: "memory-test",
		}},
		kv: storage.NewMockStore(),
	}
}

func (f *memoryFleet) session() *FleetStore {
	return newFleetStore(f.store, f.witness, f.witness.record, f.witness.record.DeploymentID)
}

func (f *memoryFleet) settings(t *testing.T) Settings {
	t.Helper()
	settings := identityTestSettings(filepath.Join(t.TempDir(), "state"), "", "127.0.0.1:0")
	settings.DeploymentID = f.witness.record.DeploymentID
	return settings
}

// open starts one gateway process over a private state directory.
func (f *memoryFleet) open(t *testing.T) *Runtime {
	t.Helper()
	opened, err := openRuntime(t.Context(), f.kv, f.settings(t), runtimeCollectors{fleet: f.session()})
	require.NoError(t, err)
	t.Cleanup(func() { _ = opened.Close(context.Background()) })
	return opened
}

// leaseHolder reads the holder name from the native publication lease.
func (f *memoryFleet) leaseHolder(t *testing.T, fleet *FleetStore) string {
	t.Helper()
	data, _, err := f.store.ReadWithLifetime(t.Context(), fleet.prefix+"lease", 4096)
	require.NoError(t, err)
	var grant fleetGrant
	require.NoError(t, json.Unmarshal(data, &grant))
	require.NotEmpty(t, grant.Holder)
	return grant.Holder
}

// retainOlderBaseline publishes the packaged catalog bytes under an older baseline identity, as an older binary did.
// The forged recovery record keeps the Starmap version 2 format and recomputes its replay compatibility.
// It returns the retained identity and the accepted head.
func retainOlderBaseline(t *testing.T, kv storage.KVStore, fleet func() *FleetStore, settings Settings) (catalogs.GenerationIdentity, runtime.FleetHead) {
	t.Helper()
	ctx := t.Context()
	store := fleet()
	first, err := openRuntime(ctx, kv, settings, runtimeCollectors{fleet: store})
	require.NoError(t, err)
	candidate, err := first.CurrentCandidate(ctx)
	require.NoError(t, err)
	require.NoError(t, first.Accept(ctx, candidate))
	status := first.Status()
	snapshot, err := store.Publication(ctx, candidate.FleetHead)
	require.NoError(t, err)
	require.NoError(t, first.Close(ctx))

	reader, err := gzip.NewReader(bytes.NewReader(snapshot.Publication.Recovery.Data))
	require.NoError(t, err)
	decoded, err := io.ReadAll(reader)
	require.NoError(t, err)
	var record map[string]jsontext.Value
	require.NoError(t, jsonv2.Unmarshal(decoded, &record))
	var baseline catalogs.Generation
	require.NoError(t, jsonv2.Unmarshal(record["baseline"], &baseline, json.FormatDurationAsNano(true)))
	var compatibility string
	require.NoError(t, jsonv2.Unmarshal(record["compatibility"], &compatibility))
	require.Equal(t, compatibility, replayCompatibility(t, baseline.Manifest, status), "the forged record must use the Starmap compatibility formula")
	require.Equal(t, baseline.Manifest.GenerationID, candidate.State.GenerationID, "an embedded-only fleet serves its baseline unchanged")

	older := catalogs.Generation{Manifest: baseline.Manifest.Copy(), Payload: bytes.Clone(baseline.Payload)}
	older.Manifest.GenerationID = "csp162-retained-" + rand.Text()
	older.Manifest.GeneratedAt = baseline.Manifest.GeneratedAt.Add(-24 * time.Hour)
	record["baseline"], err = jsonv2.Marshal(older, jsonv2.Deterministic(true), json.FormatDurationAsNano(true))
	require.NoError(t, err)
	record["compatibility"], err = jsonv2.Marshal(replayCompatibility(t, older.Manifest, status))
	require.NoError(t, err)
	encoded, err := jsonv2.Marshal(record, jsonv2.Deterministic(true))
	require.NoError(t, err)
	var compressed bytes.Buffer
	writer, err := gzip.NewWriterLevel(&compressed, gzip.BestSpeed)
	require.NoError(t, err)
	_, err = writer.Write(encoded)
	require.NoError(t, err)
	require.NoError(t, writer.Close())

	grant, err := store.AcquireLease(ctx, "csp162-older-binary", time.Minute)
	require.NoError(t, err)
	head, err := store.CommitPublication(ctx, runtime.FleetPublication{
		Generation: older, Grant: grant, Expected: candidate.FleetHead,
		Recovery: runtime.FleetRecovery{
			GenerationID: older.Manifest.GenerationID, PayloadChecksum: older.Manifest.Payload.Checksum,
			Checksum: payloadDigest(compressed.Bytes()), Data: compressed.Bytes(),
		},
	})
	require.NoError(t, err)
	require.NoError(t, store.AcceptPublication(ctx, head, candidate.FleetHead))
	require.NoError(t, store.Release(ctx, grant))
	return catalogs.GenerationIdentity{GenerationID: older.Manifest.GenerationID, PayloadChecksum: older.Manifest.Payload.Checksum}, head
}

// replayCompatibility repeats the Starmap replay compatibility value for one embedded-only runtime.
// An embedded fleet has no provider binding, acquisition source, or publisher alias.
func replayCompatibility(t *testing.T, baseline catalogs.GenerationManifest, status runtime.Status) string {
	t.Helper()
	record := struct {
		BaselineID, BaselineChecksum string
		RequireAuthority             bool
		Bindings                     map[string]sources.ProviderAcquisitionBinding
		Sources                      []sources.ID
		Aliases                      []string
		Capabilities                 []sources.SourceActivity
	}{baseline.GenerationID, baseline.Payload.Checksum, status.AuthorityRequired, nil, nil, nil, status.SourceConfiguration}
	data, err := jsonv2.Marshal(record, jsonv2.Deterministic(true), jsonv2.FormatNilMapAsNull(true), jsonv2.FormatNilSliceAsNull(true))
	require.NoError(t, err)
	return payloadDigest(data)
}

// promotableFleet opens one leader that retains an older baseline than its packaged baseline.
func promotableFleet(t *testing.T) (*memoryFleet, *Runtime, BaselineReport) {
	t.Helper()
	fleet := newMemoryFleet()
	retained, _ := retainOlderBaseline(t, fleet.kv, fleet.session, fleet.settings(t))
	leader := fleet.open(t)
	report, err := leader.BaselineReport()
	require.NoError(t, err)
	require.True(t, report.Promotable, report.Refusal)
	require.Equal(t, baselineIdentity(retained), report.Retained)
	require.NotEqual(t, report.Packaged.GenerationID, report.Retained.GenerationID)
	require.Equal(t, report.Packaged.Checksum, report.Retained.Checksum)
	require.Equal(t, fleet.witness.record.DeploymentID, report.DeploymentID)
	return fleet, leader, report
}

func TestFleetPromotionAdvancesHeadWithIncreasingRevision(t *testing.T) {
	fleet, leader, report := promotableFleet(t)
	ctx := t.Context()
	follower := fleet.open(t)
	followerReport, err := follower.BaselineReport()
	require.NoError(t, err)
	require.Equal(t, report.Retained, followerReport.Retained, "a follower replays the retained baseline, not its packaged baseline")
	holder := fleet.leaseHolder(t, leader.fleet)
	refused, err := follower.PromoteBaseline(ctx, PromotionRequest{OperationID: "follower-1", Actor: "operator"})
	require.NoError(t, err)
	require.Equal(t, PromotionRefused, refused.Status)
	require.Contains(t, refused.Refusal, holder)
	require.Contains(t, refused.Refusal, "retry with the same operation ID")
	_, found, err := follower.fleet.promotionReceipt(ctx, "follower-1")
	require.NoError(t, err)
	require.False(t, found, "a refusal stores no receipt")
	head, err := leader.fleet.CurrentHead(ctx)
	require.NoError(t, err)
	require.Equal(t, report.HeadRevision, head.Revision, "a follower refusal leaves the head unchanged")

	receipt, err := leader.PromoteBaseline(ctx, PromotionRequest{OperationID: "promote-1", ExpectedRevision: report.HeadRevision, Actor: "operator"})
	require.NoError(t, err)
	require.Equal(t, PromotionApplied, receipt.Status, receipt.Refusal)
	require.Equal(t, "promote-1", receipt.OperationID)
	require.Equal(t, report.DeploymentID, receipt.DeploymentID)
	require.Equal(t, PromotionIdentity{GenerationID: report.Retained.GenerationID, Checksum: report.Retained.Checksum, Revision: report.HeadRevision}, receipt.Previous)
	require.Equal(t, report.Packaged.GenerationID, receipt.Promoted.GenerationID)
	require.Equal(t, report.Packaged.Checksum, receipt.Promoted.Checksum)
	require.Greater(t, receipt.Promoted.Revision, receipt.Previous.Revision)
	require.NotNil(t, receipt.InertRemovals)
	require.Empty(t, receipt.InertRemovals)
	require.Empty(t, receipt.Refusal)
	require.Equal(t, "operator", receipt.Actor)
	require.False(t, receipt.CreatedAt.IsZero())

	head, err = leader.fleet.CurrentHead(ctx)
	require.NoError(t, err)
	require.Equal(t, receipt.Promoted.Revision, head.Revision)
	require.Equal(t, receipt.Promoted.GenerationID, head.GenerationID)
	acceptance, _, err := leader.fleet.readAcceptance(ctx)
	require.NoError(t, err)
	require.Equal(t, head, acceptance.Head, "fleet acceptance must select the promoted head before the receipt reports it applied")
	accepted, err := leader.AcceptedStore().Current(ctx)
	require.NoError(t, err)
	require.Equal(t, report.Packaged.GenerationID, accepted.Manifest.GenerationID)
	stored, found, err := leader.fleet.promotionReceipt(ctx, "promote-1")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, receipt, stored)
	encoded, err := json.Marshal(receipt)
	require.NoError(t, err)
	var members map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(encoded, &members))
	require.Len(t, members, 9)
	for _, name := range []string{"operation_id", "deployment_id", "status", "previous", "promoted", "inert_removals", "refusal", "actor", "created_at"} {
		require.Contains(t, members, name)
	}
	require.JSONEq(t, `[]`, string(members["inert_removals"]))

	after, err := leader.BaselineReport()
	require.NoError(t, err)
	require.False(t, after.Promotable)
	require.Equal(t, after.Packaged, after.Retained)
	require.Equal(t, receipt.Promoted.Revision, after.HeadRevision)

	// A new replica replays the promoted baseline from the shared head.
	replica := fleet.open(t)
	replicaReport, err := replica.BaselineReport()
	require.NoError(t, err)
	require.Equal(t, after.Retained, replicaReport.Retained)
}

func TestPromotionExactRetryReturnsOriginalReceipt(t *testing.T) {
	fleet, leader, report := promotableFleet(t)
	ctx := t.Context()
	request := PromotionRequest{OperationID: "retry-1", ExpectedRevision: report.HeadRevision, Actor: "operator"}
	original, err := leader.PromoteBaseline(ctx, request)
	require.NoError(t, err)
	require.Equal(t, PromotionApplied, original.Status, original.Refusal)
	head, err := leader.fleet.CurrentHead(ctx)
	require.NoError(t, err)

	// Without the stored receipt this replica refuses, because its packaged baseline is now retained.
	status, ok := leader.runtime.BaselineStatus()
	require.True(t, ok)
	require.False(t, status.Promotable)
	retried, err := leader.PromoteBaseline(ctx, request)
	require.NoError(t, err)
	require.Equal(t, original, retried)
	request.ExpectedRevision = 0
	retried, err = leader.PromoteBaseline(ctx, request)
	require.NoError(t, err)
	require.Equal(t, original, retried, "a retry without an expected revision matches the deployment and packaged generation")

	// A retry from another process after the leader stops also returns the original receipt.
	require.NoError(t, leader.Close(ctx))
	replacement := fleet.open(t)
	request.ExpectedRevision = original.Previous.Revision
	retried, err = replacement.PromoteBaseline(ctx, request)
	require.NoError(t, err)
	require.Equal(t, original, retried)
	current, err := replacement.fleet.CurrentHead(ctx)
	require.NoError(t, err)
	require.Greater(t, current.Revision, head.Revision, "the replacement owner republished at open")
	require.Equal(t, head.GenerationID, current.GenerationID)
	replacementStatus, ok := replacement.runtime.BaselineStatus()
	require.True(t, ok)
	require.Equal(t, current, replacementStatus.Head, "an exact retry publishes nothing")
}

func TestPromotionAcceptsTheHeadBeforeItsOwnTakeover(t *testing.T) {
	fleet, leader, report := promotableFleet(t)
	ctx := t.Context()
	require.NoError(t, leader.Close(ctx))
	// A command process takes the stopped leader's lease and republishes the reviewed head.
	replacement := fleet.open(t)
	head, err := replacement.fleet.CurrentHead(ctx)
	require.NoError(t, err)
	require.Greater(t, head.Revision, report.HeadRevision)
	require.Equal(t, report.Retained.GenerationID, head.GenerationID)

	older, err := replacement.PromoteBaseline(ctx, PromotionRequest{OperationID: "older-1", ExpectedRevision: report.HeadRevision - 1, Actor: "operator"})
	require.NoError(t, err)
	require.Equal(t, PromotionRefused, older.Status, "only the head before this process's own republication matches")
	require.Contains(t, older.Refusal, "not the expected revision")

	request := PromotionRequest{OperationID: "reviewed-1", ExpectedRevision: report.HeadRevision, Actor: "operator"}
	receipt, err := replacement.PromoteBaseline(ctx, request)
	require.NoError(t, err)
	require.Equal(t, PromotionApplied, receipt.Status, receipt.Refusal)
	require.Equal(t, PromotionIdentity{GenerationID: report.Retained.GenerationID, Checksum: report.Retained.Checksum, Revision: report.HeadRevision}, receipt.Previous)
	require.Greater(t, receipt.Promoted.Revision, head.Revision)
	retried, err := replacement.PromoteBaseline(ctx, request)
	require.NoError(t, err)
	require.Equal(t, receipt, retried)
}

func TestPromotionRefusesReusedOperationID(t *testing.T) {
	fleet, leader, report := promotableFleet(t)
	ctx := t.Context()
	_, err := leader.PromoteBaseline(ctx, PromotionRequest{OperationID: "", Actor: "operator"})
	require.True(t, starmaperrors.IsValidationError(err), "an empty operation ID is invalid: %v", err)
	_, err = leader.PromoteBaseline(ctx, PromotionRequest{OperationID: "bad id/with space", Actor: "operator"})
	require.True(t, starmaperrors.IsValidationError(err), "an operation ID outside the key alphabet is invalid: %v", err)

	stale, err := leader.PromoteBaseline(ctx, PromotionRequest{OperationID: "stale-1", ExpectedRevision: report.HeadRevision + 7, Actor: "operator"})
	require.NoError(t, err)
	require.Equal(t, PromotionRefused, stale.Status)
	require.Contains(t, stale.Refusal, "not the expected revision")
	_, found, err := leader.fleet.promotionReceipt(ctx, "stale-1")
	require.NoError(t, err)
	require.False(t, found)

	original, err := leader.PromoteBaseline(ctx, PromotionRequest{OperationID: "reuse-1", ExpectedRevision: report.HeadRevision, Actor: "operator"})
	require.NoError(t, err)
	require.Equal(t, PromotionApplied, original.Status, original.Refusal)
	head, err := leader.fleet.CurrentHead(ctx)
	require.NoError(t, err)

	conflict, err := leader.PromoteBaseline(ctx, PromotionRequest{OperationID: "reuse-1", ExpectedRevision: original.Promoted.Revision, Actor: "operator"})
	require.NoError(t, err)
	require.Equal(t, PromotionRefused, conflict.Status)
	require.Contains(t, conflict.Refusal, "Use a new operation ID")
	require.Equal(t, "reuse-1", conflict.OperationID)

	// An older binary used the same operation ID for its own packaged generation.
	key := leader.fleet.promotionKey("reuse-1")
	current, _, err := fleet.store.ReadWithLifetime(ctx, key, fleetDescriptorMaxBytes)
	require.NoError(t, err)
	older := original
	older.Promoted.GenerationID = "older-binary-generation"
	replaced, err := json.Marshal(older)
	require.NoError(t, err)
	require.NoError(t, fleet.store.CompareAndSwap(ctx, []storage.CompareAndSwapMutation{{Key: key, ExpectedValue: current, NewValue: replaced}}))
	conflict, err = leader.PromoteBaseline(ctx, PromotionRequest{OperationID: "reuse-1", Actor: "operator"})
	require.NoError(t, err)
	require.Equal(t, PromotionRefused, conflict.Status)
	require.Contains(t, conflict.Refusal, "older-binary-generation")
	require.Contains(t, conflict.Refusal, "Use a new operation ID")

	after, err := leader.fleet.CurrentHead(ctx)
	require.NoError(t, err)
	require.Equal(t, head, after, "a reused operation ID changes no head")
	stored, found, err := leader.fleet.promotionReceipt(ctx, "reuse-1")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, older, stored, "a refusal never replaces the stored receipt")
}

func TestPromotionRefusesLocalRuntime(t *testing.T) {
	local, err := openRuntime(t.Context(), storage.NewMockStore(), identityTestSettings(filepath.Join(t.TempDir(), "state"), "", "127.0.0.1:0"), runtimeCollectors{})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, local.Close(context.Background())) })
	_, err = local.PromoteBaseline(t.Context(), PromotionRequest{OperationID: "local-1", Actor: "operator"})
	require.ErrorIs(t, err, ErrBaselinePromotionFleetOnly)
	_, err = local.BaselineReport()
	require.ErrorIs(t, err, ErrBaselinePromotionFleetOnly)
}

func TestPromotionReceiptRequiresTheLiveGrant(t *testing.T) {
	fleet, leader, report := promotableFleet(t)
	ctx := t.Context()
	receipt := PromotionReceipt{OperationID: "fenced-1", DeploymentID: report.DeploymentID, Status: PromotionApplied, InertRemovals: []catalogs.CatalogRemovalTarget{}}
	head, err := leader.fleet.CurrentHead(ctx)
	require.NoError(t, err)
	fleet.store.expire(leader.fleet.prefix + "lease")
	err = leader.fleet.recordPromotion(ctx, head, receipt)
	var conflict *starmaperrors.ConflictError
	require.True(t, errors.As(err, &conflict), "a lapsed grant must refuse the receipt: %v", err)
	_, found, err := leader.fleet.promotionReceipt(ctx, "fenced-1")
	require.NoError(t, err)
	require.False(t, found)
}
