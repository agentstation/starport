package jobs

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/v2"
	"errors"
	"maps"
	"reflect"
	"strings"
	"time"

	"github.com/agentstation/starport/internal/limits/reservation"
	"github.com/agentstation/starport/internal/storage"
)

var (
	// ErrReconciliationInvalid refuses incomplete administrator evidence.
	ErrReconciliationInvalid = errors.New("jobs: invalid administrator reconciliation")
	// ErrReconciliationConflict preserves an existing decision or provider result.
	ErrReconciliationConflict = errors.New("jobs: reconciliation conflicts with retained evidence")
	// ErrAuditRetention keeps administrator decisions in storage.
	ErrAuditRetention = errors.New("jobs: administrator audit record must be retained")
)

// ReconciliationRequest binds operator evidence to an inspected job identity.
// The authenticated actor and decision time come from the service boundary.
type ReconciliationRequest struct {
	DecisionID        string                 `json:"decision_id"`
	Binding           string                 `json:"binding"`
	EvidenceReference string                 `json:"evidence_reference"`
	Reason            string                 `json:"reason"`
	Disposition       string                 `json:"disposition"`
	Quantities        reservation.Quantities `json:"quantities,omitempty"`
	Tokens            int64                  `json:"tokens"`
}

// ReconciliationDecision is the immutable audit stored inside its owning job.
type ReconciliationDecision struct {
	ReconciliationRequest
	Actor     string    `json:"actor"`
	DecidedAt time.Time `json:"decided_at"`
}

// ReconciliationView exposes pinned identity and audit evidence only to administrators.
type ReconciliationView struct {
	Binding              string                  `json:"binding"`
	Account              string                  `json:"account"`
	JobID                string                  `json:"job_id"`
	ReservationID        string                  `json:"reservation_id"`
	Provider             string                  `json:"provider"`
	Model                string                  `json:"model"`
	CatalogGeneration    string                  `json:"catalog_generation"`
	Valuation            *reservation.Valuation  `json:"valuation"`
	Status               string                  `json:"status"`
	Accounted            bool                    `json:"accounted"`
	Decision             *ReconciliationDecision `json:"decision,omitempty"`
	LateProviderEvidence *LateProviderEvidence   `json:"late_provider_evidence,omitempty"`
}

// LateProviderEvidence preserves a response after an administrator decision.
// It contains no asset URL or body and never changes the accepted billing decision.
type LateProviderEvidence struct {
	RequestID   string                `json:"request_id"`
	State       JobState              `json:"state"`
	RecordedAt  time.Time             `json:"recorded_at"`
	Measurement *reservation.Evidence `json:"measurement,omitempty"`
	AssetDigest string                `json:"asset_digest"`
}

func reconciliationText(value string, bound int) bool {
	return strings.TrimSpace(value) != "" && len(value) <= bound && strings.IndexFunc(value, func(r rune) bool { return r < 32 || r == 127 }) < 0
}

func (r ReconciliationRequest) valid(v *reservation.Valuation) bool {
	if !reconciliationText(r.DecisionID, 128) || !reconciliationText(r.EvidenceReference, 2048) || !reconciliationText(r.Reason, 2048) || r.Tokens < 0 {
		return false
	}
	switch r.Disposition {
	case "no_charge":
		return len(r.Quantities) == 0 && r.Tokens == 0
	case "usage":
		if v == nil {
			return false
		}
		_, err := v.NanoUSD(r.Quantities)
		return err == nil
	default:
		return false
	}
}

func (j Job) reconciliationBinding() string {
	data, _ := json.Marshal(struct {
		Account, ID, Reservation, Provider, Model, Generation, Key, Operation string
		Valuation                                                             *reservation.Valuation
	}{j.Account, j.ID, j.ReservationID, j.Provider, j.Model, j.CatalogGeneration, j.KeyID, string(j.Operation), j.Valuation})
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

// BillingEvidence returns copied billing input without private audit details.
// Provider measurements and administrator dispositions remain distinct records.
func (j Job) BillingEvidence() *reservation.Evidence {
	if j.adminDecision == nil {
		return copyMeasurement(j.Measurement)
	}
	d := j.adminDecision
	return &reservation.Evidence{ID: "admin:" + d.Binding, NoCharge: d.Disposition == "no_charge", Quantities: maps.Clone(d.Quantities), Tokens: d.Tokens}
}

// BillingConflict reports late evidence that cannot confirm the accepted decision.
func (j Job) BillingConflict() bool {
	if j.adminDecision == nil || j.lateProviderEvidence == nil {
		return false
	}
	measured := j.lateProviderEvidence.Measurement
	if measured == nil {
		return true
	}
	decision := j.adminDecision
	if decision.Disposition == "no_charge" {
		if measured.Tokens != 0 || j.Valuation == nil {
			return true
		}
		amount, err := j.Valuation.NanoUSD(measured.Quantities)
		return err != nil || amount != 0
	}
	return measured.Tokens != decision.Tokens || !maps.Equal(measured.Quantities, decision.Quantities)
}

// ReconciliationStatus reports safe recovery state to the ordinary job API.
func (j Job) ReconciliationStatus() string {
	if j.adminDecision == nil {
		if j.SubmissionPending {
			return "evidence_required"
		}
		return ""
	}
	if j.BillingConflict() {
		return "provider_evidence_review_required"
	}
	if !j.Accounted() {
		return "administrator_recorded"
	}
	return "administrator_resolved"
}

func (j Job) reconciliationView() ReconciliationView {
	view := ReconciliationView{Binding: j.reconciliationBinding(), Account: j.Account, JobID: j.ID, ReservationID: j.ReservationID, Provider: j.Provider, Model: j.Model, CatalogGeneration: j.CatalogGeneration, Valuation: copyValuation(j.Valuation), Status: j.ReconciliationStatus(), Accounted: j.Accounted()}
	if j.adminDecision != nil {
		decision := *j.adminDecision
		decision.Quantities = maps.Clone(decision.Quantities)
		view.Decision = &decision
	}
	if j.lateProviderEvidence != nil {
		evidence := *j.lateProviderEvidence
		evidence.Measurement = copyMeasurement(evidence.Measurement)
		view.LateProviderEvidence = &evidence
	}
	return view
}

// InspectReconciliation reads the durable audit without provider traffic.
// Its caller must enforce administrator authorization.
func (s *Service) InspectReconciliation(ctx context.Context, account, id string) (ReconciliationView, error) {
	job, err := s.records.Get(ctx, account, id)
	if err != nil {
		return ReconciliationView{}, err
	}
	return job.reconciliationView(), nil
}

// ReconcileAdministrator persists evidence before settlement or slot release.
// Its caller must authenticate the actor and enforce administrator authorization.
func (s *Service) ReconcileAdministrator(ctx context.Context, account, id, actor string, request ReconciliationRequest) (ReconciliationView, error) {
	if !reconciliationText(actor, 256) || actor == "anonymous" {
		return ReconciliationView{}, ErrReconciliationInvalid
	}
	if s.assets == nil {
		return ReconciliationView{}, ErrAssetNotFound
	}
	request.Quantities = maps.Clone(request.Quantities)
	for range 8 {
		job, err := s.records.Get(ctx, account, id)
		if err != nil {
			return ReconciliationView{}, err
		}
		if request.Binding != job.reconciliationBinding() || !request.valid(job.Valuation) {
			return job.reconciliationView(), ErrReconciliationInvalid
		}
		if job.adminDecision != nil {
			previous := job.adminDecision
			equal := previous.Actor == actor && previous.DecisionID == request.DecisionID && previous.Binding == request.Binding && previous.EvidenceReference == request.EvidenceReference && previous.Reason == request.Reason && previous.Disposition == request.Disposition && previous.Tokens == request.Tokens && maps.Equal(previous.Quantities, request.Quantities)
			if !equal {
				return job.reconciliationView(), ErrReconciliationConflict
			}
			job, err = s.recoverNative(ctx, job)
			if err != nil {
				return job.reconciliationView(), err
			}
			if job.BillingConflict() {
				return job.reconciliationView(), ErrReconciliationConflict
			}
			settled, err := s.settleAccounting(ctx, job)
			if errors.Is(err, storage.ErrConflict) {
				continue
			}
			return settled.reconciliationView(), err
		}
		if !job.Native || !job.SubmissionPending || job.Measurement != nil {
			return job.reconciliationView(), ErrReconciliationConflict
		}
		// A retained response wins before any manual decision. A concurrent response
		// either wins the same job CAS or remains available as independent evidence.
		recovered, err := s.recoverNative(ctx, job)
		if err != nil {
			return job.reconciliationView(), err
		}
		if recovered.adminDecision != nil {
			continue
		}
		if !recovered.SubmissionPending {
			return recovered.reconciliationView(), ErrReconciliationConflict
		}
		job = recovered
		next := job
		next.adminDecision = &ReconciliationDecision{ReconciliationRequest: request, Actor: actor, DecidedAt: s.now().UTC()}
		next.SubmissionPending = false
		if err := applyReport(&next, Report{State: JobStateFailed, Reason: "native_response_unavailable"}, next.adminDecision.DecidedAt); err != nil {
			return job.reconciliationView(), err
		}
		if err := s.records.Replace(ctx, job, next); err != nil {
			if errors.Is(err, storage.ErrConflict) {
				continue
			}
			// A lost acknowledgement never authorizes capacity release in this call.
			return job.reconciliationView(), err
		}
		settled, err := s.settleAccounting(ctx, next)
		if errors.Is(err, storage.ErrConflict) {
			continue
		}
		return settled.reconciliationView(), err
	}
	return ReconciliationView{}, storage.ErrConflict
}

func (j Job) validateAdministrator() error {
	if j.adminDecision == nil {
		if j.lateProviderEvidence != nil {
			return ErrInvalidJob
		}
		return nil
	}
	d := j.adminDecision
	if !j.Native || j.SubmissionPending || j.State != JobStateFailed || j.Reason != "native_response_unavailable" || j.Measurement != nil || j.AssetKey != "" || d.Binding != j.reconciliationBinding() || !d.valid(j.Valuation) || !reconciliationText(d.Actor, 256) || d.Actor == "anonymous" || d.DecidedAt.Before(j.CreatedAt) || !d.DecidedAt.Equal(j.TerminalAt) {
		return ErrInvalidJob
	}
	if e := j.lateProviderEvidence; e != nil {
		if e.RequestID == "" || !e.State.Terminal() || e.RecordedAt.Before(j.CreatedAt) {
			return ErrInvalidJob
		}
		measured := j
		measured.Measurement = e.Measurement
		if err := measured.validateNative(); err != nil {
			return err
		}
	}
	return nil
}

func immutableAdministrator(expected, next Job) bool {
	return (expected.adminDecision == nil || reflect.DeepEqual(expected.adminDecision, next.adminDecision)) && (expected.lateProviderEvidence == nil || reflect.DeepEqual(expected.lateProviderEvidence, next.lateProviderEvidence))
}
