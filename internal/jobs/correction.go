package jobs

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/v2"
	"errors"
	"maps"
	"time"

	"github.com/agentstation/starport/internal/limits"
	"github.com/agentstation/starport/internal/limits/reservation"
	"github.com/agentstation/starport/internal/storage"
)

const (
	correctionReportDelivered = "delivered"
	correctionReportExpired   = "expired"
	correctionReportDisabled  = "disabled"
)

// ErrCorrectionNotFound reports an absent correction within the requested job.
var ErrCorrectionNotFound = errors.New("jobs: correction not found")

// CorrectionIntent retains one authenticated decision and its inspected evidence.
// PreviousID orders all intents. PreviousAppliedID orders effective adjustments.
type CorrectionIntent struct {
	Account           string                 `json:"account"`
	JobID             string                 `json:"job_id"`
	Decision          ReconciliationDecision `json:"decision"`
	PreviousID        string                 `json:"previous_id,omitempty"`
	PreviousAppliedID string                 `json:"previous_applied_id,omitempty"`
	EvidenceBinding   string                 `json:"evidence_binding"`
	BudgetBinding     string                 `json:"budget_binding,omitempty"`
}

// CorrectionAudit separates immutable intent from application and optional reporting.
type CorrectionAudit struct {
	Intent       CorrectionIntent `json:"intent"`
	Applied      bool             `json:"applied"`
	ReportStatus string           `json:"report_status,omitempty"`
	SupersededBy string           `json:"superseded_by,omitempty"`
}

// CorrectionCommit publishes owner mutations with required budget correction.
// A nil callback is valid only for a job without a required reservation.
type CorrectionCommit func(context.Context, CorrectionIntent, []storage.CompareAndSwapMutation) error

// CorrectionRepository owns immutable decisions and their ordered outcome records.
// Methods that write a job compare the complete caller-observed record.
type CorrectionRepository interface {
	CreateCorrection(context.Context, Job, CorrectionIntent) (Job, error)
	InspectCorrection(context.Context, string, string, string) (CorrectionAudit, error)
	ApplyCorrection(context.Context, Job, CorrectionCommit) (Job, error)
	NextCorrectionReport(context.Context, Job) (*CorrectionIntent, error)
	MarkCorrectionReported(context.Context, Job, CorrectionIntent, string) (Job, error)
}

func correctionID(intent *CorrectionIntent) string {
	if intent == nil {
		return ""
	}
	return intent.Decision.DecisionID
}

func copyCorrection(intent *CorrectionIntent) *CorrectionIntent {
	if intent == nil {
		return nil
	}
	result := *intent
	result.Decision.Quantities = maps.Clone(intent.Decision.Quantities)
	return &result
}

func correctionDigest(value any) string {
	data, err := json.Marshal(value, json.Deterministic(true))
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

func digestValid(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size && hex.EncodeToString(decoded) == value
}

func (j Job) correctionEvidenceBinding() string {
	return correctionDigest(struct {
		Identity string
		Decision *ReconciliationDecision
		Provider *LateProviderEvidence
	}{j.reconciliationBinding(), j.adminDecision, j.lateProviderEvidence})
}

// CorrectionBinding binds operator inspection to job evidence and required budget state.
// The settlement owner supplies budgetBinding from its inspected reservation.
func (j Job) CorrectionBinding(budgetBinding string) string {
	return correctionStateBinding(j.correctionEvidenceBinding(), correctionID(j.correctionHead), correctionID(j.correctionApplied), budgetBinding)
}

func correctionStateBinding(evidence, head, applied, budget string) string {
	return correctionDigest(struct{ Evidence, Head, Applied, Budget string }{evidence, head, applied, budget})
}

// NewCorrection retains copied input without changing effective billing.
// The caller must supply an authenticated actor and the inspected budget binding.
func (j Job) NewCorrection(request ReconciliationRequest, actor, budgetBinding string, at time.Time) (CorrectionIntent, error) {
	if err := j.checkCorrectionHorizon(at); err != nil {
		return CorrectionIntent{}, err
	}
	request.Quantities = maps.Clone(request.Quantities)
	intent := CorrectionIntent{Account: j.Account, JobID: j.ID, Decision: ReconciliationDecision{ReconciliationRequest: request, Actor: actor, DecidedAt: at.UTC()}, PreviousID: correctionID(j.correctionHead), PreviousAppliedID: correctionID(j.correctionApplied), EvidenceBinding: j.correctionEvidenceBinding(), BudgetBinding: budgetBinding}
	if !intent.validFor(j) || request.Binding != j.CorrectionBinding(budgetBinding) || (j.correctionHead != nil && at.Before(j.correctionHead.Decision.DecidedAt)) {
		return CorrectionIntent{}, ErrReconciliationInvalid
	}
	return intent, nil
}

func (c CorrectionIntent) validFor(job Job) bool {
	d := c.Decision
	if job.checkCorrectionHorizon(d.DecidedAt) != nil {
		return false
	}
	if job.adminDecision == nil || c.Account != job.Account || c.JobID != job.ID || !d.valid(job.Valuation) || !reconciliationText(d.Actor, 256) || d.Actor == anonymousReconciliationActor || d.DecidedAt.Before(job.TerminalAt) || !digestValid(d.Binding) || !digestValid(c.EvidenceBinding) {
		return false
	}
	if (job.ReservationID == "" && c.BudgetBinding != "") || (job.ReservationID != "" && !digestValid(c.BudgetBinding)) {
		return false
	}
	for _, id := range []string{c.PreviousID, c.PreviousAppliedID} {
		if id != "" && (!reconciliationText(id, 128) || id == d.DecisionID) {
			return false
		}
	}
	return d.DecisionID != job.adminDecision.DecisionID && d.Binding == correctionStateBinding(c.EvidenceBinding, c.PreviousID, c.PreviousAppliedID, c.BudgetBinding) && (c.PreviousAppliedID == "" || c.PreviousID != "")
}

// checkCorrectionHorizon covers jobs whose decision itself settles billing.
// Required reservations use their storage authority's original settlement time.
func (j Job) checkCorrectionHorizon(at time.Time) error {
	if j.ReservationID != "" || j.adminDecision == nil {
		return nil
	}
	if at.Before(j.adminDecision.DecidedAt) {
		return ErrReconciliationInvalid
	}
	if !at.Before(j.adminDecision.DecidedAt.Add(limits.CorrectionHorizon)) {
		return limits.ErrCorrectionExpired
	}
	return nil
}

// Evidence returns copied charge evidence without private administrator details.
func (c CorrectionIntent) Evidence() reservation.Evidence {
	return reservation.Evidence{ID: "job-correction:" + correctionDigest([3]string{c.Account, c.JobID, c.Decision.DecisionID}), NoCharge: c.Decision.Disposition == reconciliationNoCharge, Quantities: maps.Clone(c.Decision.Quantities), Tokens: c.Decision.Tokens}
}

func (j Job) validateCorrections() error {
	if j.correctionHead == nil {
		if j.correctionApplied != nil || j.correctionReported != "" {
			return ErrInvalidJob
		}
		return nil
	}
	if !j.correctionHead.validFor(j) {
		return ErrInvalidJob
	}
	if j.correctionApplied != nil && !j.correctionApplied.validFor(j) {
		return ErrInvalidJob
	}
	if correctionID(j.correctionApplied) == correctionID(j.correctionHead) {
		if !sameCorrectionIntent(*j.correctionApplied, *j.correctionHead) {
			return ErrInvalidJob
		}
	} else if j.correctionHead.PreviousAppliedID != correctionID(j.correctionApplied) {
		return ErrInvalidJob
	}
	if j.correctionReported != "" && (j.correctionApplied == nil || !reconciliationText(j.correctionReported, 128)) {
		return ErrInvalidJob
	}
	return nil
}

func unchangedCorrections(expected, next Job) bool {
	return correctionDigest(expected.correctionHead) == correctionDigest(next.correctionHead) && correctionDigest(expected.correctionApplied) == correctionDigest(next.correctionApplied) && expected.correctionReported == next.correctionReported
}

func sameCorrectionIntent(first, second CorrectionIntent) bool {
	a, err := encodeCorrection(first)
	if err != nil {
		return false
	}
	b, err := encodeCorrection(second)
	return err == nil && bytes.Equal(a, b)
}
