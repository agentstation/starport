package catalog

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/agentstation/starmap"
	starmaperrors "github.com/agentstation/starmap/pkg/errors"
	"github.com/agentstation/starmap/runtime"
	"github.com/stretchr/testify/require"
)

// promotionCommand is the command process. It shares the store and holds no lease.
func (f *memoryFleet) promotionCommand() *PromotionRequests {
	return &PromotionRequests{fleet: f.session()}
}

// heldLease rebuilds the live grant of one leader, as its lease keeper holds it.
func (f *memoryFleet) heldLease(t *testing.T, leader *Runtime) runtime.Lease {
	t.Helper()
	data, _, err := f.store.ReadWithLifetime(t.Context(), leader.fleet.prefix+"lease", 4096)
	require.NoError(t, err)
	var grant fleetGrant
	require.NoError(t, json.Unmarshal(data, &grant))
	require.Equal(t, leader.fleet.session, grant.Session, "the runtime must hold the lease")
	return runtime.Lease{Holder: grant.Holder, SessionID: grant.Session, Epoch: grant.Epoch, Identity: grant.Identity}
}

// leaseKeeper returns the runtime that holds the publication lease.
func (f *memoryFleet) leaseKeeper(t *testing.T, runtimes ...*Runtime) *Runtime {
	t.Helper()
	data, _, err := f.store.ReadWithLifetime(t.Context(), runtimes[0].fleet.prefix+"lease", 4096)
	require.NoError(t, err)
	var grant fleetGrant
	require.NoError(t, json.Unmarshal(data, &grant))
	for _, candidate := range runtimes {
		if candidate.fleet.session == grant.Session {
			return candidate
		}
	}
	require.FailNow(t, "no runtime holds the lease")
	return nil
}

// requestLifetime reads the native lifetime of the shared request record.
func (f *memoryFleet) requestLifetime(t *testing.T, requests *PromotionRequests) time.Duration {
	t.Helper()
	_, lifetime, err := f.store.ReadWithLifetime(t.Context(), requests.fleet.promotionRequestKey(), fleetDescriptorMaxBytes)
	require.NoError(t, err)
	return lifetime
}

func TestPromotionRequestFollowerIgnoresTheRequest(t *testing.T) {
	fleet, leader, report := promotableFleet(t)
	ctx := t.Context()
	follower := fleet.open(t)
	requests := fleet.promotionCommand()
	holder, err := requests.Leader(ctx)
	require.NoError(t, err)
	require.Equal(t, fleet.leaseHolder(t, leader.fleet), holder)

	submitted, err := requests.Submit(ctx, PromotionRequest{OperationID: "request-1", ExpectedRevision: report.HeadRevision, PackagedGenerationID: report.Packaged.GenerationID, Actor: "operator"})
	require.NoError(t, err)
	require.Equal(t, PromotionPending, submitted.Status)
	require.Nil(t, submitted.Receipt)

	require.NoError(t, follower.executePromotionRequest(ctx))
	pending, found, err := requests.Read(ctx, "request-1")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, PromotionPending, pending.Status, "a follower never settles the request")
	_, found, err = follower.fleet.promotionReceipt(ctx, "request-1")
	require.NoError(t, err)
	require.False(t, found, "a follower stores no receipt")
	head, err := leader.fleet.CurrentHead(ctx)
	require.NoError(t, err)
	require.Equal(t, report.HeadRevision, head.Revision, "a follower publishes nothing")

	require.NoError(t, leader.executePromotionRequest(ctx))
	settled, found, err := requests.Read(ctx, "request-1")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, PromotionApplied, settled.Status)
	require.NotNil(t, settled.Receipt)
	require.Equal(t, PromotionApplied, settled.Receipt.Status, settled.Receipt.Refusal)
	require.Equal(t, report.Packaged.GenerationID, settled.Receipt.Promoted.GenerationID)
	require.Equal(t, "operator", settled.Receipt.Actor)
	stored, found, err := leader.fleet.promotionReceipt(ctx, "request-1")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, stored, *settled.Receipt, "the outcome and the durable receipt come from one transaction")
	head, err = leader.fleet.CurrentHead(ctx)
	require.NoError(t, err)
	require.Equal(t, settled.Receipt.Promoted.Revision, head.Revision)

	// A settled request is not executed again.
	require.NoError(t, leader.executePromotionRequest(ctx))
	after, err := leader.fleet.CurrentHead(ctx)
	require.NoError(t, err)
	require.Equal(t, head, after)
}

func TestPromotionRequestOldBinaryLeaderRefuses(t *testing.T) {
	fleet, leader, report := promotableFleet(t)
	ctx := t.Context()
	requests := fleet.promotionCommand()
	_, err := requests.Submit(ctx, PromotionRequest{OperationID: "upgrade-1", PackagedGenerationID: "newer-binary-generation", Actor: "operator"})
	require.NoError(t, err)

	require.NoError(t, leader.executePromotionRequest(ctx))
	settled, found, err := requests.Read(ctx, "upgrade-1")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, PromotionRefused, settled.Status)
	require.Contains(t, settled.Receipt.Refusal, report.Packaged.GenerationID, "the refusal names the generation of the leader")
	require.Contains(t, settled.Receipt.Refusal, "newer-binary-generation", "the refusal names the generation of the request")
	require.Contains(t, settled.Receipt.Refusal, "Finish the gateway upgrade")
	_, found, err = leader.fleet.promotionReceipt(ctx, "upgrade-1")
	require.NoError(t, err)
	require.False(t, found, "a refused receipt stays only in the request record")
	head, err := leader.fleet.CurrentHead(ctx)
	require.NoError(t, err)
	require.Equal(t, report.HeadRevision, head.Revision)
	require.Equal(t, promotionOutcomeLifetime, fleet.requestLifetime(t, requests).Round(time.Minute))

	// A refused outcome is not durable. The same operation ID records the request again for a new evaluation.
	again, err := requests.Submit(ctx, PromotionRequest{OperationID: "upgrade-1", PackagedGenerationID: report.Packaged.GenerationID, Actor: "operator"})
	require.NoError(t, err)
	require.Equal(t, PromotionPending, again.Status)
	require.Equal(t, PromotionRequestLifetime, fleet.requestLifetime(t, requests).Round(time.Minute))
	require.NoError(t, leader.executePromotionRequest(ctx))
	applied, found, err := requests.Read(ctx, "upgrade-1")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, PromotionApplied, applied.Status, applied.Receipt.Refusal)
}

func TestPromotionRequestRefusesAnotherPendingOperation(t *testing.T) {
	fleet, leader, report := promotableFleet(t)
	ctx := t.Context()
	requests := fleet.promotionCommand()
	request := PromotionRequest{OperationID: "first-1", ExpectedRevision: report.HeadRevision, PackagedGenerationID: report.Packaged.GenerationID, Actor: "operator"}
	first, err := requests.Submit(ctx, request)
	require.NoError(t, err)

	other := fleet.promotionCommand()
	_, err = other.Submit(ctx, PromotionRequest{OperationID: "second-1", PackagedGenerationID: report.Packaged.GenerationID, Actor: "operator"})
	require.True(t, starmaperrors.IsConflict(err), "another pending operation refuses the write: %v", err)
	require.ErrorContains(t, err, "promotion request first-1 is pending until")
	_, found, err := requests.Read(ctx, "second-1")
	require.NoError(t, err)
	require.False(t, found)

	// The same operation ID continues the wait on the original record.
	again, err := other.Submit(ctx, request)
	require.NoError(t, err)
	require.Equal(t, first.RequestedAt, again.RequestedAt)
	require.Equal(t, PromotionPending, again.Status)

	// An invalid request writes nothing.
	_, err = other.Submit(ctx, PromotionRequest{OperationID: "bad id/with space", PackagedGenerationID: report.Packaged.GenerationID})
	require.True(t, starmaperrors.IsValidationError(err), "an operation ID outside the key alphabet is invalid: %v", err)
	_, err = other.Submit(ctx, PromotionRequest{OperationID: "third-1"})
	require.ErrorContains(t, err, "packaged generation")

	// A settled outcome does not block the next operation.
	require.NoError(t, leader.executePromotionRequest(ctx))
	next, err := other.Submit(ctx, PromotionRequest{OperationID: "second-1", PackagedGenerationID: report.Packaged.GenerationID, Actor: "operator"})
	require.NoError(t, err)
	require.Equal(t, PromotionPending, next.Status)
	_, found, err = requests.Read(ctx, "first-1")
	require.NoError(t, err)
	require.False(t, found, "the next request replaces the settled outcome")
}

func TestPromotionRequestLifetime(t *testing.T) {
	fleet, leader, report := promotableFleet(t)
	ctx := t.Context()
	requests := fleet.promotionCommand()
	request := PromotionRequest{OperationID: "lifetime-1", ExpectedRevision: report.HeadRevision, PackagedGenerationID: report.Packaged.GenerationID, Actor: "operator"}
	submitted, err := requests.Submit(ctx, request)
	require.NoError(t, err)
	require.Equal(t, PromotionRequestLifetime, fleet.requestLifetime(t, requests).Round(time.Minute))
	require.WithinDuration(t, time.Now().Add(PromotionRequestLifetime), submitted.Expires, time.Minute)

	// A request that no leader settles expires, and the same operation ID writes it again.
	fleet.store.expire(requests.fleet.promotionRequestKey())
	_, found, err := requests.Read(ctx, "lifetime-1")
	require.NoError(t, err)
	require.False(t, found)
	_, err = requests.Submit(ctx, request)
	require.NoError(t, err)

	require.NoError(t, leader.executePromotionRequest(ctx))
	settled, found, err := requests.Read(ctx, "lifetime-1")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, PromotionApplied, settled.Status, settled.Receipt.Refusal)
	require.Equal(t, promotionOutcomeLifetime, fleet.requestLifetime(t, requests).Round(time.Minute))
	retried, err := requests.Submit(ctx, request)
	require.NoError(t, err)
	require.Equal(t, settled.Receipt, retried.Receipt, "an exact retry within the outcome lifetime returns the outcome")

	// After the outcome expires, the same operation ID returns the durable receipt.
	fleet.store.expire(requests.fleet.promotionRequestKey())
	_, err = requests.Submit(ctx, request)
	require.NoError(t, err)
	require.NoError(t, leader.executePromotionRequest(ctx))
	again, found, err := requests.Read(ctx, "lifetime-1")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, settled.Receipt, again.Receipt)
}

func TestLeaseRenewalSignalsPromotionWithoutExecuting(t *testing.T) {
	fleet, leader, report := promotableFleet(t)
	ctx := t.Context()
	lease := fleet.heldLease(t, leader)
	_, err := leader.fleet.Renew(ctx, lease, time.Minute)
	require.NoError(t, err)
	require.Empty(t, leader.fleet.promotions, "renewal without a pending request sends no signal")

	requests := fleet.promotionCommand()
	_, err = requests.Submit(ctx, PromotionRequest{OperationID: "renew-1", ExpectedRevision: report.HeadRevision, PackagedGenerationID: report.Packaged.GenerationID, Actor: "operator"})
	require.NoError(t, err)
	_, err = leader.fleet.Renew(ctx, lease, time.Minute)
	require.NoError(t, err)
	require.Len(t, leader.fleet.promotions, 1, "renewal signals the pending request")
	// A full signal channel drops the signal. Renewal never blocks, and the next renewal reads the record again.
	_, err = leader.fleet.Renew(ctx, lease, time.Minute)
	require.NoError(t, err)
	require.Len(t, leader.fleet.promotions, 1, "a full channel keeps one signal")
	record, _, err := requests.Read(ctx, "renew-1")
	require.NoError(t, err)
	require.Equal(t, PromotionPending, record.Status, "renewal never executes the promotion")
	head, err := leader.fleet.CurrentHead(ctx)
	require.NoError(t, err)
	require.Equal(t, report.HeadRevision, head.Revision)
}

func TestStartedLeaderExecutesTheRecordedRequest(t *testing.T) {
	fleet, leader, report := promotableFleet(t)
	ctx := t.Context()
	generation, err := starmap.EmbeddedGeneration()
	require.NoError(t, err)
	require.Equal(t, generation.Manifest.GenerationID, report.Packaged.GenerationID, "the command derives the packaged generation that the leader reports")
	require.NoError(t, leader.Start(ctx))
	requests := fleet.promotionCommand()
	_, err = requests.Submit(ctx, PromotionRequest{OperationID: "started-1", ExpectedRevision: report.HeadRevision, PackagedGenerationID: report.Packaged.GenerationID, Actor: "operator"})
	require.NoError(t, err)
	_, err = leader.fleet.Renew(ctx, fleet.heldLease(t, leader), time.Minute)
	require.NoError(t, err)

	var settled PromotionRecord
	require.Eventually(t, func() bool {
		record, found, err := requests.Read(ctx, "started-1")
		settled = record
		return err == nil && found && record.Status != PromotionPending
	}, 3*time.Minute, 50*time.Millisecond)
	require.Equal(t, PromotionApplied, settled.Status, settled.Receipt.Refusal)
	head, err := leader.fleet.CurrentHead(ctx)
	require.NoError(t, err)
	require.Equal(t, settled.Receipt.Promoted.Revision, head.Revision)
	after, err := leader.BaselineReport()
	require.NoError(t, err)
	require.Equal(t, after.Packaged, after.Retained)
}

func TestPromotionRequestLeaseLossLeavesTheRequest(t *testing.T) {
	fleet, leader, report := promotableFleet(t)
	ctx := t.Context()
	requests := fleet.promotionCommand()
	request := PromotionRequest{OperationID: "lost-1", ExpectedRevision: report.HeadRevision, PackagedGenerationID: report.Packaged.GenerationID, Actor: "operator"}
	_, err := requests.Submit(ctx, request)
	require.NoError(t, err)
	_, err = leader.fleet.Renew(ctx, fleet.heldLease(t, leader), time.Minute)
	require.NoError(t, err)
	require.Len(t, leader.fleet.promotions, 1, "the leader has the signal before the run")
	_, pending, err := leader.fleet.readPromotionRequest(ctx)
	require.NoError(t, err)

	// The lease ends while the promoted head commit is in flight.
	fleet.store.loseLeaseBefore(leader.fleet.prefix+"head-initialized", leader.fleet.prefix+"lease")
	err = leader.executePromotionRequest(ctx)
	require.True(t, starmaperrors.IsConflict(err), "a leader without the lease gets the typed refusal: %v", err)
	require.ErrorContains(t, err, "The request stays pending for the next leader")
	_, after, err := leader.fleet.readPromotionRequest(ctx)
	require.NoError(t, err)
	require.Equal(t, pending, after, "the request record stays untouched for the next leader")
	_, found, err := leader.fleet.promotionReceipt(ctx, "lost-1")
	require.NoError(t, err)
	require.False(t, found, "a lost lease stores no receipt")
	head, err := leader.fleet.CurrentHead(ctx)
	require.NoError(t, err)
	require.Equal(t, report.HeadRevision, head.Revision, "a lost lease publishes no head")

	// The next leader picks up the same request.
	next := fleet.open(t)
	require.NoError(t, fleet.leaseKeeper(t, leader, next).executePromotionRequest(ctx))
	settled, found, err := requests.Read(ctx, "lost-1")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, PromotionApplied, settled.Status, settled.Receipt.Refusal)
}
