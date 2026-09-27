package jobs

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ErrSubmissionRecorderRequired refuses dispatch without durable job ownership.
var ErrSubmissionRecorderRequired = errors.New("jobs: durable submission recorder is required")

// ErrSubmissionUnconfirmed retains work whose provider acceptance is unknown.
var ErrSubmissionUnconfirmed = errors.New("jobs: provider acceptance is unconfirmed")

// SubmissionError identifies the gateway job that requires reconciliation.
// It exposes no provider identifier or credential.
type SubmissionError struct {
	JobID string
	Cause error
}

func (e *SubmissionError) Error() string {
	return fmt.Sprintf("Provider acceptance is unconfirmed for job %s. Check this job before submitting again.", e.JobID)
}
func (e *SubmissionError) Unwrap() error { return errors.Join(ErrSubmissionUnconfirmed, e.Cause) }

// Dispatch binds the selected catalog offering and its required reservation.
// An empty ReservationID means no budget reservation applies to this attempt.
type Dispatch struct {
	Provider          string
	Model             string
	CatalogGeneration string
	ReservationID     string
}

// SubmissionRecorder must persist BeforeDispatch before any provider call.
// Accepted must persist the provider handle before the caller receives success.
// A failed or missing response does not prove that the provider refused work.
type SubmissionRecorder interface {
	BeforeDispatch(context.Context, Dispatch) error
	Accepted(context.Context, Acceptance) error
}

type submissionRecorder struct {
	service    *Service
	submission Submission
	job        Job
	attempted  bool
	accepted   bool
}

func (j Job) validateSubmission() error {
	if j.SlotReleased && (j.SlotID == "" || !j.State.Terminal()) {
		return ErrInvalidJob
	}
	if j.SubmissionPending {
		if j.State != JobStateQueued || j.HasProviderJob() || j.Accounted() {
			return fmt.Errorf("%w: unconfirmed submission has accepted state", ErrInvalidJob)
		}
		if strings.TrimSpace(j.CatalogGeneration) == "" {
			return fmt.Errorf("%w: unconfirmed submission has no catalog generation", ErrInvalidJob)
		}
	} else if j.CatalogGeneration != "" && !j.HasProviderJob() {
		return fmt.Errorf("%w: confirmed dispatch names no provider job", ErrInvalidJob)
	}
	if j.ReservationID != "" && j.CatalogGeneration == "" {
		return fmt.Errorf("%w: reservation names no catalog generation", ErrInvalidJob)
	}
	return nil
}

func (r *submissionRecorder) BeforeDispatch(ctx context.Context, dispatch Dispatch) error {
	if r.attempted {
		return ErrSubmissionUnconfirmed
	}
	if strings.TrimSpace(dispatch.CatalogGeneration) == "" {
		return ErrInvalidJob
	}
	job, err := New(r.submission.jobID, r.submission.Account, dispatch.Provider, dispatch.Model, r.submission.Operation, r.service.now().UTC())
	if err != nil {
		return err
	}
	job.KeyID = r.submission.KeyID
	job.SlotID = r.submission.slotID
	job.CatalogGeneration = dispatch.CatalogGeneration
	job.ReservationID = dispatch.ReservationID
	job.SubmissionPending = true
	r.job, r.attempted = job, true
	err = r.service.records.Create(ctx, job)
	if errors.Is(err, ErrJobExists) {
		r.attempted = false
	}
	return err
}

func (r *submissionRecorder) Accepted(ctx context.Context, answer Acceptance) error {
	if !r.attempted || r.accepted || answer.Provider != r.job.Provider || answer.Model != r.job.Model {
		return ErrInvalidJob
	}
	job := r.job
	if err := job.AdoptProviderJob(answer.ProviderJobID); err != nil {
		return err
	}
	job.SubmissionPending = false
	if err := applyReport(&job, Report{State: answer.State, Reason: answer.Reason}, r.service.now().UTC()); err != nil {
		return err
	}
	// Persist acceptance even if the submitting client disconnects. This bounded
	// write grants no new provider permission and does not release budget capacity.
	commit, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := r.service.records.Replace(commit, r.job, job); err != nil {
		current, readErr := r.service.records.Get(commit, job.Account, job.ID)
		if readErr != nil || current.SubmissionPending || current.providerJobID != job.providerJobID || current.Provider != job.Provider || current.Model != job.Model {
			return err
		}
		job = current
	}
	r.job, r.accepted = job, true
	return nil
}
