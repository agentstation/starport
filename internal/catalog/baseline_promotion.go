package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/agentstation/starmap/pkg/catalogs"
	starmaperrors "github.com/agentstation/starmap/pkg/errors"
	"github.com/agentstation/starmap/runtime"

	"github.com/agentstation/starport/internal/storage"
)

// ErrBaselinePromotionFleetOnly refuses baseline promotion outside fleet mode.
// A local deployment retains no baseline apart from its binary.
var ErrBaselinePromotionFleetOnly = errors.New("baseline promotion is fleet-only. A local deployment serves the baseline of its binary")

// PromotionStatus names the outcome of one baseline promotion.
type PromotionStatus string

const (
	// PromotionApplied means the fleet accepted the promoted baseline.
	PromotionApplied PromotionStatus = "applied"
	// PromotionRefused means the fleet head and its baseline did not change.
	PromotionRefused PromotionStatus = "refused"
)

// maxPromotionOperationID bounds the operator-supplied operation ID.
const maxPromotionOperationID = 128

// PromotionIdentity identifies one baseline at one fleet head revision.
type PromotionIdentity struct {
	GenerationID string `json:"generation_id"`
	Checksum     string `json:"checksum"`
	Revision     uint64 `json:"revision"`
}

// PromotionReceipt records one baseline promotion. It carries no credential.
// The fleet stores an applied receipt under its operation ID.
type PromotionReceipt struct {
	OperationID   string                          `json:"operation_id"`
	DeploymentID  string                          `json:"deployment_id"`
	Status        PromotionStatus                 `json:"status"`
	Previous      PromotionIdentity               `json:"previous"`
	Promoted      PromotionIdentity               `json:"promoted"`
	InertRemovals []catalogs.CatalogRemovalTarget `json:"inert_removals"`
	Refusal       string                          `json:"refusal"`
	Actor         string                          `json:"actor"`
	CreatedAt     time.Time                       `json:"created_at"`
}

// PromotionRequest selects the packaged baseline of this binary for the fleet.
type PromotionRequest struct {
	// OperationID is the stable operator ID that makes a retry exact.
	OperationID string
	// ExpectedRevision is the fleet head revision that the operator reviewed.
	// Zero accepts the head that this replica observes. The head before this
	// process republished the same generation as it took the lease also matches.
	ExpectedRevision uint64
	// PackagedGenerationID names the packaged baseline of the binary that wrote the request.
	// The leader promotes only its own packaged generation. Empty selects the packaged
	// generation of this replica.
	PackagedGenerationID string
	// Actor names the operator for the receipt.
	Actor string
}

// BaselineIdentity identifies one catalog baseline.
type BaselineIdentity struct {
	GenerationID string `json:"generation_id"`
	Checksum     string `json:"checksum"`
}

// BaselineReport compares the packaged baseline with the retained fleet baseline.
// It comes from memory and carries no credential.
type BaselineReport struct {
	DeploymentID string           `json:"deployment_id"`
	Packaged     BaselineIdentity `json:"packaged"`
	Retained     BaselineIdentity `json:"retained"`
	HeadRevision uint64           `json:"head_revision"`
	Promotable   bool             `json:"promotable"`
	Refusal      string           `json:"refusal,omitempty"`
}

// ValidatePromotionOperationID accepts 1 to 128 letters, digits, and the characters ".", "_", ":", and "-".
func ValidatePromotionOperationID(id string) error {
	if id == "" || len(id) > maxPromotionOperationID {
		return &starmaperrors.ValidationError{Field: "operation_id", Message: "must have 1 to 128 characters"}
	}
	for _, c := range id {
		if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (c < '0' || c > '9') && c != '.' && c != '_' && c != ':' && c != '-' {
			return &starmaperrors.ValidationError{Field: "operation_id", Message: "may contain only letters, digits, '.', '_', ':', and '-'"}
		}
	}
	return nil
}

// BaselineReport reads the packaged and retained baselines from memory.
func (r *Runtime) BaselineReport() (BaselineReport, error) {
	if r == nil || r.runtime == nil || r.fleet == nil {
		return BaselineReport{}, ErrBaselinePromotionFleetOnly
	}
	status, ok := r.runtime.BaselineStatus()
	if !ok {
		return BaselineReport{}, ErrBaselinePromotionFleetOnly
	}
	return BaselineReport{
		DeploymentID: r.fleet.identity.DeploymentID,
		Packaged:     baselineIdentity(status.Packaged),
		Retained:     baselineIdentity(status.Retained),
		HeadRevision: status.Head.Revision,
		Promotable:   status.Promotable,
		Refusal:      status.Refusal,
	}, nil
}

// PromoteBaseline makes the packaged baseline of this binary the retained fleet baseline.
// Only the publication lease holder promotes. The promoted head passes fleet acceptance
// before the receipt reports it applied. An exact retry returns the stored receipt
// without a promotion. A refusal returns a receipt that the fleet does not store.
// The leader calls it for a recorded request. See executePromotionRequest.
func (r *Runtime) PromoteBaseline(ctx context.Context, request PromotionRequest) (PromotionReceipt, error) {
	receipt, _, err := r.promoteBaseline(ctx, request, nil, PromotionRecord{})
	return receipt, err
}

// promoteBaseline runs one promotion. With the bytes of a pending request, the
// applied receipt and the outcome that replaces the request share one native
// transaction, and settled reports that the outcome is stored.
func (r *Runtime) promoteBaseline(ctx context.Context, request PromotionRequest, pending []byte, record PromotionRecord) (receipt PromotionReceipt, settled bool, err error) {
	if err := ValidatePromotionOperationID(request.OperationID); err != nil {
		return PromotionReceipt{}, false, err
	}
	if r == nil || r.runtime == nil || r.fleet == nil {
		return PromotionReceipt{}, false, ErrBaselinePromotionFleetOnly
	}
	status, ok := r.runtime.BaselineStatus()
	if !ok {
		return PromotionReceipt{}, false, ErrBaselinePromotionFleetOnly
	}
	refused := func(message string) PromotionReceipt {
		return PromotionReceipt{
			OperationID: request.OperationID, DeploymentID: r.fleet.identity.DeploymentID, Status: PromotionRefused,
			Previous:      promotionIdentity(status.Retained, status.Head.Revision),
			Promoted:      promotionIdentity(status.Packaged, 0),
			InertRemovals: []catalogs.CatalogRemovalTarget{}, Refusal: message, Actor: request.Actor, CreatedAt: time.Now().UTC(),
		}
	}
	// An old binary cannot promote the request of a new binary, or the reverse.
	if request.PackagedGenerationID != "" && request.PackagedGenerationID != status.Packaged.GenerationID {
		return refused(fmt.Sprintf("the leader runs packaged generation %s, and the request names packaged generation %s. A leader promotes only its own packaged generation. Finish the gateway upgrade, or run the command with the binary of the leader",
			status.Packaged.GenerationID, request.PackagedGenerationID)), false, nil
	}
	stored, found, err := r.fleet.promotionReceipt(ctx, request.OperationID)
	if err != nil {
		return PromotionReceipt{}, false, err
	}
	if found {
		if conflict := stored.bindingConflict(r.fleet.identity.DeploymentID, status.Packaged.GenerationID, request.ExpectedRevision); conflict != "" {
			return refused(conflict), false, nil
		}
		return stored, false, nil
	}
	previous := status.Head.Revision
	if request.ExpectedRevision != 0 && request.ExpectedRevision != status.Head.Revision {
		republished, err := r.fleet.republishedRevision(ctx, status.Head)
		if err != nil {
			return PromotionReceipt{}, false, err
		}
		if republished != request.ExpectedRevision {
			return refused(fmt.Sprintf("the fleet head is at revision %d, not the expected revision %d", status.Head.Revision, request.ExpectedRevision)), false, nil
		}
		previous = republished
	}
	holder, foreign, err := r.fleet.promotionLeader(ctx)
	if err != nil {
		return PromotionReceipt{}, false, err
	}
	if foreign {
		return refused(fmt.Sprintf("the fleet leader %q holds the publication lease. Only the leader promotes. The leader executes a recorded request at its next lease renewal. Retry with the same operation ID", holder)), false, nil
	}
	if !status.Promotable {
		return refused(status.Refusal), false, nil
	}
	result, err := r.runtime.PromoteEmbeddedBaseline(ctx, runtime.BaselinePromotion{ExpectedHead: status.Head, PackagedGenerationID: status.Packaged.GenerationID})
	if err != nil {
		if starmaperrors.IsConflict(err) {
			return refused(err.Error()), false, nil
		}
		return PromotionReceipt{}, false, err
	}
	if result.Head.Revision <= status.Head.Revision {
		return PromotionReceipt{}, false, fleetStoreConflict("the promoted fleet head did not advance its revision")
	}
	if err := r.acceptPromotedHead(ctx, result.Head); err != nil {
		return PromotionReceipt{}, false, fmt.Errorf("accept promoted fleet head %d: %w", result.Head.Revision, err)
	}
	receipt = PromotionReceipt{
		OperationID: request.OperationID, DeploymentID: r.fleet.identity.DeploymentID, Status: PromotionApplied,
		Previous:      promotionIdentity(result.Previous, previous),
		Promoted:      promotionIdentity(result.Promoted, result.Head.Revision),
		InertRemovals: append([]catalogs.CatalogRemovalTarget{}, result.InertRemovals...),
		Actor:         request.Actor, CreatedAt: time.Now().UTC(),
	}
	if err := r.fleet.recordPromotion(ctx, result.Head, receipt, pending, record); err != nil {
		return PromotionReceipt{}, false, fmt.Errorf("record the receipt of accepted fleet head %d: %w", result.Head.Revision, err)
	}
	return receipt, pending != nil, nil
}

// acceptPromotedHead runs the fleet acceptance that every replica replays.
func (r *Runtime) acceptPromotedHead(ctx context.Context, head runtime.FleetHead) error {
	candidate, err := r.CurrentCandidate(ctx)
	if err != nil {
		return err
	}
	if candidate.FleetHead != head {
		return fleetStoreConflict("the fleet head changed after promotion")
	}
	return r.Accept(ctx, candidate)
}

// bindingConflict reports why a stored receipt does not answer this request.
// Without an expected revision, a retry matches on the deployment and the packaged generation.
func (p PromotionReceipt) bindingConflict(deployment, packaged string, expected uint64) string {
	switch {
	case p.DeploymentID != deployment:
		return "operation ID " + p.OperationID + " belongs to deployment " + p.DeploymentID + ". Use a new operation ID"
	case p.Promoted.GenerationID != packaged:
		return "operation ID " + p.OperationID + " promoted packaged generation " + p.Promoted.GenerationID + ", not " + packaged + ". Use a new operation ID"
	case expected != 0 && expected != p.Previous.Revision:
		return "operation ID " + p.OperationID + " expected revision " + strconv.FormatUint(p.Previous.Revision, 10) + ", not " + strconv.FormatUint(expected, 10) + ". Use a new operation ID"
	}
	return ""
}

func baselineIdentity(identity catalogs.GenerationIdentity) BaselineIdentity {
	return BaselineIdentity{GenerationID: identity.GenerationID, Checksum: identity.PayloadChecksum}
}

func promotionIdentity(identity catalogs.GenerationIdentity, revision uint64) PromotionIdentity {
	return PromotionIdentity{GenerationID: identity.GenerationID, Checksum: identity.PayloadChecksum, Revision: revision}
}

// promotionKey shares the hash tag of the fleet keys, so one native transaction
// checks the publication lease and creates the receipt.
func (s *FleetStore) promotionKey(operationID string) string {
	return "catalog:promotion:{" + payloadDigest([]byte(s.identity.DeploymentID)) + "}:v1:" + operationID
}

// promotionReceipt reads the applied receipt of one operation ID.
func (s *FleetStore) promotionReceipt(ctx context.Context, operationID string) (PromotionReceipt, bool, error) {
	if err := s.checkApproval(ctx); err != nil {
		return PromotionReceipt{}, false, err
	}
	data, _, err := s.store.ReadWithLifetime(ctx, s.promotionKey(operationID), fleetDescriptorMaxBytes)
	if errors.Is(err, storage.ErrNotFound) {
		return PromotionReceipt{}, false, nil
	}
	if err != nil {
		return PromotionReceipt{}, false, err
	}
	var receipt PromotionReceipt
	if err := json.Unmarshal(data, &receipt); err != nil || receipt.OperationID != operationID || receipt.Status != PromotionApplied {
		return PromotionReceipt{}, false, fleetStoreConflict("the stored promotion receipt is invalid")
	}
	return receipt, true, nil
}

// republishedRevision returns the revision of the predecessor head when this process
// published the given head as a republication of the same generation. A process that
// takes the lease republishes the retained generation at open. That republication
// changes no baseline, so the predecessor is the head that the operator reviewed.
// Zero means the head is not such a republication.
func (s *FleetStore) republishedRevision(ctx context.Context, head runtime.FleetHead) (uint64, error) {
	snapshot, err := s.Publication(ctx, head)
	if err != nil {
		return 0, err
	}
	publication := snapshot.Publication
	if publication.Grant.SessionID != s.session || publication.Expected.GenerationID != head.GenerationID {
		return 0, nil
	}
	return publication.Expected.Revision, nil
}

// promotionLeader names the current lease holder and reports whether another process holds it.
func (s *FleetStore) promotionLeader(ctx context.Context) (string, bool, error) {
	data, _, err := s.store.ReadWithLifetime(ctx, s.prefix+"lease", 4096)
	if errors.Is(err, storage.ErrNotFound) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	var held fleetGrant
	if err := json.Unmarshal(data, &held); err != nil {
		return "", false, err
	}
	return held.Holder, held.Session != s.session, nil
}

// recordPromotion creates the receipt under the live grant that committed the promoted head.
// With the bytes of a pending request, the same transaction replaces the request with its outcome.
// A lost or replaced grant, or a changed request, refuses the write.
func (s *FleetStore) recordPromotion(ctx context.Context, head runtime.FleetHead, receipt PromotionReceipt, pending []byte, record PromotionRecord) error {
	encoded, err := json.Marshal(receipt)
	if err != nil {
		return err
	}
	if len(encoded) > fleetDescriptorMaxBytes {
		return errors.New("the promotion receipt exceeds its byte bound")
	}
	snapshot, err := s.Publication(ctx, head)
	if err != nil {
		return err
	}
	grant, err := encodeFleetGrant(snapshot.Publication.Grant)
	if err != nil {
		return err
	}
	mutations := []storage.CompareAndSwapMutation{
		{Key: s.prefix + "lease", ExpectedValue: grant, NewValue: grant},
		{Key: s.promotionKey(receipt.OperationID), NewValue: encoded},
	}
	if pending != nil {
		outcome, err := promotionOutcome(s.promotionRequestKey(), pending, record, receipt)
		if err != nil {
			return err
		}
		mutations = append(mutations, outcome)
	}
	err = s.store.CompareAndSwap(ctx, mutations, s.prefix+"lease")
	if errors.Is(err, storage.ErrConflict) {
		return fleetStoreConflict("the publication lease ended, the operation ID gained a receipt, or the request changed before the write")
	}
	return err
}
