package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/agentstation/starport/internal/sqlstore"
	"github.com/agentstation/starport/internal/storage"
)

const (
	// PromotionRequestLifetime bounds a pending request. A request that no
	// leader settles expires, and the same operation ID can write it again.
	PromotionRequestLifetime = 10 * time.Minute
	// promotionOutcomeLifetime keeps a settled outcome for the waiting command.
	// The durable applied receipt stays under its own key.
	promotionOutcomeLifetime = 5 * time.Minute
	// promotionRunTimeout bounds one leader execution of a request.
	promotionRunTimeout = 2 * time.Minute
)

// PromotionPending means that no leader settled the request yet.
const PromotionPending PromotionStatus = "pending"

// PromotionRecord is the one shared promotion request of a deployment.
// The command writes it pending. The leader replaces it with the outcome.
type PromotionRecord struct {
	OperationID          string            `json:"operation_id"`
	ExpectedRevision     uint64            `json:"expected_revision"`
	PackagedGenerationID string            `json:"packaged_generation_id"`
	Actor                string            `json:"actor"`
	RequestedAt          time.Time         `json:"requested_at"`
	Status               PromotionStatus   `json:"status"`
	Receipt              *PromotionReceipt `json:"receipt,omitempty"`
	// Expires is the end of the record lifetime that the read observed.
	Expires time.Time `json:"-"`
}

func (p PromotionRecord) request() PromotionRequest {
	return PromotionRequest{OperationID: p.OperationID, ExpectedRevision: p.ExpectedRevision, PackagedGenerationID: p.PackagedGenerationID, Actor: p.Actor}
}

// valid checks the record shape. A settled record carries the receipt of its operation.
func (p PromotionRecord) valid() bool {
	if ValidatePromotionOperationID(p.OperationID) != nil || p.PackagedGenerationID == "" {
		return false
	}
	switch p.Status {
	case PromotionPending:
		return p.Receipt == nil
	case PromotionApplied, PromotionRefused:
		return p.Receipt != nil && p.Receipt.Status == p.Status && p.Receipt.OperationID == p.OperationID
	}
	return false
}

// promotionRequestKey shares the hash tag of the fleet keys, so one native
// transaction checks the publication lease, creates the receipt, and settles the request.
func (s *FleetStore) promotionRequestKey() string {
	return "catalog:promotion-request:{" + payloadDigest([]byte(s.identity.DeploymentID)) + "}:v1"
}

// readPromotionRequest returns the current record and its stored bytes.
func (s *FleetStore) readPromotionRequest(ctx context.Context) (PromotionRecord, []byte, error) {
	data, lifetime, err := s.store.ReadWithLifetime(ctx, s.promotionRequestKey(), fleetDescriptorMaxBytes)
	if err != nil {
		return PromotionRecord{}, nil, err
	}
	var record PromotionRecord
	if err := json.Unmarshal(data, &record); err != nil || !record.valid() {
		return PromotionRecord{}, nil, fleetStoreConflict("the stored promotion request is invalid")
	}
	record.Expires = time.Now().Add(lifetime).UTC()
	return record, data, nil
}

// submitPromotion writes one pending request. A pending request with another
// operation ID refuses the write. The same operation ID returns the current
// record, so a repeated command continues the wait. A refused outcome is not
// durable, so the same operation ID replaces it and the leader evaluates the request again.
func (s *FleetStore) submitPromotion(ctx context.Context, request PromotionRequest) (PromotionRecord, error) {
	if err := ValidatePromotionOperationID(request.OperationID); err != nil {
		return PromotionRecord{}, err
	}
	if request.PackagedGenerationID == "" {
		return PromotionRecord{}, errors.New("the promotion request requires the packaged generation of this binary")
	}
	if err := s.checkApproval(ctx); err != nil {
		return PromotionRecord{}, err
	}
	current, previous, err := s.readPromotionRequest(ctx)
	if err != nil && !errors.Is(err, storage.ErrNotFound) {
		return PromotionRecord{}, err
	}
	if previous != nil {
		same := current.OperationID == request.OperationID
		if same && current.Status != PromotionRefused {
			return current, nil
		}
		if !same && current.Status == PromotionPending {
			return PromotionRecord{}, fleetStoreConflict(fmt.Sprintf("promotion request %s is pending until %s. Wait for it with its operation ID, or retry after its lifetime ends",
				current.OperationID, current.Expires.Format(time.RFC3339)))
		}
	}
	record := PromotionRecord{
		OperationID: request.OperationID, ExpectedRevision: request.ExpectedRevision, PackagedGenerationID: request.PackagedGenerationID,
		Actor: request.Actor, RequestedAt: time.Now().UTC(), Status: PromotionPending,
	}
	encoded, err := json.Marshal(record)
	if err != nil {
		return PromotionRecord{}, err
	}
	err = s.store.CompareAndSwap(ctx, []storage.CompareAndSwapMutation{
		{Key: s.promotionRequestKey(), ExpectedValue: previous, NewValue: encoded, TTL: PromotionRequestLifetime},
	})
	if errors.Is(err, storage.ErrConflict) {
		return PromotionRecord{}, fleetStoreConflict("another command changed the promotion request. Retry with the same operation ID")
	}
	if err != nil {
		return PromotionRecord{}, err
	}
	record.Expires = time.Now().Add(PromotionRequestLifetime).UTC()
	return record, nil
}

// settlePromotion replaces the pending request with its outcome. The outcome
// of a fresh applied promotion is written by recordPromotion instead.
func (s *FleetStore) settlePromotion(ctx context.Context, pending []byte, record PromotionRecord, receipt PromotionReceipt) error {
	mutation, err := promotionOutcome(s.promotionRequestKey(), pending, record, receipt)
	if err != nil {
		return err
	}
	err = s.store.CompareAndSwap(ctx, []storage.CompareAndSwapMutation{mutation})
	if errors.Is(err, storage.ErrConflict) {
		return fleetStoreConflict("the promotion request changed before its outcome")
	}
	return err
}

func promotionOutcome(key string, pending []byte, record PromotionRecord, receipt PromotionReceipt) (storage.CompareAndSwapMutation, error) {
	record.Status, record.Receipt = receipt.Status, &receipt
	encoded, err := json.Marshal(record)
	if err != nil {
		return storage.CompareAndSwapMutation{}, err
	}
	if len(encoded) > fleetDescriptorMaxBytes {
		return storage.CompareAndSwapMutation{}, errors.New("the promotion outcome exceeds its byte bound")
	}
	return storage.CompareAndSwapMutation{Key: key, ExpectedValue: pending, NewValue: encoded, TTL: promotionOutcomeLifetime}, nil
}

// signalPromotion wakes the leader executor when a request is pending. Lease
// renewal calls it after a renewal succeeds. A failed read sends no signal,
// and the next renewal reads again.
func (s *FleetStore) signalPromotion(ctx context.Context) {
	record, _, err := s.readPromotionRequest(ctx)
	if err != nil || record.Status != PromotionPending {
		return
	}
	select {
	case s.promotions <- struct{}{}:
	default:
	}
}

// executePromotions runs pending requests on this replica while it holds the lease.
func (r *Runtime) executePromotions(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-r.fleet.promotions:
			runCtx, cancel := context.WithTimeout(ctx, promotionRunTimeout)
			// A failed execution leaves the request pending. The next renewal signals it again.
			_ = r.executePromotionRequest(runCtx)
			cancel()
		}
	}
}

// executePromotionRequest runs the pending request under the publication lease of this replica.
// A follower, or a replica without the lease, leaves the request for the leader.
// The outcome replaces the request.
func (r *Runtime) executePromotionRequest(ctx context.Context) error {
	if r == nil || r.runtime == nil || r.fleet == nil {
		return ErrBaselinePromotionFleetOnly
	}
	record, pending, err := r.fleet.readPromotionRequest(ctx)
	if errors.Is(err, storage.ErrNotFound) {
		return nil
	}
	if err != nil || record.Status != PromotionPending {
		return err
	}
	if holder, foreign, err := r.fleet.promotionLeader(ctx); err != nil || foreign || holder == "" {
		return err
	}
	receipt, settled, err := r.promoteBaseline(ctx, record.request(), pending, record)
	if err != nil || settled {
		return err
	}
	return r.fleet.settlePromotion(ctx, pending, record, receipt)
}

// PromotionRequests writes and reads the shared promotion request of one deployment.
// It never takes the publication lease and never opens a gateway state directory.
type PromotionRequests struct {
	fleet *FleetStore
}

// OpenPromotionRequests opens the request record of a fleet deployment.
// A deployment without shared catalog storage refuses.
func OpenPromotionRequests(ctx context.Context, store storage.KVStore, db *sqlstore.DB, deployment string) (*PromotionRequests, error) {
	fleet, err := openRecoveryFleet(ctx, store, db, deployment)
	if err != nil {
		return nil, err
	}
	if fleet == nil {
		return nil, ErrBaselinePromotionFleetOnly
	}
	return &PromotionRequests{fleet: fleet}, nil
}

// Submit writes one pending request, or returns the record of the same operation ID.
func (p *PromotionRequests) Submit(ctx context.Context, request PromotionRequest) (PromotionRecord, error) {
	return p.fleet.submitPromotion(ctx, request)
}

// Read returns the record when it belongs to the operation ID.
// It reports false when the record expired or another operation replaced it.
func (p *PromotionRequests) Read(ctx context.Context, operationID string) (PromotionRecord, bool, error) {
	record, _, err := p.fleet.readPromotionRequest(ctx)
	if errors.Is(err, storage.ErrNotFound) {
		return PromotionRecord{}, false, nil
	}
	if err != nil {
		return PromotionRecord{}, false, err
	}
	return record, record.OperationID == operationID, nil
}

// Leader names the current publication lease holder. It is empty when no gateway leads.
func (p *PromotionRequests) Leader(ctx context.Context) (string, error) {
	if err := p.fleet.checkApproval(ctx); err != nil {
		return "", err
	}
	holder, _, err := p.fleet.promotionLeader(ctx)
	return strings.TrimSpace(holder), err
}
